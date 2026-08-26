// Package stockpipeline — run_orchestrator_test.go (Stock Cutover, July 2026).
//
// HandleJob end-to-end tests: verify that stockpipeline.Service.HandleJob delegates
// to runOrchestratorResilient and surfaces the orchestrator's typed
// gate-fail error (ErrProductionCutterMissing) when the stockpipeline.Service has
// unwired deps (nil cutter / renderer / finalizer).
//
// RETIRED (July 2026): the 6 runOrchestrator manifest-contract tests
// (TestService_RunOrchestrator_*) and the stockpipeline.Service.Run legacy-signature
// test (TestService_Run_LegacySignature_ReturnsPipelineResult) have
// been removed. The manifest contract is now pinned by the resilient
// tests in run_upload_indexing_test.go, orchestrator_resume_test.go,
// and stock_fake_availability_test.go. The legacy runOrchestrator
// method has been retired; stockpipeline.Service.Run now delegates to runSyncPersist
// → runOrchestratorResilient.
package support

import (
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// recordingPublisher is a delivery.Publisher fake used to verify
// the Drive-write side-effects are NOT triggered when
// HandleJob runs through the orchestrator's typed manifest path
// (no Publisher.Publish / ResolveFolder calls expected).
type recordingPublisher struct {
	publishCalls       int
	resolveFolderCalls int
}

var _ delivery.Publisher = (*recordingPublisher)(nil)

func (r *recordingPublisher) Publish(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
	r.publishCalls++
	return &delivery.PublishResult{
		FileID:       "recording-fake",
		WebViewLink:  "https://drive.example/recording-fake",
		DownloadLink: "https://drive.example/recording-fake/dl",
	}, nil
}

func (r *recordingPublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	r.resolveFolderCalls++
	return "recording-fake-folder", nil
}

// ─────────────────────────────────────────────────────────────────────
// stockpipeline.Service.HandleJob end-to-end tests
// ─────────────────────────────────────────────────────────────────────

// TestService_HandleJob_DelegatesToRunOrchestratorResilient (Stock Cutover
// §12-1 §F post-cleanup, July 2026) — after removing the duplicate
// finalization block from HandleJob, the handler delegates entirely to
// runOrchestratorResilient. When the test stockpipeline.Service has no cutter/renderer
// wired, the orchestrator's RunResilient gate fires stockpipeline.ErrOrchestratorNilDeps
// BEFORE any step body runs, closing the silent-success class. The
// gate-fail-closed contract now lives inside the orchestrator, not in a
// duplicate HandleJob-level stockpipeline.BuildFinalizationRequest with empty chunks.
//
// The Publisher remains untouched because the orchestrator never reaches
// stock.publish (it fails at the entry gate before dispatching any steps).
func TestService_HandleJob_DelegatesToRunOrchestratorResilient(t *testing.T) {
	rec := &recordingPublisher{}
	svc := &stockpipeline.Service{log: zap.NewNop(), publisher: rec}

	payload, err := json.Marshal(&stockpipeline.StockRunPayload{
		DirectURLs:    []string{"https://example.com/source.mp4"},
		FolderID:      "wf-handle-1",
		FolderName:    "demo",
		TotalMinutes:  1,
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	res, err := svc.HandleJob(context.Background(), &appjobs.Job{
		ID:      "broker-job-1",
		Payload: payload,
	}, &appjobs.JobTools{})

	// Post-cleanup contract (July 2026): HandleJob delegates to
	// runOrchestratorResilient, which builds an orchestrator and calls
	// RunResilient. The orchestrator's entry gate rejects nil deps
	// (planner/stager/renderer/stepStore) before any step runs.
	// errors.Is reaches the wrapped sentinel via the typed-error chain
	// (godlike/07).
	if err == nil {
		t.Fatalf("HandleJob expected orchestrator gate-fail error (ErrProductionCutterMissing), got nil + result=%v", res)
	}
	if !errors.Is(err, ErrProductionCutterMissing) {
		t.Fatalf("HandleJob err = %v; want ErrProductionCutterMissing (production orchestrator gate fires on unwired deps)", err)
	}
	// Gate fires BEFORE any step runs, so Publisher remains untouched.
	if rec.publishCalls != 0 {
		t.Errorf("recordingPublisher.Publish called %d times (must be zero — gate fires before any Drive write)", rec.publishCalls)
	}
	if rec.resolveFolderCalls != 0 {
		t.Errorf("recordingPublisher.ResolveFolder called %d times (must be zero)", rec.resolveFolderCalls)
	}
}

// TestService_HandleJob_DelegatesToRunOrchestratorResilient_Companion
// (Stock Cutover §12-1 §F post-cleanup, July 2026) — companion to
// DelegatesToRunOrchestratorResilient. Pins the same contract with
// a different input shape. The orchestrator's entry gate is the
// single canonical fail-closed surface; HandleJob no longer has an
// independent gate.
func TestService_HandleJob_DelegatesToRunOrchestratorResilient_Companion(t *testing.T) {
	svc := &stockpipeline.Service{log: zap.NewNop(), publisher: &recordingPublisher{}}

	payload, _ := json.Marshal(&stockpipeline.StockRunPayload{
		DirectURLs:    []string{"https://example.com/src.mp4"},
		FolderID:      "wf-fields",
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	_, err := svc.HandleJob(context.Background(), &appjobs.Job{
		ID:      "job-fields",
		Payload: payload,
	}, &appjobs.JobTools{})
	// Post-cleanup: HandleJob delegates to runOrchestratorResilient.
	// Without cutter/renderer wired, the production orchestrator's
	// constructor gate fires ErrProductionCutterMissing before any step runs.
	if err == nil {
		t.Fatalf("HandleJob expected orchestrator gate-fail error, got nil (silent-success class reopened)")
	}
	if !errors.Is(err, ErrProductionCutterMissing) {
		t.Fatalf("HandleJob err = %v; want ErrProductionCutterMissing (production orchestrator gate fires on unwired deps)", err)
	}
}

// TestService_HandleJob_DelegatesToRunOrchestratorResilient_PublisherUnreached
// (Stock Cutover §12-1 §F post-cleanup, July 2026) — the Publisher
// (Drive-write canal) MUST NOT be invoked by HandleJob. The orchestrator's
// entry gate (ErrProductionCutterMissing) fires before any step runs, so the
// Publisher is never reached. This contract is preserved across the
// duplicate-finalization cleanup.
func TestService_HandleJob_DelegatesToRunOrchestratorResilient_PublisherUnreached(t *testing.T) {
	rec := &recordingPublisher{}
	svc := &stockpipeline.Service{log: zap.NewNop(), publisher: rec}

	payload, _ := json.Marshal(&stockpipeline.StockRunPayload{
		DirectURLs:    []string{"https://example.com/src.mp4"},
		FolderID:      "wf-no-drive",
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	_, err := svc.HandleJob(context.Background(), &appjobs.Job{
		ID:      "job-no-drive",
		Payload: payload,
	}, &appjobs.JobTools{})
	// Post-cleanup: orchestrator gate fires with ErrProductionCutterMissing.
	if err == nil {
		t.Fatalf("HandleJob expected orchestrator gate-fail error, got nil")
	}
	if !errors.Is(err, ErrProductionCutterMissing) {
		t.Fatalf("HandleJob err = %v; want ErrProductionCutterMissing", err)
	}
	if rec.publishCalls != 0 {
		t.Errorf("recordingPublisher.Publish called %d times (must be zero — gate fires before any Drive write)", rec.publishCalls)
	}
	if rec.resolveFolderCalls != 0 {
		t.Errorf("recordingPublisher.ResolveFolder called %d times (must be zero)", rec.resolveFolderCalls)
	}
}
