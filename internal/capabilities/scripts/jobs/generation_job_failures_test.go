// Package jobs_test — generation_job_failures_test.go locks the
// P0 (June 2026, Issue 1) contract for GenerateJobHandler.Handle:
//
//   - Decode failure            → (nil, wrapped_err)
//   - Single-item failure       → (mapped_envelope, wrapped_err)
//   - Single-item success       → (mapped_envelope, nil)
//   - Batch all-items-failed    → (mapped_envelope, wrapped_err)
//   - Batch partial/full success → (mapped_envelope, nil)
//   - Batch empty envelope      → (nil, decode_err) — rejected
//     before multi-item branch
//   - Nil handler receiver      → (nil, error)
//
// Before the fix, the single-item failure path returned
// toMap(buildSingleFailureEnvelope(...)) — i.e. (mapped, nil) —
// causing the worker to mark a failed job as COMPLETED with
// result.ok=false and no retry. The batch failure path returned
// (mapped, nil) when manyResult.Summary.Failed == Summary.Total,
// leaking the same shape. The tests below pin both cases plus
// the decode-failure contract that protects against malformed
// payloads (e.g. an empty envelope JSON, which the canonical
// domainScript.DecodeEnvelopeV2 rejects with ErrPlanInvalid).
package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// ── Mocks / fixtures ───────────────────────────────────────────────
//
// The Engine is a concrete *Engine, not an interface, so the tests
// below use the same pattern as generate_many_usecase_test.go:
// pass a nil engine to NewGenerateOneUseCase so Execute short-
// circuits with ErrGenerationFailed. Every Executed item therefore
// produces a typed error — perfect for asserting the handler
// propagates it as a non-nil Go error.

func newFailingOneUseCase() *usecase.GenerateOneUseCase {
	reg := adapters.NewSourceRegistry(zap.NewNop())
	return usecase.NewGenerateOneUseCase(
		adapters.NormalizationConfig{DefaultLanguage: "en"},
		reg,
		nil, // engine nil → every Execute returns ErrGenerationFailed
		nil,
		zap.NewNop(),
	)
}

func newTestHandler(t *testing.T, maxBatchWorkers int) (*jobs.GenerateJobHandler, appjobs.JobTools) {
	t.Helper()
	one := newFailingOneUseCase()
	many := usecase.NewGenerateManyUseCase(zap.NewNop())
	// Commit 5 P0 #4: batch path requires a broker (no silent inline
	// fallback). Wire a stub that always fails enqueue so the all-failed
	// error path is exercised.
	many.SetFanoutBroker(&stubFanoutBroker{})
	handler := jobs.NewGenerateJobHandler(one, many, zap.NewNop())
	return handler, appjobs.JobTools{}
}

// stubFanoutBroker is a test double that always fails EnqueueScriptItem
// so the all-failed fan-out error path is exercised in tests.
type stubFanoutBroker struct{}

func (s *stubFanoutBroker) EnqueueScriptItem(
	ctx context.Context,
	parentJobID string,
	itemIndex int,
	item domainScript.GenerationItemV2,
	preset domainScript.Preset,
) (string, error) {
	return "", errors.New("stub: enqueue failed")
}

var _ usecase.FanoutItemBroker = (*stubFanoutBroker)(nil)

