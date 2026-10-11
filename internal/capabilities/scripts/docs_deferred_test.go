// Package scriptgeneration — docs_deferred_test.go pins the deferred Docs leg
// cutover (TICKET-CORE-READY-DURABLE-DAG steps 2-4):
//
//   - markCoreReady hands the Docs leg to the `script.docs_publish` child and
//     reports the deferral, instead of leaving the pipeline to publish inline
//     behind the child's back;
//   - CompletePostCoreRun completes a deferred run idempotently and refuses to
//     mint a COMPLETED without every required artifact;
//   - the child's identity keys are canonical (one builder, three consumers).
package scriptgeneration

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubDocsPublishEnqueuer records enqueue requests so tests can assert the
// pipeline handed the Docs leg to exactly one child.
type stubDocsPublishEnqueuer struct {
	err   error
	calls int
	last  DocsPublishEnqueue
}

func (s *stubDocsPublishEnqueuer) EnqueueDocsPublish(_ context.Context, req DocsPublishEnqueue) error {
	s.calls++
	s.last = req
	return s.err
}

// deferredTestRequest is a docs-enabled request over the core-ready language set.
func deferredTestRequest() GenerateRequest {
	req := defaultTestRequest()
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	return req
}

func createDeferredRun(t *testing.T, repo *inMemRunRepository, id string, req GenerateRequest) {
	t.Helper()
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: id, Request: req, Status: RunStatusRunning, CurrentStage: StageCompilingAudio,
	}))
}

// TestMarkCoreReady_DefersTerminalFlipWhenChildEnqueued pins the cutover: with
// the enqueuer wired and the core durable, the boundary records CORE_READY,
// submits the child, and reports the deferral so the pipeline skips the inline
// documents() phase and the terminal complete().
func TestMarkCoreReady_DefersTerminalFlipWhenChildEnqueued(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	enqueuer := &stubDocsPublishEnqueuer{}
	runner.SetDocsPublishEnqueuer(enqueuer)

	req := deferredTestRequest()
	createDeferredRun(t, repo, "run-docs-deferred", req)

	exec := &executionRun{
		r: runner, ctx: context.Background(), runID: "run-docs-deferred", req: req,
		exec:   ExecutionContext{JobID: "parent-1", Attempt: 1},
		result: coreReadyResult(),
	}
	require.True(t, exec.markCoreReady(), "a durable core with the enqueuer wired must defer")
	require.True(t, exec.docsDeferred, "the pipeline reads the deferral flag, not the return value")
	require.Equal(t, 1, enqueuer.calls, "exactly one docs child per run")
	require.Equal(t, "parent-1", enqueuer.last.ParentJobID)
	require.Equal(t, "run-docs-deferred", enqueuer.last.RunID)

	stored, err := repo.Get(context.Background(), "run-docs-deferred")
	require.NoError(t, err)
	require.Equal(t, StageCoreReady, stored.CurrentStage)
	require.Equal(t, RunStatusRunning, stored.Status, "the run stays non-terminal: the aggregator owns completion")
}

// TestMarkCoreReady_InlineWhenEnqueuerNil pins the pre-cutover behaviour: with
// no enqueuer the boundary is observability only and the pipeline keeps
// publishing inline.
func TestMarkCoreReady_InlineWhenEnqueuerNil(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()

	req := deferredTestRequest()
	createDeferredRun(t, repo, "run-docs-inline", req)

	exec := &executionRun{
		r: runner, ctx: context.Background(), runID: "run-docs-inline", req: req,
		exec:   ExecutionContext{JobID: "parent-2", Attempt: 1},
		result: coreReadyResult(),
	}
	require.False(t, exec.markCoreReady())
	require.False(t, exec.docsDeferred)

	stored, err := repo.Get(context.Background(), "run-docs-inline")
	require.NoError(t, err)
	require.Equal(t, StageCoreReady, stored.CurrentStage, "the milestone is still recorded")
}

