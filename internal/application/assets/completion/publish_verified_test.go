package completion_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/completion"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ── Mock implementations of the 2 ports (P0-COMPL-4: Publisher port REMOVED) ──

// keyTriplet builds the canonical map-key for the (jobID, subID, sha256Hex)
// idempotency surface. Uses 0x00 as separator to avoid collision with the
// ":" separator used in splitJobArtifactID.
func keyTriplet(j, a, s string) string { return j + "\x00" + a + "\x00" + s }

// mockPreparer is the canonical post-P0-COMPL-4 mock. In the dedup-refactor
// architecture, the Preparer (typed as *finalizer.ArtifactPreparation in
// production) IS the canonical publish seam: its internal call to
// finalization.PublisherPort.Publish handles validate+sha256+Drive-upload.
// The Service struct no longer holds a Publisher field (the double-publish
// godlike/06 SSOT violation was retired in P0-COMPL-4).
//
// Mock contract:
//   - preparedPub: success-shape returned on every non-error call (Location
//     is the canonical Drive location envelope, as if Prepare had published).
//   - prepareErr: static error returned on EVERY call (overrides
//     transientSequence semantics).
//   - transientSequence: per-call sequence of transient errors followed
//     by a nil terminator (simulates the pkg/retry.Do retry-on-transient
//     loop that Prepare may encounter via its internal finalization.PublisherPort).
type mockPreparer struct {
	calls             atomic.Int64
	transientSequence []error
	prepareErr        error
	preparedPub       finalization.PublishedArtifact

	// Per-ArtifactID control (P1 #14 Scenario 3 partial-failure setup):
	// perArtFailures maps ArtifactID → the error to return on the FIRST
	// call for that artifact; subsequent calls (post-retry) succeed normally
	// (return preparedPub with full Location) UNLESS prepareErr is set.
	perArtFailures map[string]error
}

func (m *mockPreparer) Prepare(ctx context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	m.calls.Add(1)
	if len(m.transientSequence) > 0 {
		next := m.transientSequence[0]
		m.transientSequence = m.transientSequence[1:]
		if next != nil {
			return finalization.PublishedArtifact{}, next
		}
	}
	if m.prepareErr != nil {
		return finalization.PublishedArtifact{}, m.prepareErr
	}
	if m.perArtFailures != nil {
		if e, ok := m.perArtFailures[artifact.ArtifactID]; ok {
			delete(m.perArtFailures, artifact.ArtifactID)
			return finalization.PublishedArtifact{}, e
		}
	}
	pub := m.preparedPub
	pub.ArtifactID = artifact.ArtifactID
	pub.Kind = artifact.Kind
	pub.Filename = artifact.Filename
	pub.MIMEType = artifact.MIMEType
	pub.SizeBytes = artifact.SizeBytes
	pub.SHA256 = artifact.SHA256
	pub.SourceVersion = artifact.SourceVersion
	pub.Requirement = artifact.Requirement
	return pub, nil
}

// mockBookkeeper is an in-memory IdempotencyBookkeeper stub. Pre-seeding
// records + setting isPublished=true forces the short-circuit scenario.
//
// P1 #14 (July 2026): adds LookupByIdempotencyKey method that scans records
// for the FIRST entry whose IdempotencyKey matches (scoped to jobID
// namespace) — simulating a same-key collision-detection surface for the
// publisher flow.
type mockBookkeeper struct {
	mu        sync.Mutex
	records   map[string]*finalization.PublishedArtifact
	isPubTrue atomic.Bool
	lookupErr error
	recordErr error
}

