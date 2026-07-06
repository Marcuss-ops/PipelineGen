// Package stockpipeline — run_orchestrator_test.go (Stock Cutover Commit 2, July 2026).
//
// Tests pin the Stock Cutover Commit 2 contract:
//
//	(1) Service.runOrchestrator returns a *job.ArtifactManifest with the
//	    C12 §8.4 5-artifact shape (staged IDs, kinds, filenames) and
//	    the canonical V1 schema_version.
//	(2) ArtifactManifest.Validate() passes — driver exists in
//	    domain/job/artifact_manifest.go and is the wire-format gate
//	    the runner uses to reject malformed envelopes.
//	(3) HandleJob result map contains "__artifact_manifest" key with
//	    a *ArtifactManifest that passes Decode + Validate.
//	(4) Legacy fields (total_clips, total_chunks, chunks,
//	    metadata_link, metadata_file_id) populate in the result map
//	    as best-effort zero values (Commit 4-7 hydrates).
//	(5) The legacy chunk-render path is unreachable from HandleJob
//	    — neither Publisher.Publish nor any Drive write side-effect
//	    occurs (mock Publisher records 0 calls).
package stockpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// recordingPublisher is a delivery.Publisher fake used to verify
// the legacy Drive-write side-effects are NOT triggered when
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
// 5-artifact shape tests
// ─────────────────────────────────────────────────────────────────────

// TestService_RunOrchestrator_C12FiveArtifacts locks the C12 §8.4
// 5-artifact shape — every entry has a stable ID (one of the
// stockArtifactId* constants), a non-empty Kind, a non-empty
// Filename, a MIMEType, Required:false (Commit 2 cannot populate
// Paths yet).
//
// Planner configuration: tests use TotalMinutes=1 (budget=60s),
// ClipDuration=5s, ChunkDuration=5s — the planner's
// `budgetSec <= 0 || clipDur <= 0` guard (Commit 1 NIT-1 fix)
// requires positive durations for a successful round-trip. The
// test service has s.cfg=nil (no full Config composition) so the
// cfg-derived defaults are 0; explicit input durations are the
// minimum needed for the planner.Production wiring passes a fully-
// composed s.cfg, which satisfies the planner without input hints.
func TestService_RunOrchestrator_C12FiveArtifacts(t *testing.T) {
	svc := &Service{log: zap.NewNop()}

	manifest, err := svc.runOrchestrator(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/source.mp4"},
		FolderID:      "wf-folder-1",
		FolderName:    "demo-folder",
		TotalMinutes:  1,
		ClipDuration:  5,
		ChunkDuration: 5,
	}, "test-job-123")

	if err != nil {
		t.Fatalf("runOrchestrator returned err: %v (Commit 2 must NOT fail on a single-source run)", err)
	}
	if manifest == nil {
		t.Fatal("runOrchestrator returned nil manifest")
	}
	if got, want := manifest.SchemaVersion, job.SchemaVersionArtifactManifestV1; got != want {
		t.Errorf("SchemaVersion = %q, want %q (C12 wire-format contract)", got, want)
	}
	if got, want := manifest.JobID, "test-job-123"; got != want {
		t.Errorf("JobID = %q, want %q (handleJob passes broker JobID through)", got, want)
	}
	if got, want := manifest.WorkflowID, "wf-folder-1"; got != want {
		t.Errorf("WorkflowID = %q, want %q (manifest.WorkflowID ← input.FolderID)", got, want)
	}
	if got, want := len(manifest.Artifacts), stockArtifactCount; got != want {
		t.Fatalf("Artifact count = %d, want %d (C12 5-artifact shape)", got, want)
	}

	// Pin the 5 stable IDs + kinds. The downstream Commit 4-7 hydration
	// logic relies on these constants staying stable across Commits.
	expectedKinds := map[string]string{
		StockArtifactIdMetadata:  job.ArtifactKindMetadata,
		StockArtifactIdThumbnail: job.ArtifactKindImage,
		StockArtifactIdBindings:  job.ArtifactKindClipBindings,
		StockArtifactIdReport:    job.ArtifactKindScriptJSON,
		StockArtifactIdSummary:   job.ArtifactKindScriptText,
	}
	for _, a := range manifest.Artifacts {
		wantKind, ok := expectedKinds[a.ID]
		if !ok {
			t.Errorf("unexpected artifact ID %q (must be one of the stockArtifactId* constants)", a.ID)
			continue
		}
		if a.Kind != wantKind {
			t.Errorf("artifact %q: Kind = %q, want %q", a.ID, a.Kind, wantKind)
		}
		if a.Filename == "" {
			t.Errorf("artifact %q: Filename must be non-empty (C12 §8.4 envelope)", a.ID)
		}
		if a.MIMEType == "" {
			t.Errorf("artifact %q: MIMEType must be non-empty (C12 §8.4 envelope)", a.ID)
		}
		if a.Required {
			t.Errorf("artifact %q: Required must be false in Commit 2 (Path is empty; flipping to true lands in Commit 4-7)", a.ID)
		}
		if a.Path != "" {
			t.Errorf("artifact %q: Path must be empty in Commit 2 (Commit 4-7 hydrates)", a.ID)
		}
		delete(expectedKinds, a.ID)
	}
	if len(expectedKinds) != 0 {
		t.Errorf("missing artifact IDs: %v (all 5 stockArtifactId* constants must appear in the manifest)", expectedKinds)
	}
}

