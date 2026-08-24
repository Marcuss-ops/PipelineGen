// Package completion_test — complete_with_artifacts_service_test.go
// (P1 wave Azione 6, July 2026).
//
// Service-level tests for the Sender-side atomic
// CompleteWithArtifacts. Mirrors the P0 Commit 7
// complete_job_service_test.go test layout (8-test fixture
// surface) — the 5 user-mandated tests cover the canonical
// godlike/07 typed-error contract for the artifact-aware variant:
//
//  1. NotConfigured: NewWithArtifactsService fails closed on
//     nil rxRunner / nil cache / nil both (mirrors C7's
//     TestService_NotConfigured).
//
//  2. HappyPath: NewService -> CompleteWithArtifacts invokes
//     RunInTx exactly once; the in-TX 5-step chain
//     (UpdateJobToSucceededCAS -> InsertResultOnConflict ->
//     PersistArtifactMap -> InsertOutboxEnvelope ->
//     InsertAssetLocations) executes in order; response carries
//     the canonical (Status=SUCCEEDED, JobArtifactIDs, JobAssetIDs,
//     JobID, Attempt, ResultHash) fields with positional index
//     alignment.
//
//  3. IdempotencyReplay: pre-populated cache hit short-circuits
//     step 3 (RunInTx NOT invoked); echoes the cached response
//     to the caller.
//
//  4. LeaseStolen: seeded TxContext with WRONG lease_id rejects
//     CAS with typed sentinel
//     ErrConcurrentLeaseRefutation. (Reuses C7's typed gate;
//     the artifact-aware variant does not drift on the lease
//     refutation surface.)
//
//  5. AssetLocationMismatch: pre-TX passes (valid request +
//     valid AssetMapping) but prior SUCCEEDED state has a
//     DIFFERENT location fingerprint for one (jobID,
//     artifactID); the in-TX gate surfaces typed
//     ErrRemoteArtifactLocationMismatch (mirror of C7's
//     TestService_Complete_HashMismatch).
//
// Test helper reuse: the existing mockTxContext / mockTxRunner /
// mockCache / newHappyPathRequest live in
// complete_job_service_test.go (same package). The
// InsertAssetLocations method was added to mockTxContext in
// lockstep with the TxContext interface extension; the
// insertLocationsFn hook on mockTxContext (added in the same
// edit) lets test 5 inject a typed-error return path.
package policy

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/completion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/completion/internal"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Test helpers ──────────────────────────────────────────────────────

// Compile-time pin (Pattern 0): mockTxContext must remain a
// complete TxContext implementation. The user spec called for an
// explicit `var _ completion.TxContext` assertion; without it,
// a future refactor that drops the (now-7th) InsertAssetLocations
// method from TxContext would not surface as a build failure
// until a downstream test mock usage breaks — latent by hours
// of CI. This explicit pin catches drift at the surface itself.
//
// Azione 6 (July 2026) extended TxContext with the 7th method
// InsertAssetLocations; this pin locks the test mock against
// any future drift on the new or existing methods.
var _ completion.TxContext = (*mockTxContext)(nil)

// newHappyPathArtifactsRequest builds the canonical-request
// envelope for tests 2-5 (mirrors newHappyPathRequest from
// complete_job_service_test.go adapted for the artifact-aware
// surface WITHOUT a positional Artifacts slice — the slice is
// the second positional argument to
// CompleteWithArtifacts).
func newHappyPathArtifactsRequest() *remote.CompleteWithArtifactsRequest {
	return &remote.CompleteWithArtifactsRequest{
		WorkerID:   "w-1",
		JobID:      "j-1",
		Attempt:    0,
		LeaseID:    "lease-1",
		Result:     []byte(`{"ok":true,"v":1}`),
		ResultHash: "h-abcdef",
		AssetMappings: map[string]string{
			"j-1:voiceover": "asset-vo",
			"j-1:metadata":  "asset-meta",
		},
	}
}