func (m *mockBookkeeper) IsPublished(ctx context.Context, j, a, s string) (bool, error) {
	if !m.isPubTrue.Load() {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.records[keyTriplet(j, a, s)]
	return ok, nil
}

func (m *mockBookkeeper) LookupPublished(ctx context.Context, j, a, s string) (*finalization.PublishedArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lookupErr != nil {
		return nil, m.lookupErr
	}
	pub, ok := m.records[keyTriplet(j, a, s)]
	if !ok {
		return nil, errors.New("not found")
	}
	return pub, nil
}

// LookupByIdempotencyKey (P1 #14) — O(N) scan over the records map for the
// first entry whose (jobID-scoped, persisted.IdempotencyKey) triple matches.
// Forward-prevention against SAME-idem-key / DIFFERENT-content collisions
// (godlike/07 fail-closed).
//
// Forward-pointer (godlike/07 performance): the in-memory mock is O(N) by
// design. The concrete SQLite implementation MUST carry an index on
// (job_id, idempotency_key) to avoid full-table-scan on every publish gate;
// tracked in architecture/current.yaml under PHASE-1C.
func (m *mockBookkeeper) LookupByIdempotencyKey(ctx context.Context, jobID, idempotencyKey string) (*finalization.PublishedArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range m.records {
		if rec.IdempotencyKey != idempotencyKey {
			continue
		}
		// Tight scope: jobID extracted from rec.ArtifactID via the
		// canonical "jobID:subID" convention. Mirrors the canonical
		// ArtifactIdempotencyKey helper's scope discipline
		// ((jobID, subID, sha256Hex) row-level uniqueness), so a
		// collapse across DIFFERENT jobs cannot pass the same
		// idempotency-key.
		recJob := jobIDFromArtifactID(rec.ArtifactID)
		if recJob != jobID {
			continue
		}
		return rec, nil
	}
	return nil, errors.New("not found")
}

func (m *mockBookkeeper) RecordPublished(ctx context.Context, j, a, s string, pub *finalization.PublishedArtifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recordErr != nil {
		return m.recordErr
	}
	if m.records == nil {
		m.records = map[string]*finalization.PublishedArtifact{}
	}
	m.records[keyTriplet(j, a, s)] = pub
	return nil
}

// TestNewService_NilPorts_Errors

// ── Test helpers ────────────────────────────────────────────────────────────

// writeTempFile writes payload to a fresh temp file (t.TempDir auto-cleans).
func writeTempFile(t *testing.T, payload []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "artifact.bin")
	if err := os.WriteFile(p, payload, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return p
}

// newVerified produces a *finalization.VerifiedArtifact with SHA256/SizeBytes
// matching the on-disk file (so the final-checksum pass is satisfied).
func newVerified(t *testing.T, artifactID, localPath string, payload []byte) *finalization.VerifiedArtifact {
	t.Helper()
	return &finalization.VerifiedArtifact{
		ArtifactID:    artifactID,
		Kind:          finalization.KindDocument,
		Filename:      filepath.Base(localPath),
		LocalPath:     localPath,
		MIMEType:      "application/octet-stream",
		SizeBytes:     int64(len(payload)),
		SHA256:        files.SHA256Bytes(payload),
		SourceVersion: 1,
		Requirement:   finalization.ArtifactRequirementRequired,
		IdempotencyKey: remote.ArtifactIdempotencyKey(
			jobIDFromArtifactID(artifactID), subIDFromArtifactID(artifactID),
			files.SHA256Bytes(payload),
		),
	}
}

// jobIDFromArtifactID returns the canonical jobID portion for the
// (jobID:subID) ArtifactID convention. Returns "job-001" for "job-001:script_json"
// etc. Falls back to a literal identifier if no ":" separator is present.
func jobIDFromArtifactID(artifactID string) string {
	if i := indexByte(artifactID, ':'); i >= 0 {
		return artifactID[:i]
	}
	return artifactID
}

func subIDFromArtifactID(artifactID string) string {
	if i := indexByte(artifactID, ':'); i >= 0 {
		return artifactID[i+1:]
	}
	return ""
}

// indexByte returns the byte index of c in s, or -1 if not found.
// Stdlib strings.IndexByte exists but we keep the local helper to avoid
// path-bumping a single-purpose import line.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// transientErr implements the typed RetryableError interface that
// pkg/retry.IsTransient probes for classification. Used by
// TestPublish_TransientRetryOnPrepare to simulate a 401-then-success
// transient sequence occurring inside Preparer.Prepare.
//
// In the post-P0-COMPL-4 architecture, the retry lives in the Service's
// publishOne wrapper (pkg/retry.Do around Preparer.Prepare); the underlying
// concrete ArtifactPreparation handles its own finalization.PublisherPort
// retries for non-prep errors. The Service-level wrapper handles
// prep-level transient errors.
type transientErr struct {
	msg string
}

func (e *transientErr) Error() string     { return e.msg }
func (e *transientErr) IsRetryable() bool { return true }

// terminalPublishErr is a generic non-transient terminal error used by the
// P1 #14 partial-failure loop isolation test to simulate a per-artifact
// failure that the retry loop cannot recover from (no IsTransient marker).
// This represents a typed-error that propagates through the failure path.
type terminalPublishErr struct {
	msg string
}

func (e *terminalPublishErr) Error() string { return e.msg }

// ── 5 EXISTING TESTS (updated for P1 #14 []PublishOutcome signature) ─────────

// Test 1: Happy-path — single artifact routes through Prepare →
// verify-final-checksum → record-idempotency. ALL 10 fields of the
// PublishedArtifact envelope are populated correctly. IdempotencyKey is
// byte-stable per P0.7 (derived via remote.ArtifactIdempotencyKey).
//
// P1 #14 UPDATE: PublishVerifiedArtifacts now returns []PublishOutcome.
// outcome[0] wraps the canonical envelope; outcome[0].Reused=false (fresh),
// outcome[0].Err=nil.
//
// Dedup-invariant: Preparer.prepare is called EXACTLY ONCE per artifact
// (no double-Publish retry loop in this Service). Bookkeeper.RecordPublished
// is called EXACTLY ONCE.
func TestPublish_HappyPath_AllTenFieldsPopulated_SinglePrepareCall(t *testing.T) {
	payload := []byte("happy-path publish pipeline payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-001:script_json", localPath, payload)

	prepMock := &mockPreparer{
		preparedPub: finalization.PublishedArtifact{
			Location: finalization.AssetLocation{
				Provider:    "drive",
				FileID:      "drive-file-id-001",
				WebViewLink: "https://drive.google.com/file/d/drive-file-id-001/view",
			},
		},
	}
	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: got %v, want nil", err)
	}

	outcomes, topErr := svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})
	if topErr != nil {
		t.Fatalf("happy path: got top-err %v, want nil", topErr)
	}
	if len(outcomes) != 1 {
		t.Fatalf("len(outcomes): got %d, want 1", len(outcomes))
	}
	if !outcomes[0].IsSuccess() {
		t.Fatalf("outcome[0] not success (Artifact=%v, Err=%v)", outcomes[0].Artifact, outcomes[0].Err)
	}
	if outcomes[0].Reused {
		t.Errorf("outcome[0].Reused: got true, want false (fresh publish path)")
	}

	out := outcomes[0].Artifact
	if out.ArtifactID != va.ArtifactID {
		t.Errorf("ArtifactID: got %q, want %q", out.ArtifactID, va.ArtifactID)
	}
	if out.Kind != va.Kind {
		t.Errorf("Kind: got %v, want %v", out.Kind, va.Kind)
	}
	if out.Filename != va.Filename {
		t.Errorf("Filename: got %q, want %q", out.Filename, va.Filename)
	}
	if out.MIMEType != va.MIMEType {
		t.Errorf("MIMEType: got %q, want %q", out.MIMEType, va.MIMEType)
	}
	if out.SizeBytes != va.SizeBytes {
		t.Errorf("SizeBytes: got %d, want %d", out.SizeBytes, va.SizeBytes)
	}
	if out.SHA256 != va.SHA256 {
		t.Errorf("SHA256: got %q, want %q", out.SHA256, va.SHA256)
	}
	if out.SourceVersion != va.SourceVersion {
		t.Errorf("SourceVersion: got %d, want %d", out.SourceVersion, va.SourceVersion)
	}
	if out.Requirement != va.Requirement {
		t.Errorf("Requirement: got %v, want %v", out.Requirement, va.Requirement)
	}

	// IdempotencyKey is byte-stable per P0.7 via the canonical helper.
	wantKey := remote.ArtifactIdempotencyKey("job-001", "script_json", va.SHA256)
	if out.IdempotencyKey != wantKey {
		t.Errorf("IdempotencyKey: got %q, want %q", out.IdempotencyKey, wantKey)
	}

	if out.Location.Provider != "drive" {
		t.Errorf("Location.Provider: got %q, want %q", out.Location.Provider, "drive")
	}
	if out.Location.FileID != "drive-file-id-001" {
		t.Errorf("Location.FileID: got %q, want %q", out.Location.FileID, "drive-file-id-001")
	}

	// Dedup-invariant: Prepare called EXACTLY ONCE per artifact.
	if prepMock.calls.Load() != 1 {
		t.Errorf("Preparer calls: got %d, want 1 (P0-COMPL-4 + P1 #14 dedup invariant)", prepMock.calls.Load())
	}
	if _, ok := bkMock.records[keyTriplet("job-001", "script_json", va.SHA256)]; !ok {
		t.Error("Bookkeeper: triple not recorded")
	}
}