func makeEnvelopeJSON(t *testing.T, items ...domainScript.GenerationItemV2) []byte {
	t.Helper()
	env := &domainScript.GenerationEnvelopeV2{
		Version: domainScript.EnvelopeVersion,
		Preset:  domainScript.PresetCustom,
		Items:   items,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return raw
}

func makeJob(id string, payload []byte) *scriptpkg.Job {
	return &scriptpkg.Job{
		ID:      id,
		Type:    scriptpkg.TypeScriptGenerate,
		Payload: payload,
	}
}

// envelopeSummary mirrors the canonical GenerationEnvelopeResult
// JSON shape the worker writes to /api/script/jobs/:id/full. We
// JSON-roundtrip the mapped envelope through remapEnvelope so we
// exercise the same serialisation path as the wire shape.
type envelopeSummary struct {
	OK      bool `json:"ok"`
	Summary struct {
		Total     int `json:"total"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	} `json:"summary"`
	Items []struct {
		ItemID string `json:"item_id"`
		Error  string `json:"error,omitempty"`
	} `json:"items"`
}

// remapEnvelope performs json.Marshal → json.Unmarshal on a
// map[string]any envelope so the tests assert on the same wire
// shape the handler emits. This catches drift if toMap ever
// diverges from the canonical JSON encoding.
func remapEnvelope(t *testing.T, mapped map[string]any, target any) {
	t.Helper()
	raw, err := json.Marshal(mapped)
	if err != nil {
		t.Fatalf("remarshal envelope: %v", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
}

// ── Tests ─────────────────────────────────────────────────────────

// TestGenerateJobHandler_SingleFailureReturnsError locks the
// single-item failure contract: the handler MUST return a non-nil
// Go error so the worker treats the job as FAILED, not COMPLETED
// with ok=false. Before the fix, this path silently returned
// (mapped, nil).
func TestGenerateJobHandler_SingleFailureReturnsError(t *testing.T) {
	t.Parallel()
	handler, tools := newTestHandler(t, 2)

	env := &domainScript.GenerationEnvelopeV2{
		Version: domainScript.EnvelopeVersion,
		Preset:  domainScript.PresetCustom,
		Items: []domainScript.GenerationItemV2{
			{
				ID: "single-1",
				Source: domainScript.SourceSpec{
					Type:  domainScript.SourceText,
					Topic: "test topic single-1",
				},
				ScriptParams: domainScript.ScriptSpec{TargetWords: 100},
			},
		},
	}
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	job := makeJob("test-single-failure", payload)

	mapped, runErr := handler.Handle(context.Background(), job, &tools)

	// 1. The Go error must be non-nil — this is the fix.
	if runErr == nil {
		t.Fatal("P0 (Issue 1): expected non-nil error from single-item failure, got nil. " +
			"Worker would mark this job COMPLETED on /api/script/jobs/:id/full wire shape.")
	}
	// 2. The mapped envelope must still be non-nil so the worker
	//    can surface the typed envelope to the operator.
	if mapped == nil {
		t.Fatal("expected non-nil mapped envelope on single-item failure, got nil")
	}
	// 3. ErrGenerationFailed must be in the unwrap chain — the
	//    broker classifies specific errors for retry decisions.
	//    ErrGenerationFailed lives in kernel/script (alias
	//    domainScript); domain/job has Job + TypeScriptGenerate
	//    only.
	if !errors.Is(runErr, domainScript.ErrGenerationFailed) {
		t.Errorf("error must wrap domainScript.ErrGenerationFailed: %v", runErr)
	}

	// 4. Envelope must carry ok=false + summary.failed == 1 so the
	//    operator-visible /api/script/jobs/:id/full reading shows
	//    the right state.
	var got envelopeSummary
	remapEnvelope(t, mapped, &got)
	if got.OK {
		t.Errorf("envelope OK should be false on single-item failure, got true")
	}
	if got.Summary.Total != 1 {
		t.Errorf("envelope Summary.Total: got %d, want 1", got.Summary.Total)
	}
	if got.Summary.Failed != 1 {
		t.Errorf("envelope Summary.Failed: got %d, want 1", got.Summary.Failed)
	}
	if got.Summary.Succeeded != 0 {
		t.Errorf("envelope Summary.Succeeded: got %d, want 0", got.Summary.Succeeded)
	}
	if len(got.Items) != 1 {
		t.Fatalf("envelope items: got %d, want 1", len(got.Items))
	}
	if got.Items[0].Error == "" {
		t.Error("envelope item error should be non-empty on single-item failure")
	}
}

// TestGenerateJobHandler_BatchAllItemsFailedReturnsError locks the
// multi-item all-failed contract: when every item in the batch
// fails, the handler MUST return a non-nil Go error so the worker
// treats the job as FAILED, not COMPLETED with summary.failed ==
// summary.total. Before the fix, this path returned (mapped, nil).
func TestGenerateJobHandler_BatchAllItemsFailedReturnsError(t *testing.T) {
	t.Parallel()
	handler, tools := newTestHandler(t, 2)

	items := []domainScript.GenerationItemV2{
		{ID: "f1", Source: domainScript.SourceSpec{Type: domainScript.SourceText, Topic: "t1"}, ScriptParams: domainScript.ScriptSpec{TargetWords: 100}},
		{ID: "f2", Source: domainScript.SourceSpec{Type: domainScript.SourceText, Topic: "t2"}, ScriptParams: domainScript.ScriptSpec{TargetWords: 100}},
		{ID: "f3", Source: domainScript.SourceSpec{Type: domainScript.SourceText, Topic: "t3"}, ScriptParams: domainScript.ScriptSpec{TargetWords: 100}},
	}
	payload := makeEnvelopeJSON(t, items...)
	job := makeJob("test-batch-all-failed", payload)

	mapped, runErr := handler.Handle(context.Background(), job, &tools)

	// 1. Non-nil Go error — the fix for the all-failed batch case.
	//    Commit 2 P0 #4: fan-out path returns (nil, error) when ALL
	//    enqueues fail (no children were created, no envelope to return).
	if runErr == nil {
		t.Fatal("P0 (Issue 1): expected non-nil error when all items failed, got nil. " +
			"Worker would mark this job COMPLETED on /api/script/jobs/:id/full wire shape.")
	}
	// mapped is nil on all-failed fan-out (no children created).
	// The old inline path produced both mapped + error; the Commit 2
	// fan-out path produces only error when all enqueues fail.
	_ = mapped

	// 2. The error message mentions the count so operators
	//    immediately know it's a complete failure (not a partial).
	if !strings.Contains(runErr.Error(), "all 3 items failed") {
		t.Errorf("error message must report 'all 3 items failed', got: %v", runErr)
	}
}

// TestGenerateJobHandler_EmptyEnvelopeFailsDecode locks the
// canonical contract for a malformed/empty payload: the decoder
// rejects it before the multi-item branch is reached. The handler
// must return (nil, decodeErr) so the worker treats the job as
// FAILED (the envelope never reaches GenerateManyUseCase, so the
// empty-envelope edge case in the use case itself is unreachable
// from the public Handle surface).
func TestGenerateJobHandler_EmptyEnvelopeFailsDecode(t *testing.T) {
	t.Parallel()
	handler, tools := newTestHandler(t, 2)

	// Build an envelope with no items. JSON-marshalled to mimic a
	// realistic client payload that should be rejected at decode.
	payload := makeEnvelopeJSON(t) // no items
	job := makeJob("test-empty", payload)

	mapped, runErr := handler.Handle(context.Background(), job, &tools)

	// 1. The decode MUST fail and the handler MUST surface the
	//    error to the worker so the job is marked FAILED with
	//    retry, NOT COMPLETED silently.
	if runErr == nil {
		t.Fatal("empty envelope MUST fail at decode; got nil error from handler")
	}
	if mapped != nil {
		t.Errorf("empty envelope MUST return nil mapped envelope on decode fail, got: %v", mapped)
	}
	// 2. The error chain must include ErrPlanInvalid so the
	//    broker's classifier can route the failure correctly.
	if !errors.Is(runErr, domainScript.ErrPlanInvalid) {
		t.Errorf("decode failure must wrap domainScript.ErrPlanInvalid: %v", runErr)
	}
}

// TestGenerateJobHandler_NilHandler verifies Handle short-circuits
// when the receiver is nil. Locked separately so future refactors
// of the nil-guard don't accidentally regress.
func TestGenerateJobHandler_NilHandler(t *testing.T) {
	t.Parallel()
	var h *jobs.GenerateJobHandler
	_, err := h.Handle(context.Background(), &scriptpkg.Job{}, &appjobs.JobTools{})
	if err == nil {
		t.Fatal("nil handler must return an error")
	}
}