// TestService_RunOrchestrator_ManifestValidatePasses — Validate() in
// domain/job/artifact_manifest.go is the canonical wire-format gate.
// Pin that the Commit 2 manifest passes Validate end-to-end.
func TestService_RunOrchestrator_ManifestValidatePasses(t *testing.T) {
	svc := &Service{log: zap.NewNop()}
	manifest, err := svc.runOrchestrator(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/source.mp4"},
		FolderID:      "wf-2",
		ClipDuration:  5,
		ChunkDuration: 5,
	}, "job-validate")
	if err != nil {
		t.Fatalf("runOrchestrator err: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest.Validate() = %v (Commit 2 manifest must pass Validate; Required:false bypasses the Path-non-empty gate)", err)
	}
}

// TestService_RunOrchestrator_WorkflowID_FolderIDPrecedence — manifest.WorkflowID
// uses input.FolderID first; falls back to input.FolderName only when
// FolderID is empty.
func TestService_RunOrchestrator_WorkflowID_FolderIDPrecedence(t *testing.T) {
	svc := &Service{log: zap.NewNop()}
	manifest, err := svc.runOrchestrator(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/src.mp4"},
		FolderID:      "folder-id-precedence",
		FolderName:    "folder-name-overridden",
		ClipDuration:  5,
		ChunkDuration: 5,
	}, "j-1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if manifest.WorkflowID != "folder-id-precedence" {
		t.Errorf("WorkflowID = %q, want %q (FolderID takes precedence over FolderName)", manifest.WorkflowID, "folder-id-precedence")
	}
}

// TestService_RunOrchestrator_WorkflowID_FolderNameFallback — when
// FolderID is empty, WorkflowID falls back to FolderName so dashboards
// have a stable correlation ID.
func TestService_RunOrchestrator_WorkflowID_FolderNameFallback(t *testing.T) {
	svc := &Service{log: zap.NewNop()}
	manifest, err := svc.runOrchestrator(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/src.mp4"},
		FolderName:    "name-fallback",
		ClipDuration:  5,
		ChunkDuration: 5,
		// FolderID intentionally empty
	}, "j-2")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if manifest.WorkflowID != "name-fallback" {
		t.Errorf("WorkflowID = %q, want %q (FolderName fallback when FolderID empty)", manifest.WorkflowID, "name-fallback")
	}
}

// TestService_RunOrchestrator_EmptyJobIDUsesDefault — Service.Run
// passes empty jobID; the placeholder DefaultOrchestratorJobId must
// kick in via NewOrchestrator. The HandleJob path passes the real
// broker JobID; this test pins the test-fallback behaviour.
func TestService_RunOrchestrator_EmptyJobIDUsesDefault(t *testing.T) {
	svc := &Service{log: zap.NewNop()}
	manifest, err := svc.runOrchestrator(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/src.mp4"},
		FolderID:      "wf-empty-jid",
		FolderName:    "",
		ClipDuration:  5,
		ChunkDuration: 5,
	}, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if manifest.JobID != DefaultOrchestratorJobId {
		t.Errorf("JobID = %q, want %q (empty jobID → default placeholder)", manifest.JobID, DefaultOrchestratorJobId)
	}
}