// Test 2: Transient retry on Preparer.Prepare. The Preparer returns a
// transientErr (implements pkg/retry.IsRetryable) on the FIRST call and
// success on the SECOND. The Service's publishOne wrapper retries via
// pkg/retry.Do and propagates the post-retry success outcome.
//
// P1 #14 UPDATE: outcome[0] is the canonical success envelope, Reused=false,
// Err=nil.
func TestPublish_TransientRetryOnPrepare(t *testing.T) {
	payload := []byte("transient-then-success payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-001:audio_mix", localPath, payload)

	prepMock := &mockPreparer{
		preparedPub: finalization.PublishedArtifact{
			Location: finalization.AssetLocation{
				Provider: "drive",
				FileID:   "drive-file-id-002",
			},
		},
	}
	prepMock.transientSequence = []error{
		&transientErr{msg: "transient 401 unauthorized"},
		nil, // second call succeeds
	}

	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	outcomes, topErr := svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})
	if topErr != nil {
		t.Fatalf("transient-then-success: got top-err %v, want nil", topErr)
	}
	if len(outcomes) != 1 || !outcomes[0].IsSuccess() {
		t.Fatalf("outcomes: got %d success %v; want 1 success", len(outcomes), outcomes)
	}
	if outcomes[0].Artifact.Location.FileID != "drive-file-id-002" {
		t.Errorf("Location.FileID: got %q, want %q",
			outcomes[0].Artifact.Location.FileID, "drive-file-id-002")
	}

	// Cumulative call count = 2 (1 transient + 1 retry-success).
	if prepMock.calls.Load() != 2 {
		t.Errorf("Preparer cumulative calls: got %d, want 2 (1 transient + 1 retry-success)",
			prepMock.calls.Load())
	}
}

