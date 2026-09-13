// Package scriptgeneration — core_ready_test.go pins the CORE_READY boundary.
//
// CORE_READY is the point where the certified render, the published final
// audio and the canonical script row are durable and only the post-processing
// legs remain (Docs publication here, artifact/Drive finalization in the
// worker). It is NOT a terminal state: SUCCEEDED keeps meaning "every
// requested artifact is published". These tests pin the contract split, the
// resume semantics that make the boundary safe, and the two cases where the
// milestone must stay silent.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// coreReadyResult builds a durable core result: three scenes, text for EN,
// and no published documents.
func coreReadyResult() *GenerateResult {
	return &GenerateResult{
		Scenes: []Scene{
			{ID: "scene-0", Index: 0, Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover}, Text: map[Language]string{"en": "First scene text"}},
			{ID: "scene-1", Index: 1, Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover}, Text: map[Language]string{"en": "Second scene text"}},
			{ID: "scene-2", Index: 2, Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover}, Text: map[Language]string{"en": "Third scene text"}},
		},
	}
}

// TestIsCoreCompletable_IgnoresDocuments pins the core contract: a durable
// scene/text core is complete even though nothing has been published to
// Google Docs yet — that is exactly what makes CORE_READY a distinct,
// honest boundary rather than a synonym for COMPLETED.
func TestIsCoreCompletable_IgnoresDocuments(t *testing.T) {
	result := coreReadyResult()

	require.True(t, IsCoreCompletable(result, []Language{"en"}),
		"a durable scene/text core must be complete before any document is published")
	require.False(t, IsRunCompletable(result, []Language{"en"}),
		"the run must NOT be complete while the requested documents are missing")

	// Publishing the requested document flips only the run contract.
	result.Documents = map[Language]DocumentReference{
		"en": {ID: "doc-id", Link: "https://docs.google.com/document/d/doc-id/edit"},
	}
	require.True(t, IsCoreCompletable(result, []Language{"en"}))
	require.True(t, IsRunCompletable(result, []Language{"en"}),
		"a published document must satisfy the run contract")
}

// TestIsCoreCompletable_RejectsIncompleteCore pins the negative side: the
// milestone must never advertise a core that is not durable.
func TestIsCoreCompletable_RejectsIncompleteCore(t *testing.T) {
	require.False(t, IsCoreCompletable(nil, []Language{"en"}), "nil result is never core-complete")

	empty := &GenerateResult{}
	require.False(t, IsCoreCompletable(empty, []Language{"en"}), "a run with no scenes is not core-complete")

	missingText := &GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "text"}},
		{ID: "scene-1", Index: 1, Text: map[Language]string{}}, // no text for the wanted language
	}}
	require.False(t, IsCoreCompletable(missingText, []Language{"en"}),
		"a scene without text for a wanted language must block core readiness")

	// A document present but incomplete must still fail the run contract.
	partialDoc := coreReadyResult()
	partialDoc.Documents = map[Language]DocumentReference{"en": {ID: "doc-id"}}
	require.True(t, IsCoreCompletable(partialDoc, []Language{"en"}))
	require.False(t, IsRunCompletable(partialDoc, []Language{"en"}),
		"a document without a link is not a published document")
}

// TestResumeFrom_CoreReadyResumesAtDocumentPublish pins the resume semantics
// that make the boundary safe: CORE_READY is a milestone with no work of its
// own, so a resumed attempt must re-enter at the post-processing legs instead
// of replaying the whole run.
func TestResumeFrom_CoreReadyResumesAtDocumentPublish(t *testing.T) {
	run := &GenerationRun{
		ID:           "run-core-ready",
		Status:       RunStatusRunning,
		CurrentStage: StageCoreReady,
	}

	require.Equal(t, StagePublishingDocuments, ResumeFrom(run),
		"a CORE_READY run must resume at the first phase that still has work")
	require.Equal(t, -1, StageIndex(StageCoreReady),
		"CORE_READY is a milestone, not a work phase of stageOrder")
	require.False(t, StageCoreReady.IsTerminal(),
		"CORE_READY must not be terminal: SUCCEEDED keeps its existing meaning")
}