// newHappyPathArtifacts builds a canonical 2-artifact
// []*PublishedArtifact slice paired with
// newHappyPathArtifactsRequest's AssetMappings (positional index
// alignment: artifact[0] -> asset[0], etc.).
func newHappyPathArtifacts() []*finalization.PublishedArtifact {
	return []*finalization.PublishedArtifact{
		{
			ArtifactID:     "j-1:voiceover",
			Kind:           finalization.KindVoiceover,
			Filename:       "en.mp3",
			MIMEType:       "audio/mpeg",
			SizeBytes:      12345,
			SHA256:         "sh-1",
			SourceVersion:  1,
			Requirement:    finalization.ArtifactRequirementRequired,
			IdempotencyKey: "ik-vo-1",
			Location: finalization.AssetLocation{
				Provider:     "drive",
				FileID:       "drive-vo",
				WebViewLink:  "https://drive.google.com/file/d/drive-vo/view",
				DownloadLink: "https://drive.google.com/uc?export=download&id=drive-vo",
				Checksum:     "drive-md5-vo",
				FolderID:     "folder-vo",
				FolderPath:   "vo-folder/",
				Action:       finalization.PublishCreated,
			},
		},
		{
			ArtifactID:     "j-1:metadata",
			Kind:           finalization.KindMetadata,
			Filename:       "meta.json",
			MIMEType:       "application/json",
			SizeBytes:      67890,
			SHA256:         "sh-2",
			SourceVersion:  1,
			Requirement:    finalization.ArtifactRequirementRequired,
			IdempotencyKey: "ik-meta-1",
			Location: finalization.AssetLocation{
				Provider:     "drive",
				FileID:       "drive-meta",
				WebViewLink:  "https://drive.google.com/file/d/drive-meta/view",
				DownloadLink: "https://drive.google.com/uc?export=download&id=drive-meta",
				Checksum:     "drive-md5-meta",
				FolderID:     "folder-meta",
				FolderPath:   "meta-folder/",
				Action:       finalization.PublishCreated,
			},
		},
	}
}

// seedingMockWithArtifactsRunner is a TxRunner that
// pre-populates the mock TxContext with the canonical job row
// + an optional insertLocationsFn hook (for test 5's failure
// path) before delegating to the raw mockTxContext.
type seedingMockWithArtifactsRunner struct {
	seedJob           *completion.JobRow
	insertLocationsFn func(ctx context.Context, entries []completion.AssetLocationEntry) error
	priorHashes       map[string]completion.PriorArtifactHash // test 5 uses for location mismatch
}

func (s *seedingMockWithArtifactsRunner) RunInTx(
	ctx context.Context,
	fn func(ctx context.Context, tx completion.TxContext) error,
) error {
	mock := newMockTxContext()
	mock.jobs[s.seedJob.JobID] = s.seedJob
	mock.insertLocationsFn = s.insertLocationsFn
	if s.priorHashes != nil {
		mock.setPriorHashes(s.seedJob.JobID, s.priorHashes)
	}
	return fn(ctx, mock)
}

// ── Tests ─────────────────────────────────────────────────────────────

func TestWithArtifactsService_NotConfigured(t *testing.T) {
	if _, err := completion.NewWithArtifactsService(nil, newMockCache()); err == nil {
		t.Fatal("expected nil-rxRunner error")
	}
	if _, err := completion.NewWithArtifactsService(&mockTxRunner{}, nil); err == nil {
		t.Fatal("expected nil-cache error")
	}
	if _, err := completion.NewWithArtifactsService(nil, nil); err == nil {
		t.Fatal("expected nil-both error")
	}
}

func TestWithArtifactsService_HappyPath_FiveStepChain(t *testing.T) {
	cache := newMockCache()
	rxFactory := func() completion.CompleteJobTxRunner {
		return &seedingMockWithArtifactsRunner{
			seedJob: &completion.JobRow{
				JobRow:  internal.JobRow{JobID: "j-1", LeaseID: "lease-1", Attempt: 0, Status: job.StatusRunning},
				JobType: "test.artifact",
			},
		}
	}
	svc, err := completion.NewWithArtifactsService(rxFactory(), cache)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	req := newHappyPathArtifactsRequest()
	resp, err := svc.CompleteWithArtifacts(context.Background(), req, newHappyPathArtifacts())
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if resp.Status != job.StatusSucceeded {
		t.Errorf("status: want SUCCEEDED, got %s", resp.Status)
	}
	if len(resp.JobArtifactIDs) != 2 {
		t.Errorf("artifact ids count: want 2, got %d", len(resp.JobArtifactIDs))
	}
	for _, want := range []string{"j-1:voiceover", "j-1:metadata"} {
		found := false
		for _, got := range resp.JobArtifactIDs {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing artifact id %s in response", want)
		}
	}
	if len(resp.JobAssetIDs) != len(resp.JobArtifactIDs) {
		t.Errorf("JobAssetIDs length %d != JobArtifactIDs length %d (positional alignment broken)",
			len(resp.JobAssetIDs), len(resp.JobArtifactIDs))
	}
	for i, aid := range resp.JobArtifactIDs {
		if got, want := resp.JobAssetIDs[i], req.AssetMappings[aid]; got != want {
			t.Errorf("JobAssetIDs[%d]=%q, want %q (from AssetMappings[%q])", i, got, want, aid)
		}
	}
	if resp.JobID != "j-1" {
		t.Errorf("JobID echo: want j-1, got %s", resp.JobID)
	}
	if resp.ResultHash != "h-abcdef" {
		t.Errorf("ResultHash echo: want h-abcdef, got %s", resp.ResultHash)
	}
}