// Test 3: Duplicate → SAME-content idempotent replay (P1 #14 BREAKING
// CHANGE). Bookkeeper has a record whose IdempotencyKey equals the
// canonical key for the in-flight request AND whose SHA matches. Under
// P1 #14, this is the canonical byte-stable replay: top-level err is NIL,
// the outcome carries Reused=true, and Prepare is NEVER re-run.
//
// (Prior P0-COMPL-4 contract wrapped ErrAlreadyPublished. The P1 #14
// contract tests the NEW spec where same-content is reflected in the
// outcome, not as a sentinel.)
func TestPublish_Duplicate_SameContent_IdempotentReplay_ReusedTrue_NilErr(t *testing.T) {
	payload := []byte("same-content idempotent replay payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-002:image_png", localPath, payload)

	cachedPub := &finalization.PublishedArtifact{
		ArtifactID:     va.ArtifactID,
		Kind:           va.Kind,
		Filename:       va.Filename,
		MIMEType:       va.MIMEType,
		SizeBytes:      va.SizeBytes,
		SHA256:         va.SHA256, // SAME sha as in-flight
		SourceVersion:  va.SourceVersion,
		Requirement:    va.Requirement,
		IdempotencyKey: remote.ArtifactIdempotencyKey("job-002", "image_png", va.SHA256),
		Location: finalization.AssetLocation{
			Provider: "drive",
			FileID:   "drive-file-id-cached",
		},
	}

	bkMock := &mockBookkeeper{
		records: map[string]*finalization.PublishedArtifact{
			keyTriplet("job-002", "image_png", va.SHA256): cachedPub,
		},
	}
	prepMock := &mockPreparer{}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	outcomes, topErr := svc.PublishVerifiedArtifacts(
		context.Background(), []*finalization.VerifiedArtifact{va})

	// P1 #14: same-content → NIL topErr.
	if topErr != nil {
		t.Fatalf("same-content replay: top-err = %v; want nil (P1 #14 same-content contract)", topErr)
	}
	if len(outcomes) != 1 {
		t.Fatalf("len(outcomes): got %d, want 1", len(outcomes))
	}
	if outcomes[0].Err != nil {
		t.Errorf("outcome[0].Err: got %v, want nil (same-content replay path)", outcomes[0].Err)
	}
	if !outcomes[0].Reused {
		t.Errorf("outcome[0].Reused: got false, want true (idempotent replay path)")
	}
	if outcomes[0].Artifact == nil {
		t.Fatal("outcome[0].Artifact: nil; want cached envelope")
	}
	if outcomes[0].Artifact.Location.FileID != "drive-file-id-cached" {
		t.Errorf("cached FileID: got %q, want %q",
			outcomes[0].Artifact.Location.FileID, "drive-file-id-cached")
	}

	// Dedup-invariant (P0-COMPL-4 + P1 #14): Preparer NEVER called on replay.
	if prepMock.calls.Load() != 0 {
		t.Errorf("Preparer calls on replay: got %d, want 0", prepMock.calls.Load())
	}
}

