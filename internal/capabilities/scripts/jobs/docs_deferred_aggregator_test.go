package jobs

// The deferred Docs leg cutover (TICKET-CORE-READY-DURABLE-DAG step 4): a
// single-item parent that stopped at CORE_READY owns no item children — its
// deferred work is the one `script.docs_publish` child. These tests pin the
// aggregator's five-state machine for that shape: wait on flight, complete +
// flip on publication, re-drive a lost child, fail a dead one, and never mint
// a COMPLETED without the documents behind it.
import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// docsDeferredParent builds a waiting parent carrying the docs-deferred envelope.
func docsDeferredParent(parentID, runID, correlation string) *job.Job {
	raw, err := json.Marshal(map[string]any{
		"run_id":                 runID,
		"parent_state":           "waiting_children",
		"docs_deferred":          true,
		"docs_child_correlation": correlation,
	})
	if err != nil {
		panic(err)
	}
	return &job.Job{ID: parentID, Type: job.TypeScriptGenerate, Status: job.StatusSucceeded, Result: raw}
}

// docsChild builds a docs child row in the given terminal state.
func docsChild(id, correlation string, status job.Status, ok bool, retries int) *job.Job {
	raw, err := json.Marshal(map[string]any{
		"ok":           ok,
		"status":       string(status),
		"kind":         "docs_publish",
		"item_id":      "run-docs",
		"doc_id":       "doc-1",
		"doc_link":     "https://docs.google.com/document/d/doc-1/edit",
		"parent_state": "succeeded",
	})
	if err != nil {
		panic(err)
	}
	return &job.Job{
		ID: id, Type: domainScript.TypeDocsPublish, Status: status,
		CorrelationID: correlation, Result: raw,
		RetryCount: retries, MaxRetries: 2,
	}
}

// stubDocsRunCompleter records post-core completions.
type stubDocsRunCompleter struct {
	err   error
	calls []string
}

func (s *stubDocsRunCompleter) CompletePostCoreRun(_ context.Context, runID string) error {
	s.calls = append(s.calls, runID)
	return s.err
}

// stubDocsChildEnqueuer records re-drives.
type stubDocsChildEnqueuer struct {
	err   error
	calls int
	last  scriptgen.DocsPublishEnqueue
}

func (s *stubDocsChildEnqueuer) EnqueueDocsPublish(_ context.Context, req scriptgen.DocsPublishEnqueue) error {
	s.calls++
	s.last = req
	return s.err
}

// stubDocsRunRepo embeds the repository and records FailRun calls.
type stubDocsRunRepo struct {
	scriptgen.RunRepository
	failed []string
}

func (s *stubDocsRunRepo) FailRun(_ context.Context, input scriptgen.FailRunInput) error {
	s.failed = append(s.failed, input.RunID)
	return nil
}

func docsAggDeps(stub *stubScriptAggregatorJobsService) (ScriptAggregatorDeps, *stubDocsRunCompleter, *stubDocsChildEnqueuer, *stubDocsRunRepo) {
	completer := &stubDocsRunCompleter{}
	enqueuer := &stubDocsChildEnqueuer{}
	repo := &stubDocsRunRepo{}
	return ScriptAggregatorDeps{
		JobsSvc: stub, RunRepo: repo, Logger: zap.NewNop(),
		RunCompleter: completer, DocsEnqueuer: enqueuer,
	}, completer, enqueuer, repo
}

func TestDocsDeferred_ActiveChildWaits(t *testing.T) {
	stub := &stubScriptAggregatorJobsService{
		parentJob: docsDeferredParent("parent-active", "run-docs", "corr-active"),
		childJobs: map[string]*job.Job{
			"docs-1": {ID: "docs-1", Type: domainScript.TypeDocsPublish, Status: job.StatusRunning, CorrelationID: "corr-active"},
		},
	}
	deps, completer, enqueuer, _ := docsAggDeps(stub)
	NewScriptParentAggregator(deps).Tick(context.Background())

	require.Empty(t, stub.flipped, "a parent with work in flight must not flip")
	require.Empty(t, completer.calls, "no run completion while the child works")
	require.Zero(t, enqueuer.calls, "no re-drive while a child is active")
}