// TestMarkCoreReady_EnqueueErrorFailsClosed pins the fail-closed contract: a
// lost continuation is a failed run, never a parent that deferred its terminal
// flip to a child that was never submitted.
func TestMarkCoreReady_EnqueueErrorFailsClosed(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	want := errors.New("broker unavailable")
	runner.SetDocsPublishEnqueuer(&stubDocsPublishEnqueuer{err: want})

	req := deferredTestRequest()
	createDeferredRun(t, repo, "run-docs-enqueue-fail", req)

	exec := &executionRun{
		r: runner, ctx: context.Background(), runID: "run-docs-enqueue-fail", req: req,
		exec:   ExecutionContext{JobID: "parent-3", Attempt: 1},
		result: coreReadyResult(),
	}
	require.False(t, exec.markCoreReady())
	require.False(t, exec.docsDeferred, "a failed enqueue must not read as deferred")

	stored, err := repo.Get(context.Background(), "run-docs-enqueue-fail")
	require.NoError(t, err)
	require.Equal(t, RunStatusFailed, stored.Status, "the run fails instead of hanging without its child")
}

// completePostCoreRunResult is a core result with the requested document published.
func completePostCoreRunResult() *GenerateResult {
	result := coreReadyResult()
	result.Documents = map[Language]DocumentReference{
		"en": {ID: "doc-id", Link: "https://docs.google.com/document/d/doc-id/edit"},
	}
	return result
}

// TestCompletePostCoreRun pins the aggregator's completion port: idempotent on
// completed runs, refusing without every artifact, and terminal with them.
func TestCompletePostCoreRun(t *testing.T) {
	t.Run("empty run id", func(t *testing.T) {
		runner, _, _, _, _, _, _ := newTestRunner()
		err := runner.CompletePostCoreRun(context.Background(), "  ")
		require.ErrorIs(t, err, ErrRunNotFound)
	})

	t.Run("missing run", func(t *testing.T) {
		runner, _, _, _, _, _, _ := newTestRunner()
		err := runner.CompletePostCoreRun(context.Background(), "run-absent")
		// The repository owns the missing-row signal (the in-memory double
		// reports it untyped, like production's SQL layer); the call must
		// fail, never complete around a run it cannot read.
		require.ErrorContains(t, err, "run-absent")
	})

	t.Run("already completed is a no-op", func(t *testing.T) {
		runner, repo, _, _, _, _, _ := newTestRunner()
		require.NoError(t, repo.Create(context.Background(), &GenerationRun{
			ID: "run-done", Request: deferredTestRequest(), Status: RunStatusCompleted,
			CurrentStage: StageCompleted, Result: completePostCoreRunResult(),
		}))
		require.NoError(t, runner.CompletePostCoreRun(context.Background(), "run-done"))
	})

	t.Run("refuses without every artifact", func(t *testing.T) {
		runner, repo, _, _, _, _, _ := newTestRunner()
		require.NoError(t, repo.Create(context.Background(), &GenerationRun{
			ID: "run-core-only", Request: deferredTestRequest(), Status: RunStatusRunning,
			CurrentStage: StageCoreReady, Result: coreReadyResult(),
		}))
		err := runner.CompletePostCoreRun(context.Background(), "run-core-only")
		require.ErrorIs(t, err, ErrPostCoreNotCompletable)

		stored, gerr := repo.Get(context.Background(), "run-core-only")
		require.NoError(t, gerr)
		require.NotEqual(t, RunStatusCompleted, stored.Status,
			"a run without its documents must never read COMPLETED")
	})

	t.Run("completes with every artifact", func(t *testing.T) {
		runner, repo, _, _, _, _, _ := newTestRunner()
		require.NoError(t, repo.Create(context.Background(), &GenerationRun{
			ID: "run-post-core", Request: deferredTestRequest(), Status: RunStatusRunning,
			CurrentStage: StageCoreReady, Result: completePostCoreRunResult(),
		}))
		require.NoError(t, runner.CompletePostCoreRun(context.Background(), "run-post-core"))

		stored, err := repo.Get(context.Background(), "run-post-core")
		require.NoError(t, err)
		require.Equal(t, RunStatusCompleted, stored.Status)
		require.Equal(t, StageCompleted, stored.CurrentStage)
	})
}

// TestDocsPublishKeys_Canonical pins the single-owner identity keys: the wiring
// enqueuer, the waiting envelope and the aggregator lookup derive the same
// values, so a retried parent addresses the same child row.
func TestDocsPublishKeys_Canonical(t *testing.T) {
	require.Equal(t, "docs-publish:parent-1:run-1",
		DocsPublishActiveKey("parent-1", "run-1"))
	require.Equal(t, "trace-1",
		DocsPublishCorrelationID("parent-1", "trace-1", "run-1"),
		"a parent correlation joins the parent trace")
	require.Equal(t, "parent-1:docs:run-1",
		DocsPublishCorrelationID("parent-1", "  ", "run-1"),
		"without one the key derives from the (parent, run) pair")
}