// Test 4: Final-checksum mismatch raises ErrFinalChecksumMismatch.
//
// P1 #14 UPDATE: per-art failure is embedded in outcomes[0].Err; top-level
// err is nil (loop isolation discipline). Subsequent artifacts in a batch
// loop continue to publish (verified by TestPublish_P1_14_S3_* below).
func TestPublish_FinalChecksumMismatch_ErrFinalChecksumMismatch(t *testing.T) {
	payload := []byte("original payload for final-checksum test")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-003:cover_image", localPath, payload)

	mutatedPayload := append([]byte(nil), payload...)
	mutatedPayload = append(mutatedPayload, []byte("EXTRA_MUTATED_TAIL")...)
	if err := os.WriteFile(localPath, mutatedPayload, 0o644); err != nil {
		t.Fatalf("mutate file: %v", err)
	}

	prepMock := &mockPreparer{
		preparedPub: finalization.PublishedArtifact{
			Location: finalization.AssetLocation{
				Provider: "drive",
				FileID:   "drive-file-id-final-mismatch",
			},
		},
	}
	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	outcomes, topErr := svc.PublishVerifiedArtifacts(
		context.Background(), []*finalization.VerifiedArtifact{va})

	// P1 #14: top-level err is nil (per-artifact failure absorbed into slice).
	if topErr != nil {
		t.Fatalf("final-checksum: top-err = %v, want nil (per-art failures absorbed into slice)", topErr)
	}
	if len(outcomes) != 1 {
		t.Fatalf("len(outcomes): got %d, want 1", len(outcomes))
	}
	if !errors.Is(outcomes[0].Err, completion.ErrFinalChecksumMismatch) {
		t.Errorf("outcomes[0].Err = %v; want wraps ErrFinalChecksumMismatch via errors.Is", outcomes[0].Err)
	}
	if outcomes[0].Artifact != nil {
		t.Errorf("outcomes[0].Artifact: got non-nil (%+v), want nil (terminal per-art failure)", outcomes[0].Artifact)
	}
	if outcomes[0].Reused {
		t.Errorf("outcomes[0].Reused: got true, want false")
	}

	if prepMock.calls.Load() != 1 {
		t.Errorf("Preparer calls: got %d, want 1", prepMock.calls.Load())
	}
	if _, ok := bkMock.records[keyTriplet("job-003", "cover_image", va.SHA256)]; ok {
		t.Errorf("Bookkeeper recorded drift triple; want recording skipped on final-checksum mismatch")
	}
}

// Test 5: Empty-slice input → ErrPublishEmptySlice. Defensive pre-loop
// input gate fires BEFORE any per-artifact work. Top-level typed error
// semantic unchanged by P1 #14.
func TestPublish_EmptySlice_ErrPublishEmptySlice(t *testing.T) {
	prepMock := &mockPreparer{}
	bkMock := &mockBookkeeper{}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got, err := svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, completion.ErrPublishEmptySlice) {
		t.Errorf("err = %v; want wraps ErrPublishEmptySlice via errors.Is", err)
	}
	if got != nil {
		t.Errorf("got non-nil slice on empty input: %v; want nil", got)
	}
}