func TestDocsDeferred_SucceededChildCompletesAndFlips(t *testing.T) {
	stub := &stubScriptAggregatorJobsService{
		parentJob: docsDeferredParent("parent-done", "run-docs", "corr-done"),
		childJobs: map[string]*job.Job{
			"docs-1": docsChild("docs-1", "corr-done", job.StatusSucceeded, true, 0),
		},
	}
	deps, completer, enqueuer, _ := docsAggDeps(stub)
	NewScriptParentAggregator(deps).Tick(context.Background())

	require.Equal(t, []string{"run-docs"}, completer.calls, "the run must complete through the shared port")
	flip, ok := stub.flipped["parent-done"]
	require.True(t, ok, "the parent must flip once the documents are durable")
	require.Equal(t, job.StatusSucceeded, flip.targetStatus)
	require.Equal(t, "doc-1", flip.result["doc_id"])
	require.Equal(t, "https://docs.google.com/document/d/doc-1/edit", flip.result["doc_link"])
	require.Equal(t, "completed", flip.result["parent_state"])
	require.Zero(t, enqueuer.calls)
}

func TestDocsDeferred_RefusedCompletionNeverFlips(t *testing.T) {
	stub := &stubScriptAggregatorJobsService{
		parentJob: docsDeferredParent("parent-refused", "run-docs", "corr-refused"),
		childJobs: map[string]*job.Job{
			"docs-1": docsChild("docs-1", "corr-refused", job.StatusSucceeded, true, 0),
		},
	}
	deps, completer, _, _ := docsAggDeps(stub)
	completer.err = scriptgen.ErrPostCoreNotCompletable
	NewScriptParentAggregator(deps).Tick(context.Background())

	require.Empty(t, stub.flipped, "a COMPLETED without its documents is a lie the aggregator must not mint")
}

func TestDocsDeferred_MissingChildIsRedriven(t *testing.T) {
	stub := &stubScriptAggregatorJobsService{
		parentJob: docsDeferredParent("parent-lost", "run-docs", "corr-lost"),
		childJobs: map[string]*job.Job{},
	}
	deps, completer, enqueuer, _ := docsAggDeps(stub)
	NewScriptParentAggregator(deps).Tick(context.Background())

	require.Equal(t, 1, enqueuer.calls, "a childless parent must be re-driven, never hung")
	require.Equal(t, "parent-lost", enqueuer.last.ParentJobID)
	require.Equal(t, "run-docs", enqueuer.last.RunID)
	require.Empty(t, stub.flipped)
	require.Empty(t, completer.calls)
}

func TestDocsDeferred_ExhaustedChildFailsParentAndRun(t *testing.T) {
	stub := &stubScriptAggregatorJobsService{
		parentJob: docsDeferredParent("parent-dead", "run-docs", "corr-dead"),
		childJobs: map[string]*job.Job{
			"docs-1": docsChild("docs-1", "corr-dead", job.StatusFailed, false, 2),
		},
	}
	deps, completer, enqueuer, repo := docsAggDeps(stub)
	NewScriptParentAggregator(deps).Tick(context.Background())

	flip, ok := stub.flipped["parent-dead"]
	require.True(t, ok, "a definitively failed Docs leg must fail the parent")
	require.Equal(t, job.StatusFailed, flip.targetStatus)
	require.Equal(t, []string{"run-docs"}, repo.failed, "the run must fail with its parent")
	require.Empty(t, completer.calls)
	require.Zero(t, enqueuer.calls, "an exhausted child must not be re-driven")
}

