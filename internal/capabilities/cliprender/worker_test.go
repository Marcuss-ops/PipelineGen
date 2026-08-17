package cliprender

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

func newTestWorker(t *testing.T) (*Worker, *fakeMaterializer, *fakeTranscriptResolver) {
	t.Helper()
	resolver := newFakeAssetResolver(map[string]AssetRef{
		"asset-source": {AssetID: "asset-source"},
	})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{
		existing:   &TranscriptResult{AssetID: "asset-source", Language: "en", Text: "existing", Reused: true},
		existingOK: true,
	}
	preparer := newTestPreparer(resolver, mat, tr)
	w, err := NewWorker(preparer, t.TempDir(), zap.NewNop())
	if err != nil {
		panic(err)
	}
	return w, mat, tr
}

func renderJobPayload(t *testing.T, req *RenderRequest) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

// TestWorker_ValidPayload_PreparesAndFailsClosed verifies the full worker
// path: decode → validate → prepare → result envelope emitted + typed
// terminal error (render phase not implemented — fail-closed, never a
// silent success).
func TestWorker_ValidPayload_PreparesAndFailsClosed(t *testing.T) {
	w, mat, _ := newTestWorker(t)

	req := baseRenderRequest()
	payload := renderJobPayload(t, req)

	tools := &job.JobExecutionTools{
		Progress: func(int, string) {},
		Event:    func(string, string, map[string]any) {},
	}
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-1", Payload: payload}, tools)

	if !errors.Is(err, ErrRenderPhaseNotImplemented) {
		t.Fatalf("expected ErrRenderPhaseNotImplemented, got %v", err)
	}
	if result == nil {
		t.Fatal("expected result envelope with prepared artifacts")
	}
	if result["phase"] != "plan_sealed" {
		t.Errorf("phase: got %v, want plan_sealed", result["phase"])
	}
	plan, ok := result["plan"].(map[string]any)
	if !ok || plan["plan_sha256"] == "" {
		t.Errorf("plan envelope: got %v", result["plan"])
	}
	if got := result["contract_id"]; got != OutputContractVeloxEditingClipV1 {
		t.Errorf("contract_id: got %v", got)
	}
	if len(mat.calls) != 1 || mat.calls[0] != "asset-source" {
		t.Errorf("expected source materialization only, got %v", mat.calls)
	}
}

// TestWorker_InvalidPayload_Terminal verifies an undecodable payload fails
// with the typed terminal sentinel before any preparation runs.
func TestWorker_InvalidPayload_Terminal(t *testing.T) {
	w, mat, _ := newTestWorker(t)

	result, err := w.Handle(context.Background(), &job.Job{ID: "job-2", Payload: json.RawMessage(`{not json`)}, nil)
	if !errors.Is(err, ErrInvalidJobPayload) {
		t.Fatalf("expected ErrInvalidJobPayload, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result on invalid payload, got %v", result)
	}
	if len(mat.calls) != 0 {
		t.Errorf("preparation must not run on invalid payload, got %v", mat.calls)
	}
}

// TestWorker_ValidationFailure_Terminal verifies an invalid (non-normalized)
// request fails with the typed terminal sentinel.
func TestWorker_ValidationFailure_Terminal(t *testing.T) {
	w, mat, _ := newTestWorker(t)

	// Missing source_asset_id — fails Validate after Normalize.
	raw, _ := json.Marshal(&RenderRequest{})
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-3", Payload: raw}, nil)
	if !errors.Is(err, ErrInvalidJobPayload) {
		t.Fatalf("expected ErrInvalidJobPayload, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result on validation failure, got %v", result)
	}
	if len(mat.calls) != 0 {
		t.Errorf("preparation must not run on invalid request, got %v", mat.calls)
	}
}

// TestWorker_PrepareFailure_Wrapped verifies a preparation failure surfaces
// as a wrapped error, never a silent success.
func TestWorker_PrepareFailure_Wrapped(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{}
	preparer := newTestPreparer(resolver, mat, tr)
	w, err := NewWorker(preparer, t.TempDir(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	req := baseRenderRequest()
	payload := renderJobPayload(t, req)
	_, err = w.Handle(context.Background(), &job.Job{ID: "job-4", Payload: payload}, nil)
	if err == nil {
		t.Fatal("expected error when asset resolution fails")
	}
	if errors.Is(err, ErrRenderPhaseNotImplemented) {
		t.Fatalf("resolution failure must not be misreported as render-phase sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "resolve source") {
		t.Fatalf("expected the wrapped resolution failure, got %v", err)
	}
}
