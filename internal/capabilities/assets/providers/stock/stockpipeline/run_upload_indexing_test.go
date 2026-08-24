package stockpipeline

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// stubStager è il Commit 1.2 test fixture per il canonical
// assets.SourceStager port (godlike/06 SSOT — sostituisce il
// retired NewNoopSourceStager del local stockpipeline.SourceStager).
// I 3 failure-mode test NON invocano stager.Stage (lo step
// stage_sources dell'orchestrator è Begin/Complete only — vedi
// orchestrator.go), quindi lo stub ritorna un StagedAsset
// deterministico + nil error per passare l'nil-guard
// dell'orchestrator e arrivare ai resilience ports under test.
type stubStager struct{}

func (stubStager) Prepare(_ context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	return &acquisition.PrepareContext{
		LocalPath:    "/tmp/stub-stager",
		CleanupToken: "/tmp/stub-stager",
	}, nil
}

func (stubStager) Release(_ context.Context, _ string) error { return nil }

// stubWriter supporta le 3 failure-mode dei test:
//   - modeA (forceFail=true): ritorna errore alla PRIMA chiamata.
//     Usato dal test (a) per verificare abort immediato dell'orchestrator.
type stubWriter struct {
	calls     int
	forceFail bool
}

func (w *stubWriter) WriteAndEnqueue(_ context.Context, _ *asset.Asset, _ string) error {
	w.calls++
	if w.forceFail {
		return errors.New("simulated outbox insert failure (test stub)")
	}
	return nil
}

// stubBuilder restituisce un manifest invalido per test (b).
// Artifact[0] ha Required:true ma Path:"" — Validate() fallisce
// => orchestrator returns ErrManifestIncomplete.
type stubBuilder struct{}

func (stubBuilder) Build(_, _ string) (*job.ArtifactManifest, error) {
	return &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		Artifacts: []job.Artifact{{
			ID:       "test:incomplete",
			Kind:     job.ArtifactKindMetadata,
			Required: true,
			Path:     "",
		}},
	}, nil
}

// stubProjection supporta test (c): ritorna errore per simulare Qdrant
// offline => orchestrator flips FinalStatus a StatusIndexPending.
type stubProjection struct{}

func (stubProjection) Project(_ context.Context, _ *job.ArtifactManifest) error {
	return errors.New("simulated qdrant offline (test stub)")
}

// fakeSucceedingCutter è il FASE-1-retry (PR-STOCK-ATLASTORCH-DISPATCH
// commit-1, 2026-07-04) test fixture che esercita il path end-to-end
// di stock.extract_clips: ritorna len(req.Jobs) CutItemStatusSucceeded
// per request, garantendo che il loop downstream in
// step_extract_clips.go raggiunga writer.WriteAndEnqueue per ogni clip
// (senza un cutter che produca almeno 1 Succeeded Item, il loop
// short-circuita prima della chiamata writer). Wired nel test (a)
// per asserire l'abort tipato con ErrAtomicDispatchFailed invece del
// silenzio log+continue.
type fakeSucceedingCutter struct{}

func (fakeSucceedingCutter) Cut(_ context.Context, req CutRequest) (CutBatchResult, error) {
	items := make([]CutItemResult, len(req.Jobs))
	for i, j := range req.Jobs {
		_ = os.WriteFile(j.OutputPath, []byte("fake-cut-bytes"), 0o644)
		items[i] = CutItemResult{
			JobID:      j.OutputPath,
			OutputPath: j.OutputPath,
			Status:     CutItemStatusSucceeded,
			SizeBytes:  1024,
		}
	}
	return CutBatchResult{
		SourcePath: req.SourcePath,
		Items:      items,
	}, nil
}

