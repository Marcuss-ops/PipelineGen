package completion_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/completion"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
// etc. Falls back to a literal "test-job" if no ":" separator is present
// (hermetic test fixture default).
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

// ── 5 TDD TESTS (P0-COMPL-4 dedup invariants pinned) ───────────────────────

// Test 1: Happy-path — single artifact routes through Prepare →
// verify-final-checksum → record-idempotency. ALL 10 fields of the
// PublishedArtifact envelope are populated correctly. IdempotencyKey is
// byte-stable per P0.7 (derived via remote.ArtifactIdempotencyKey).
//
// Dedup-invariant: Preparer.prepare is called EXACTLY ONCE per artifact
// (no double-Publish retry loop in this Service). Bookkeeper.RecordPublished
// is called EXACTLY ONCE.
func TestPublish_HappyPath_AllTenFieldsPopulated_SinglePrepareCall(t *testing.T) {
	payload := []byte("happy-path publish pipeline payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-001:script_json", localPath, payload)

	// (post-P0-COMPL-4) mockPreparer's preparedPub.Is the canonical
	// success-shape Location envelope; Prepare internally publishes to
	// Drive, then returns this fully-populated envelope.
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

	got, err := svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})
	if err != nil {
		t.Fatalf("happy path: got error %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(output): got %d, want 1", len(got))
	}

	out := got[0]
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

	// Dedup-invariants (P0-COMPL-4): Prepare called EXACTLY ONCE.
	if prepMock.calls.Load() != 1 {
		t.Errorf("Preparer calls: got %d, want 1 (P0-COMPL-4 dedup invariant)", prepMock.calls.Load())
	}
	if _, ok := bkMock.records[keyTriplet("job-001", "script_json", va.SHA256)]; !ok {
		t.Error("Bookkeeper: triple not recorded")
	}
	if got, want := len(bkMock.records), 1; got != want {
		t.Errorf("Bookkeeper record count: got %d, want %d (single canonical record)", got, want)
	}
}

// Test 2: Transient retry on Preparer.Prepare. The Preparer returns a
// transientErr (implements pkg/retry.IsRetryable) on the FIRST call and
// success on the SECOND. The Service's publishOne wrapper retries via
// pkg/retry.Do and propagates the post-retry success outcome.
//
// Dedup-invariant: cumulative calls == 2 (1 transient + 1 success retry)
// — exposes the canonical retry path. NO Publisher-field retries in this
// Service (Publisher was REMOVED in P0-COMPL-4).
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
		nil, // second call: success
	}

	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got, err := svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})
	if err != nil {
		t.Fatalf("transient-then-success: got %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(output): got %d, want 1", len(got))
	}
	if got[0].Location.FileID != "drive-file-id-002" {
		t.Errorf("Location.FileID: got %q, want %q", got[0].Location.FileID, "drive-file-id-002")
	}

	// Cumulative call count = 2 (1 transient + 1 success post-internal-retry).
	if prepMock.calls.Load() != 2 {
		t.Errorf("Preparer cumulative calls: got %d, want 2 (1 transient + 1 retry-success)", prepMock.calls.Load())
	}
}

// Test 3: Duplicate → idempotent short-circuit. Bookkeeper reports the
// triple is already-published; Service MUST NOT call Preparer.Prepare;
// the cached envelope is returned via the output slice position;
// ErrAlreadyPublished surfaces via errors.Is (godlike/07
// no-duplicate-side-effects).
//
// Dedup-invariant: zero Prepare calls on short-circuit; the canonical
// idempotency-key collision path is the single source of truth for
// "do not re-publish".
func TestPublish_Duplicate_AlreadyPublished_IdempotentShortCircuit(t *testing.T) {
	payload := []byte("already-published duplicate payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-002:image_png", localPath, payload)

	cachedPub := &finalization.PublishedArtifact{
		ArtifactID:     va.ArtifactID,
		Kind:           va.Kind,
		Filename:       va.Filename,
		MIMEType:       va.MIMEType,
		SizeBytes:      va.SizeBytes,
		SHA256:         va.SHA256,
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
	bkMock.isPubTrue.Store(true)

	prepMock := &mockPreparer{}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got, err := svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})

	if err == nil {
		t.Fatal("expected error (short-circuit), got nil")
	}
	if !errors.Is(err, completion.ErrAlreadyPublished) {
		t.Errorf("err = %v; want wraps ErrAlreadyPublished", err)
	}

	if len(got) != 1 {
		t.Fatalf("len(output): got %d, want 1 (cached record)", len(got))
	}
	if got[0].Location.FileID != "drive-file-id-cached" {
		t.Errorf("cached FileID: got %q, want %q", got[0].Location.FileID, "drive-file-id-cached")
	}

	// Dedup-invariant (P0-COMPL-4): Preparer NEVER called on short-circuit.
	if prepMock.calls.Load() != 0 {
		t.Errorf("Preparer calls on short-circuit: got %d, want 0", prepMock.calls.Load())
	}
}

// Test 4: Final-checksum mismatch raises ErrFinalChecksumMismatch.
// Preparer returns success (it published to Drive via its internal port),
// BUT the on-disk file has been mutated after the publish (simulates
// Drive-write corruption). Service's post-publish fail-closed invariant
// MUST surface ErrFinalChecksumMismatch.
//
// Dedup-invariant: Preparer called exactly once (the publish DID happen);
// bookkeeper recording is SKIPPED on checksum mismatch
// (godlike/07 no-fake-availability).
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

	_, err = svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})
	if err == nil {
		t.Fatal("expected ErrFinalChecksumMismatch, got nil")
	}
	if !errors.Is(err, completion.ErrFinalChecksumMismatch) {
		t.Errorf("err = %v; want wraps ErrFinalChecksumMismatch via errors.Is", err)
	}

	// Prepare succeeded; failure is post-publish recompute.
	if prepMock.calls.Load() != 1 {
		t.Errorf("Preparer calls: got %d, want 1", prepMock.calls.Load())
	}

	// Bookkeeper MUST NOT contain drift triple (godlike/07 no-fake-availability).
	if _, ok := bkMock.records[keyTriplet("job-003", "cover_image", va.SHA256)]; ok {
		t.Errorf("Bookkeeper recorded drift triple; want recording skipped on final-checksum mismatch")
	}
}

// Test 5: Empty-slice input → ErrPublishEmptySlice. Defensive pre-loop
// input gate fires BEFORE any per-artifact work.
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

// Defensive test bonus: nil-port ctor → wrapped ErrPublishInvalidArtifact.
// Not in the user's 4-test list but essential for godlike/07 wire-up-bug
// surfacing at boot.
//
// CRITICAL (idempotency-of-interface-nil): struct fields are declared as
// INTERFACE types (not typed-nil pointers) so passing the literal `nil`
// in the struct literal assigns an INTERFACE-NIL value. The interface
// comparison `if p == nil` then returns TRUE. If the slots were typed as
// *mockPreparer/*mockBookkeeper, the same `nil` literal would wrap a
// typed-nil POINTER in the interface (interface IS NOT nil), and the
// ctor's nil-check would silently pass through — a godlike/07
// wire-up-bug-detection regression.
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