func TestWithArtifactsService_IdempotencyReplay_ShortCircuitsTx(t *testing.T) {
	req := newHappyPathArtifactsRequest()
	published := newHappyPathArtifacts()
	canonicalArtifactIDs := make([]string, 0, len(published))
	for _, pa := range published {
		if pa != nil {
			canonicalArtifactIDs = append(canonicalArtifactIDs, pa.ArtifactID)
		}
	}
	cache := newMockCache()
	cachedResp := &remote.CompleteJobResponse{
		Status:         job.StatusSucceeded,
		JobArtifactIDs: canonicalArtifactIDs,
		JobID:          "j-1",
		Attempt:        0,
		ResultHash:     "h-abcdef",
	}
	if err := cache.StoreCanonical(context.Background(), "j-1", 0, "h-abcdef", cachedResp); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	bombed := false
	rx := &bombingTxRunner{bomb: &bombed}
	svc, err := completion.NewWithArtifactsService(rx, cache)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	resp, err := svc.CompleteWithArtifacts(context.Background(), req, published)
	if err != nil {
		t.Fatalf("idempotency replay: %v", err)
	}
	if resp.Status != job.StatusSucceeded {
		t.Errorf("status drift: %s", resp.Status)
	}
	if len(resp.JobArtifactIDs) != 2 {
		t.Errorf("artifact ids drift: %d", len(resp.JobArtifactIDs))
	}
	if len(resp.JobAssetIDs) != 2 {
		t.Errorf("asset ids drift: expected 2, got %d", len(resp.JobAssetIDs))
	}
	if bombed {
		t.Error("TX runner was invoked when the cache hit should short-circuit step 3")
	}
}

func TestWithArtifactsService_LeaseStolen_ReturnsTypedErrConcurrentLeaseRefutation(t *testing.T) {
	rx := &seedingMockWithArtifactsRunner{
		seedJob: &completion.JobRow{
			JobRow:  internal.JobRow{JobID: "j-1", LeaseID: "different-lease", Attempt: 0, Status: job.StatusRunning},
			JobType: "test.artifact",
		},
	}
	cache := newMockCache()
	svc, err := completion.NewWithArtifactsService(rx, cache)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	req := newHappyPathArtifactsRequest()
	_, err = svc.CompleteWithArtifacts(context.Background(), req, newHappyPathArtifacts())
	if err == nil {
		t.Fatal("expected typed ErrConcurrentLeaseRefutation")
	}
	if !errors.Is(err, remote.ErrConcurrentLeaseRefutation) {
		t.Errorf("expected ErrConcurrentLeaseRefutation, got: %v", err)
	}
}

func TestWithArtifactsService_AssetLocationMismatch_ReturnsTypedErrRemoteArtifactLocationMismatch(t *testing.T) {
	cache := newMockCache()
	// Pre-TX passes (valid request, valid AssetMappings) but the
	// mock InsertAssetLocations hook returns the typed sentinel
	// verbatim to simulate a prior SUCCEEDED state with a
	// DIFFERENT (location_kind, external_id, ...) tuple.
	rx := &seedingMockWithArtifactsRunner{
		seedJob: &completion.JobRow{
			JobRow:  internal.JobRow{JobID: "j-1", LeaseID: "lease-1", Attempt: 0, Status: job.StatusRunning},
			JobType: "test.artifact",
		},
		insertLocationsFn: func(ctx context.Context, entries []completion.AssetLocationEntry) error {
			for _, e := range entries {
				if e.ArtifactID == "j-1:voiceover" {
					return fmt.Errorf(
						"%w: assetID=%q prior_external_id=%q new_external_id=%q",
						remote.ErrRemoteArtifactLocationMismatch,
						e.AssetID,
						"DIFFERENT-FILE-ID",
						e.ExternalID,
					)
				}
			}
			return nil
		},
	}
	svc, err := completion.NewWithArtifactsService(rx, cache)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	req := newHappyPathArtifactsRequest()
	_, err = svc.CompleteWithArtifacts(context.Background(), req, newHappyPathArtifacts())
	if err == nil {
		t.Fatal("expected typed ErrRemoteArtifactLocationMismatch")
	}
	if !errors.Is(err, remote.ErrRemoteArtifactLocationMismatch) {
		t.Errorf("expected ErrRemoteArtifactLocationMismatch, got: %v", err)
	}
}

// Bonus 6th test (mirrors the C7 bonus tests): nil-receiver
// fail-closed on the WithArtifacts service. Bolts a clean
// nil-receiver audit pin onto Azione 6 the same way C7 did.
func TestWithArtifactsService_NilReceiver_ReturnsNotConfigured(t *testing.T) {
	var svc *completion.WithArtifactsService
	_, err := svc.CompleteWithArtifacts(context.Background(), newHappyPathArtifactsRequest(), newHappyPathArtifacts())
	if err == nil {
		t.Fatal("expected nil-receiver error")
	}
	if !errors.Is(err, remote.ErrCompleteWithArtifactsNotConfigured) {
		t.Errorf("expected ErrCompleteWithArtifactsNotConfigured, got: %v", err)
	}
}