// ─── TEST (a): outbox rollback ─────────────────────────────────
// Spec utente: "outbox not written → DB rollback"
// Contract: writer returns error at first call => RunResilient aborts
//
//	immediately, surfaces ErrAtomicDispatchFailed via errors.Is.
//
// PR-STOCK-ATLASTORCH-DISPATCH commit-1 (2026-07-04): ora wired con
// fakeSucceedingCutter (1 Succeeded Item per CutRequest) per esercitare
// il path end-to-end di stock.extract_clips fino a writer.WriteAndEnqueue.
// Pre-fix il test passava nil, nil per cutter/renderer — lo step
// short-circuitava su cutter==nil e non chiamava mai la write, quindi
// l'abort contract non era mai stato esercitato neanche dopo il fix
// in step_extract_clips.go. Wired il fake cutter per chiudere il gap.
func TestOrchestrator_RunResilient_OutboxRollback(t *testing.T) {
	w := &stubWriter{forceFail: true}
	o := NewOrchestratorWithResilience(
		OrchestratorConfig{JobId: "test-a", Lease: testLease("test-a"), PolicyVersion: "v1", ChunkDurationSec: 5, ClipDurationSec: 5},
		NewDeterministicPlanner(),
		stubStager{},
		fakeSucceedingCutter{}, successNoopRenderer(),
		ResilienceDeps{Builder: stockManifestBuilder{}, Writer: w, Projection: noopProjection{}},
	).
		WithAssetPreparation(&recordingArtifactPreparation{}).
		WithJobFinalizer(stubJobFinalizer{}).
		WithLocalFS(newRealishFakeLocalFS())
	_, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/a.mp4"},
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	if err == nil {
		t.Fatal("expected ErrAtomicDispatchFailed, got nil")
	}
	if !errors.Is(err, ErrAtomicDispatchFailed) {
		t.Errorf("err = %v, want errors.Is(err, ErrAtomicDispatchFailed) == true", err)
	}
	if w.calls != 1 {
		t.Errorf("writer.calls = %d, want 1 (abort on first failure)", w.calls)
	}
}

// ─── TEST (b): manifest-completeness gate ──────────────────────
// Spec utente: "asset missing from manifest → job NOT marked SUCCEEDED"
// Contract: Required:true + empty Path => Validate() fails => Gate
//
//	surfaces ErrManifestIncomplete; summary MUST be nil.
func TestOrchestrator_RunResilient_ManifestGateFails(t *testing.T) {
	o := NewOrchestratorWithResilience(
		OrchestratorConfig{JobId: "test-b", Lease: testLease("test-b"), PolicyVersion: "v1", ChunkDurationSec: 5, ClipDurationSec: 5},
		NewDeterministicPlanner(),
		stubStager{},
		fakeSucceedingCutter{}, successNoopRenderer(),
		ResilienceDeps{Builder: stubBuilder{}, Writer: noopWriter{}, Projection: noopProjection{}},
	).
		WithAssetPreparation(&recordingArtifactPreparation{}).
		WithJobFinalizer(stubJobFinalizer{}).
		WithLocalFS(newRealishFakeLocalFS())
	summary, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/b.mp4"},
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	if err == nil {
		t.Fatal("expected ErrManifestIncomplete, got nil")
	}
	if !errors.Is(err, ErrManifestIncomplete) {
		t.Errorf("err = %v, want errors.Is(err, ErrManifestIncomplete) == true", err)
	}
	if summary != nil {
		t.Errorf("summary must be nil on gate failure, got %v", summary)
	}
}

// ─── TEST (c): Qdrant offline → INDEX_PENDING ──────────────────
// Spec utente: "Qdrant offline → job SUCCEEDED with INDEX_PENDING"
// Contract: projection.Project returns error => RunResilient flips
//
//	FinalStatus a StatusIndexPending, ritorna (manifest, nil).
func TestOrchestrator_RunResilient_QdrantOffline_IndexPending(t *testing.T) {
	o := NewOrchestratorWithResilience(
		OrchestratorConfig{JobId: "test-c", Lease: testLease("test-c"), PolicyVersion: "v1", ChunkDurationSec: 5, ClipDurationSec: 5},
		NewDeterministicPlanner(),
		stubStager{},
		fakeSucceedingCutter{}, successNoopRenderer(),
		ResilienceDeps{Builder: stockManifestBuilder{}, Writer: noopWriter{}, Projection: stubProjection{}},
	).
		WithAssetPreparation(&recordingArtifactPreparation{}).
		WithJobFinalizer(stubJobFinalizer{}).
		WithLocalFS(newRealishFakeLocalFS())
	summary, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/c.mp4"},
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	if err != nil {
		t.Fatalf("RunResilient err = %v (Qdrant offline must NOT surface as error — resilient path flips to INDEX_PENDING)", err)
	}
	if summary == nil {
		t.Fatal("RunResilient returned nil summary on Qdrant offline (artifacts ARE on Drive; only indexing is deferred)")
	}
	if summary.Manifest == nil {
		t.Error("summary.Manifest must be non-nil on Qdrant offline")
	}
	if summary.FinalStatus != job.StatusIndexPending {
		t.Errorf("summary.FinalStatus = %q, want %q", summary.FinalStatus, job.StatusIndexPending)
	}
}