// ── 3 NEW P1 #14 SCENARIOS ───────────────────────────────────────────────────

// Test 6 (P1 #14 SCENARIO 1 — same-content replay, alias of Test 3 kept for
// numbered audit-pin): same-content idempotent-replay → nil err,
// Reused=true, Artifact=cached. Already covered by Test 3 above;
// duplicate-numbered here for the explicit user-spec test annotation.
func TestPublish_P1_14_S1_SameContent_NilErr_ReusedTrue(t *testing.T) {
	payload := []byte("P1 #14 Scenario 1 payload: same-content replay")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-P114-S1:scenario_one_replay", localPath, payload)

	cached := &finalization.PublishedArtifact{
		ArtifactID:     va.ArtifactID,
		Kind:           va.Kind,
		Filename:       va.Filename,
		MIMEType:       va.MIMEType,
		SizeBytes:      va.SizeBytes,
		SHA256:         va.SHA256,
		SourceVersion:  va.SourceVersion,
		Requirement:    va.Requirement,
		IdempotencyKey: remote.ArtifactIdempotencyKey("job-P114-S1", "scenario_one_replay", va.SHA256),
		Location: finalization.AssetLocation{
			Provider: "drive",
			FileID:   "drive-file-id-S1-cached",
		},
	}
	bkMock := &mockBookkeeper{
		records: map[string]*finalization.PublishedArtifact{
			keyTriplet("job-P114-S1", "scenario_one_replay", va.SHA256): cached,
		},
	}
	prepMock := &mockPreparer{}

	svc, _ := completion.NewService(prepMock, bkMock)
	outcomes, topErr := svc.PublishVerifiedArtifacts(
		context.Background(), []*finalization.VerifiedArtifact{va})

	if topErr != nil {
		t.Fatalf("scenario 1: top-err = %v; want nil (same-content replay)", topErr)
	}
	if len(outcomes) != 1 {
		t.Fatalf("len(outcomes): got %d, want 1", len(outcomes))
	}
	if outcomes[0].Err != nil {
		t.Errorf("outcomes[0].Err: got %v, want nil (same-content replay)", outcomes[0].Err)
	}
	if !outcomes[0].Reused {
		t.Errorf("outcomes[0].Reused: got false, want true (idempotent replay)")
	}
	if outcomes[0].Artifact == nil ||
		outcomes[0].Artifact.Location.FileID != "drive-file-id-S1-cached" {
		t.Errorf("outcomes[0].Artifact location mismatch: got %+v", outcomes[0].Artifact)
	}
	if prepMock.calls.Load() != 0 {
		t.Errorf("Preparer calls on replay: got %d, want 0", prepMock.calls.Load())
	}
}