func TestDocsDeferred_RetryableChildWaits(t *testing.T) {
	stub := &stubScriptAggregatorJobsService{
		parentJob: docsDeferredParent("parent-retry", "run-docs", "corr-retry"),
		childJobs: map[string]*job.Job{
			"docs-1": docsChild("docs-1", "corr-retry", job.StatusFailed, false, 0),
		},
	}
	deps, _, enqueuer, _ := docsAggDeps(stub)
	NewScriptParentAggregator(deps).Tick(context.Background())

	require.Empty(t, stub.flipped, "a child with broker budget left drives its own retry")
	require.Zero(t, enqueuer.calls)
}

func TestDocsDeferred_OkFalseChildIsFailure(t *testing.T) {
	stub := &stubScriptAggregatorJobsService{
		parentJob: docsDeferredParent("parent-okfalse", "run-docs", "corr-okfalse"),
		childJobs: map[string]*job.Job{
			"docs-1": docsChild("docs-1", "corr-okfalse", job.StatusSucceeded, false, 0),
		},
	}
	deps, completer, _, repo := docsAggDeps(stub)
	NewScriptParentAggregator(deps).Tick(context.Background())

	flip, ok := stub.flipped["parent-okfalse"]
	require.True(t, ok, "broker-SUCCEEDED with ok=false is a failure (P0.1 gate)")
	require.Equal(t, job.StatusFailed, flip.targetStatus)
	require.Equal(t, []string{"run-docs"}, repo.failed)
	require.Empty(t, completer.calls, "a false success must never complete the run")
}

// TestIsDocsDeferredRun_DecisionTable pins the handler's deferral predicate:
// only a RUNNING run at CORE_READY with documents requested and a durable
// core takes the waiting envelope. Everything else keeps the legacy
// incomplete-run path (a failed run owns its retry schedule; a completed run
// is already terminal; a run without deferred work must not wait).
func TestIsDocsDeferredRun_DecisionTable(t *testing.T) {
	coreResult := &scriptgen.GenerateResult{Scenes: []scriptgen.Scene{
		{ID: "s0", Text: map[scriptgen.Language]string{"en": "text"}},
	}}
	docsReqVal := scriptgen.GenerateRequest{}
	docsReqVal.Docs = scriptgen.DocumentsConfig{Enabled: true, Languages: []scriptgen.Language{"en"}}
	noDocsReq := scriptgen.GenerateRequest{}

	cases := []struct {
		name string
		run  *scriptgen.GenerationRun
		want bool
	}{
		{"deferred", &scriptgen.GenerationRun{ID: "r", Status: scriptgen.RunStatusRunning, CurrentStage: scriptgen.StageCoreReady, Request: docsReqVal, Result: coreResult}, true},
		{"failed run keeps its retry path", &scriptgen.GenerationRun{ID: "r", Status: scriptgen.RunStatusFailed, CurrentStage: scriptgen.StageCoreReady, Request: docsReqVal, Result: coreResult}, false},
		{"completed run is terminal", &scriptgen.GenerationRun{ID: "r", Status: scriptgen.RunStatusCompleted, CurrentStage: scriptgen.StageCompleted, Request: docsReqVal, Result: coreResult}, false},
		{"docs disabled", &scriptgen.GenerationRun{ID: "r", Status: scriptgen.RunStatusRunning, CurrentStage: scriptgen.StageCoreReady, Request: noDocsReq, Result: coreResult}, false},
		{"broken core", &scriptgen.GenerationRun{ID: "r", Status: scriptgen.RunStatusRunning, CurrentStage: scriptgen.StageCoreReady, Request: docsReqVal, Result: &scriptgen.GenerateResult{}}, false},
		{"nil result", &scriptgen.GenerationRun{ID: "r", Status: scriptgen.RunStatusRunning, CurrentStage: scriptgen.StageCoreReady, Request: docsReqVal}, false},
		{"nil run", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isDocsDeferredRun(tc.run))
		})
	}
}