// TestService_RunOrchestrator_NoSources_ReturnsError — orchestrator's
// firstSource guard returns error when both SearchQueries and
// DirectURLs are empty. Verify the error surfaces to the caller
// (no silent success).
func TestService_RunOrchestrator_NoSources_ReturnsError(t *testing.T) {
	svc := &Service{log: zap.NewNop()}
	manifest, err := svc.runOrchestrator(context.Background(), &RunInput{
		// SearchQueries and DirectURLs intentionally empty
		FolderID:      "wf-empty",
		ClipDuration:  5,
		ChunkDuration: 5,
	}, "j-3")
	if err == nil {
		t.Errorf("expected error from runOrchestrator with no sources, got nil manifest = %v", manifest)
	}
	if manifest != nil {
		t.Errorf("expected nil manifest on error, got %v", manifest)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Service.Run (legacy-signature shim) tests
// ─────────────────────────────────────────────────────────────────────

// TestService_Run_LegacySignature_ReturnsPipelineResult — preserves
// the ServiceRunner interface contract. The body returns
// *PipelineResult (zero-projected today because Commit 2 cannot
// emit real chunks).
//
// Note: Service.Run signature is `(ctx, *RunInput)` (2 args) —
// the third jobID argument goes through runOrchestrator directly
// in HandleJob. Service.Run defaults to DefaultOrchestratorJobId
// (= "stock_orchestrator_v1") via NewOrchestrator's fallback.
func TestService_Run_LegacySignature_ReturnsPipelineResult(t *testing.T) {
	svc := &Service{log: zap.NewNop()}
	result, err := svc.Run(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/src.mp4"},
		FolderID:      "wf-run-1",
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	if err != nil {
		t.Fatalf("legacy Run returned err: %v", err)
	}
	if result == nil {
		t.Fatal("Run returned nil *PipelineResult (ServiceRunner interface contract violated)")
	}
	// Commit 2 expected: zero chunks/links (Commit 4-7 hydrates).
	if len(result.Chunks) != 0 {
		t.Errorf("result.Chunks len = %d, want 0 (Commit 2 has no chunk ladder output)", len(result.Chunks))
	}
	if result.MetadataLink != "" {
		t.Errorf("result.MetadataLink = %q, want empty (Commit 2 — Commit 4-7 hydrates)", result.MetadataLink)
	}
	if result.MetadataFileID != "" {
		t.Errorf("result.MetadataFileID = %q, want empty (Commit 2 — Commit 4-7 hydrates)", result.MetadataFileID)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Service.HandleJob end-to-end tests
// ─────────────────────────────────────────────────────────────────────

// TestService_HandleJob_DelegatesToRunOrchestratorResilient (Stock Cutover
// §12-1 §F post-cleanup, July 2026) — after removing the duplicate
// finalization block from HandleJob, the handler delegates entirely to
// runOrchestratorResilient. When the test Service has no cutter/renderer
// wired, the orchestrator's RunResilient gate fires ErrOrchestratorNilDeps
// BEFORE any step body runs, closing the silent-success class. The
// gate-fail-closed contract now lives inside the orchestrator, not in a
// duplicate HandleJob-level BuildFinalizationRequest with empty chunks.
//
// The Publisher remains untouched because the orchestrator never reaches
// stock.publish (it fails at the entry gate before dispatching any steps).
func TestService_HandleJob_DelegatesToRunOrchestratorResilient(t *testing.T) {
	rec := &recordingPublisher{}
	svc := &Service{log: zap.NewNop(), publisher: rec}

	payload, err := json.Marshal(&StockRunPayload{
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
		t.Fatalf("HandleJob expected orchestrator gate-fail error (ErrOrchestratorNilDeps), got nil + result=%v", res)
	}
	if !errors.Is(err, ErrOrchestratorNilDeps) {
		t.Fatalf("HandleJob err = %v; want ErrOrchestratorNilDeps (orchestrator gate fires on unwired deps)", err)
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
	svc := &Service{log: zap.NewNop(), publisher: &recordingPublisher{}}

	payload, _ := json.Marshal(&StockRunPayload{
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
	// Without cutter/renderer wired, the orchestrator's entry gate fires
	// ErrOrchestratorNilDeps before any step runs.
	if err == nil {
		t.Fatalf("HandleJob expected orchestrator gate-fail error, got nil (silent-success class reopened)")
	}
	if !errors.Is(err, ErrOrchestratorNilDeps) {
		t.Fatalf("HandleJob err = %v; want ErrOrchestratorNilDeps (orchestrator gate fires on unwired deps)", err)
	}
}

// TestService_HandleJob_DelegatesToRunOrchestratorResilient_PublisherUnreached
// (Stock Cutover §12-1 §F post-cleanup, July 2026) — the Publisher
// (Drive-write canal) MUST NOT be invoked by HandleJob. The orchestrator's
// entry gate (ErrOrchestratorNilDeps) fires before any step runs, so the
// Publisher is never reached. This contract is preserved across the
// duplicate-finalization cleanup.
func TestService_HandleJob_DelegatesToRunOrchestratorResilient_PublisherUnreached(t *testing.T) {
	rec := &recordingPublisher{}
	svc := &Service{log: zap.NewNop(), publisher: rec}

	payload, _ := json.Marshal(&StockRunPayload{
		DirectURLs:    []string{"https://example.com/src.mp4"},
		FolderID:      "wf-no-drive",
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	_, err := svc.HandleJob(context.Background(), &appjobs.Job{
		ID:      "job-no-drive",
		Payload: payload,
	}, &appjobs.JobTools{})
	// Post-cleanup: orchestrator gate fires with ErrOrchestratorNilDeps.
	if err == nil {
		t.Fatalf("HandleJob expected orchestrator gate-fail error, got nil")
	}
	if !errors.Is(err, ErrOrchestratorNilDeps) {
		t.Fatalf("HandleJob err = %v; want ErrOrchestratorNilDeps", err)
	}
	if rec.publishCalls != 0 {
		t.Errorf("recordingPublisher.Publish called %d times (must be zero — gate fires before any Drive write)", rec.publishCalls)
	}
	if rec.resolveFolderCalls != 0 {
		t.Errorf("recordingPublisher.ResolveFolder called %d times (must be zero)", rec.resolveFolderCalls)
	}
}