// Test 7 (P1 #14 SCENARIO 2 — same-key / different-sha collision): a prior
// record has an IdempotencyKey equal to the canonical key for the IN-FLIGHT
// SHA, but the prior record's SHA differs from the in-flight SHA. This
// simulates a canonical-invariance violation (idempotency-key collision
// with differing content) — the FAIL-CLOSED surface, surfacing via the
// top-level err.
//
// Top-level err IS non-nil (the only typed-error case that escapes the
// loop). The collision outcome at index 0 has Artifact=nil, Reused=false,
// Err=typed.
func TestPublish_P1_14_S2_DifferentContent_ErrIdempotencyKeyConflictDifferingContent(t *testing.T) {
	// The vaInFlight setup: LocalPath empty so final-checksum gate is skipped
	// (the collision surface fires BEFORE Prepare).
	vaInFlight := &finalization.VerifiedArtifact{
		ArtifactID:  "job-P114-S2:scenario_two_collision",
		Kind:        finalization.KindDocument,
		Filename:    "scenario.bin",
		MIMEType:    "application/octet-stream",
		SizeBytes:   11,
		SHA256:      "new-sha-for-in-flight-x",
		Requirement: finalization.ArtifactRequirementRequired,
	}

	// The stale cached record has a STALE SHA but its IdempotencyKey equals
	// the canonical key for the in-flight SHA — i.e. the lookup hits on
	// IdempotencyKey but the SHA comparison fails.
	staleSHA := "stale-sha-from-prior-wiring-bug-y"
	staleCached := &finalization.PublishedArtifact{
		ArtifactID: "job-P114-S2:scenario_two_collision",
		SHA256:     staleSHA,
		IdempotencyKey: remote.ArtifactIdempotencyKey(
			"job-P114-S2", "scenario_two_collision", vaInFlight.SHA256,
		), // COLLISION: stale record carries the canonical key for IN-FLIGHT sha
		Location: finalization.AssetLocation{
			Provider: "drive",
			FileID:   "drive-stale",
		},
	}

	bkMock := &mockBookkeeper{
		records: map[string]*finalization.PublishedArtifact{
			keyTriplet("job-P114-S2", "scenario_two_collision", staleSHA): staleCached,
		},
	}
	prepMock := &mockPreparer{}

	svc, _ := completion.NewService(prepMock, bkMock)
	outcomes, topErr := svc.PublishVerifiedArtifacts(
		context.Background(), []*finalization.VerifiedArtifact{vaInFlight})

	// P1 #14: top-level err is non-nil on SAME-idem-key / DIFFERENT-sha.
	if topErr == nil {
		t.Fatal("scenario 2: top-err = nil; want typed ErrIdempotencyKeyConflictDifferingContent")
	}
	if !errors.Is(topErr, completion.ErrIdempotencyKeyConflictDifferingContent) {
		t.Errorf("top-err = %v; want wraps ErrIdempotencyKeyConflictDifferingContent via errors.Is", topErr)
	}
	if len(outcomes) != 1 {
		t.Fatalf("len(outcomes): got %d, want 1 (collision outcome preserved)", len(outcomes))
	}
	if outcomes[0].Artifact != nil {
		t.Errorf("outcomes[0].Artifact: got non-nil; want nil (collision fail-closed)")
	}
	if outcomes[0].Reused {
		t.Errorf("outcomes[0].Reused: got true, want false")
	}
	if !errors.Is(outcomes[0].Err, completion.ErrIdempotencyKeyConflictDifferingContent) {
		t.Errorf("outcomes[0].Err = %v; want wraps ErrIdempotencyKeyConflictDifferingContent", outcomes[0].Err)
	}

	// Side-effects: Prepare MUST NOT have been called (collision fails closed).
	if prepMock.calls.Load() != 0 {
		t.Errorf("Preparer calls: got %d, want 0 (collision fails closed)", prepMock.calls.Load())
	}
}