// TestExecutionRun_StartAdoptsDurableResultOnResume pins the prerequisite the
// boundary depends on. Every phase before the resume index is skipped, so a
// resume from CORE_READY (or from PUBLISHING_DOCUMENTS) must continue from the
// checkpointed result; otherwise it would publish documents from an empty
// result and silently discard the completed core work.
func TestExecutionRun_StartAdoptsDurableResultOnResume(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()

	durable := coreReadyResult()
	durable.ScriptID = 42
	run := &GenerationRun{
		ID:           "run-adopt-result",
		Request:      defaultTestRequest(),
		Status:       RunStatusRunning,
		CurrentStage: StageCoreReady,
		Result:       durable,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	require.NoError(t, repo.Create(context.Background(), run))

	exec := &executionRun{
		r:     runner,
		ctx:   context.Background(),
		runID: run.ID,
		req:   defaultTestRequest(),
		exec:  ExecutionContext{Attempt: 1},
	}
	require.True(t, exec.start(), "start must accept a CORE_READY resume")

	require.Equal(t, StageIndex(StagePublishingDocuments), exec.resumeIdx,
		"resume index must point at the post-processing legs")
	require.NotNil(t, exec.result, "the checkpointed result must be adopted, not rebuilt")
	require.Equal(t, int64(42), exec.result.ScriptID,
		"the adopted result must be the durable one, not a fresh empty result")
	require.Len(t, exec.result.Scenes, len(durable.Scenes))
}

// TestMarkCoreReady_RecordsBoundary pins the emitted signal: the milestone is
// recorded as its own stage, the KPI lands on the run, and the durable
// CurrentStage becomes CORE_READY so consumers can observe core availability
// before the post-processing legs finish.
func TestMarkCoreReady_RecordsBoundary(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()

	// The core result carries EN text only, so the request must ask for EN
	// documents: the boundary asserts the core contract for the SAME language
	// set it is deferring, and must stay silent when the core cannot satisfy
	// it (see the negative case below).
	req := defaultTestRequest()
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	docsEnabled, docsLangs, _ := req.ResolveDocsConfig()
	require.True(t, docsEnabled)
	require.Equal(t, []Language{"en"}, docsLangs)

	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: "run-mark-core-ready", Request: req, Status: RunStatusRunning, CurrentStage: StageCompilingAudio,
	}))

	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-core-ready", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	// RecordKPIMilestone ignores a zero offset, so let the run clock advance
	// one measurable millisecond before the boundary fires.
	time.Sleep(2 * time.Millisecond)

	exec := &executionRun{
		r: runner, ctx: ctx, runID: "run-mark-core-ready", req: req,
		exec:   ExecutionContext{Attempt: 1},
		result: coreReadyResult(),
	}
	exec.markCoreReady()
	run.Finish()

	stored, err := repo.Get(context.Background(), "run-mark-core-ready")
	require.NoError(t, err)
	require.Equal(t, StageCoreReady, stored.CurrentStage,
		"the durable run stage must become CORE_READY")
	require.Equal(t, RunStatusRunning, stored.Status,
		"CORE_READY is not terminal: the job is still running its post-processing legs")

	report := run.Report()
	found := false
	for _, stage := range report.Stages {
		if stage.Name == string(StageCoreReady) {
			found = true
		}
	}
	require.True(t, found, "the CORE_READY stage must be recorded, got %+v", report.Stages)
	require.NotZero(t, report.KPIs.CoreReadyMs,
		"the core_ready_ms KPI must be recorded so the boundary is measurable")
}

// TestMarkCoreReady_StaysSilentWithoutDeferredWork pins both no-op cases:
// the milestone must not fire when no post-processing leg is deferred (it
// would just rename COMPLETED), and it must not fire when the core contract
// does not hold (NO-FAKE-AVAILABILITY).
func TestMarkCoreReady_StaysSilentWithoutDeferredWork(t *testing.T) {
	t.Run("docs disabled", func(t *testing.T) {
		runner, repo, _, _, _, _, _ := newTestRunner()
		req := defaultTestRequest()
		req.Docs = DocumentsConfig{Enabled: false}
		req.DocsEnabled = false

		require.NoError(t, repo.Create(context.Background(), &GenerationRun{
			ID: "run-no-docs", Request: req, Status: RunStatusRunning, CurrentStage: StageCompilingAudio,
		}))
		exec := &executionRun{
			r: runner, ctx: context.Background(), runID: "run-no-docs", req: req,
			exec: ExecutionContext{Attempt: 1}, result: coreReadyResult(),
		}
		exec.markCoreReady()

		stored, err := repo.Get(context.Background(), "run-no-docs")
		require.NoError(t, err)
		require.Equal(t, StageCompilingAudio, stored.CurrentStage,
			"with nothing deferred, CORE_READY would be a second name for COMPLETED")
	})

	t.Run("core contract does not hold", func(t *testing.T) {
		runner, repo, _, _, _, _, _ := newTestRunner()
		req := defaultTestRequest()

		require.NoError(t, repo.Create(context.Background(), &GenerationRun{
			ID: "run-broken-core", Request: req, Status: RunStatusRunning, CurrentStage: StageCompilingAudio,
		}))
		exec := &executionRun{
			r: runner, ctx: context.Background(), runID: "run-broken-core", req: req,
			exec: ExecutionContext{Attempt: 1}, result: &GenerateResult{},
		}
		exec.markCoreReady()

		stored, err := repo.Get(context.Background(), "run-broken-core")
		require.NoError(t, err)
		require.Equal(t, StageCompilingAudio, stored.CurrentStage,
			"a run whose core is not durable must never advertise core readiness")
	})
}