// Test 8 (P1 #14 SCENARIO 3 — partial failure + loop isolation): 4
// artifacts in a batch; 3 succeed (fresh publish), 1 fails (terminal
// Prepare error). All 4 outcomes accumulated; top-level err is nil
// (per-art failures absorbed); the failing outcome preserves its typed err.
//
// (The selectiveFailingPublisher from prior turns is gone — replaced by
// mockPreparer.perArtFailures which maps ArtifactID → first-call error.)
func TestPublish_P1_14_S3_PartialFailure_LoopIsolation(t *testing.T) {
	// Build 4 distinct verified artifacts with on-disk files matching their SHA.
	payloads := []string{"alpha-ok-1", "beta-FAIL-2", "gamma-ok-3", "delta-ok-4"}
	vas := make([]*finalization.VerifiedArtifact, 0, 4)
	expectedSuccessIdx := []int{0, 2, 3}
	expectedFailureIdx := []int{1}

	for i, pl := range payloads {
		lp := writeTempFile(t, []byte(pl))
		va := newVerified(t, fmt.Sprintf("job-P114-S3:artifact_%d", i), lp, []byte(pl))
		vas = append(vas, va)
	}

	// mockPreparer fails specifically on the second artifact (index 1)
	// via perArtFailures; all others succeed normally.
	failingArtID := "job-P114-S3:artifact_1"
	prepMock := &mockPreparer{
		preparedPub: finalization.PublishedArtifact{
			Location: finalization.AssetLocation{
				Provider: "drive",
				FileID:   "drive-file-id-P114-S3",
			},
		},
		perArtFailures: map[string]error{
			failingArtID: &terminalPublishErr{msg: "terminal publish failure (simulated Drive 4xx after retry exhaustion)"},
		},
	}
	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, _ := completion.NewService(prepMock, bkMock)
	outcomes, topErr := svc.PublishVerifiedArtifacts(context.Background(), vas)

	// Top-level err is nil (per-art failures absorb into slice; only
	// typed idem-key-different-content would escape the loop).
	if topErr != nil {
		t.Fatalf("scenario 3: top-err = %v; want nil (per-art failures absorbed into slice)", topErr)
	}
	if len(outcomes) != 4 {
		t.Fatalf("len(outcomes): got %d, want 4 (1:1 with input)", len(outcomes))
	}

	// 3 successful outcomes: Artifact non-nil, Reused=false, Err nil.
	for _, idx := range expectedSuccessIdx {
		if !outcomes[idx].IsSuccess() {
			t.Errorf("outcomes[%d] not success (Artifact=%v, Err=%v)",
				idx, outcomes[idx].Artifact, outcomes[idx].Err)
		}
		if outcomes[idx].Reused {
			t.Errorf("outcomes[%d].Reused: got true, want false", idx)
		}
	}

	// 1 failed outcome: Artifact nil, Err non-nil (typed terminal).
	for _, idx := range expectedFailureIdx {
		if outcomes[idx].Artifact != nil {
			t.Errorf("outcomes[%d].Artifact: got non-nil; want nil", idx)
		}
		if outcomes[idx].Err == nil {
			t.Errorf("outcomes[%d].Err: got nil; want typed terminal err", idx)
		}
	}

	// LOOP ISOLATION: every artifact's Publish was attempted independently.
	// Successful artifacts' Preparer calls incremented (3 success calls);
	// the failing artifact's Preparer call incremented (1 terminal
	// attempt, no retry because the error has no IsRetryable marker).
	minExpectedCalls := int64(len(expectedSuccessIdx)) + 1
	if prepMock.calls.Load() < minExpectedCalls {
		t.Errorf("Preparer calls: got %d, want >= %d (loop continued past failure)",
			prepMock.calls.Load(), minExpectedCalls)
	}

	// Verify: each successful artifact's triple was Bookkeeper-recorded.
	for _, idx := range expectedSuccessIdx {
		va := vas[idx]
		jobID := jobIDFromArtifactID(va.ArtifactID)
		subID := subIDFromArtifactID(va.ArtifactID)
		if _, ok := bkMock.records[keyTriplet(jobID, subID, va.SHA256)]; !ok {
			t.Errorf("Bookkeeper: outcomes[%d] success but triple not recorded (jobID=%s subID=%s sha=%s)",
				idx, jobID, subID, va.SHA256)
		}
	}
	// Failure side: failing artifact's triple was NOT recorded.
	failureIdx := expectedFailureIdx[0]
	failureVa := vas[failureIdx]
	jobIDFail := jobIDFromArtifactID(failureVa.ArtifactID)
	subIDFail := subIDFromArtifactID(failureVa.ArtifactID)
	if _, ok := bkMock.records[keyTriplet(jobIDFail, subIDFail, failureVa.SHA256)]; ok {
		t.Errorf("Bookkeeper: outcomes[%d] failure but triple WAS recorded (fail-closed violated)",
			failureIdx)
	}
}

// ── Defensive newService nil-port test (from prior surface) ─────────────────

// Defensive test bonus: nil-port ctor → wrapped ErrPublishInvalidArtifact.
// CRITICAL (interface-nil): the nil-checks MUST succeed; INTERFACE-typed
// slots (not typed pointers) prevent the godlike/07 wire-up-bug regression.
func TestNewService_NilPorts_Errors(t *testing.T) {
	cases := []struct {
		name string
		pr   completion.Preparer
		b    completion.IdempotencyBookkeeper
	}{
		{"nil preparer", nil, &mockBookkeeper{}},
		{"nil bookkeeper", &mockPreparer{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := completion.NewService(tc.pr, tc.b)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, completion.ErrPublishInvalidArtifact) {
				t.Errorf("err = %v; want wraps ErrPublishInvalidArtifact", err)
			}
			if svc != nil {
				t.Errorf("got non-nil Service on nil-port ctor: %v", svc)
			}
		})
	}
}

// Reserve imports for forward-compat P3 instrumentation.
var _ = retry.IsTransient
var _ = sha256.New
var _ = job.StatusPartiallySucceeded
