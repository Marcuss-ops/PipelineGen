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
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ── Mock implementations of the 3 ports ────────────────────────────────────

// keyTriplet builds the canonical map-key for the (jobID, subID, sha256Hex)
// idempotency surface. Uses 0x00 as separator to avoid collision with the
// ":" separator used in splitJobArtifactID.
func keyTriplet(j, a, s string) string { return j + "\x00" + a + "\x00" + s }

// mockPublisher is a stateful Publisher stub. Configurable per-test for
// success-after-transient, general errors, or always-fail behavior.
type mockPublisher struct {
	calls             atomic.Int64
	transientSequence []error // explicit sequence returned one-by-one
	succeedLocation   finalization.AssetLocation
}

func (m *mockPublisher) Publish(ctx context.Context, artifact finalization.VerifiedArtifact) (finalization.AssetLocation, error) {
	m.calls.Add(1)
	if len(m.transientSequence) > 0 {
		next := m.transientSequence[0]
		m.transientSequence = m.transientSequence[1:]
		if next != nil {
			return finalization.AssetLocation{}, next
		}
	}
	return m.succeedLocation, nil
}

// mockPreparer is a stateless Preparer stub. Returns a deterministic
// PublishedArtifact envelope (10-field canonical shape) with input identity
// propagated through.
type mockPreparer struct {
	calls       atomic.Int64
	prepareErr  error
	preparedPub finalization.PublishedArtifact
}

func (m *mockPreparer) Prepare(ctx context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	m.calls.Add(1)
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
	}
}

// transientErr implements pkg/retry.RetryableError via the IsRetryable()
// method, qualifying as a TRANSIENT error per pkg/retry.IsTransient probe.
// Used by TestPublish_FourOhOne_ThenSuccess to simulate a 401-then-success
// scenario where pkg/retry.Do retries the Publish call after the transient.
type transientErr struct {
	msg string
}

func (e *transientErr) Error() string     { return e.msg }
func (e *transientErr) IsRetryable() bool { return true }

// ── 5 TDD TESTS ─────────────────────────────────────────────────────────────

// Test 1: Happy-path — single artifact routes through Prepare → Publish →
// verify-final-checksum → record-idempotency. ALL 10 fields of the
// PublishedArtifact envelope are populated correctly. IdempotencyKey is
// byte-stable per P0.7 (derived via remote.ArtifactIdempotencyKey).
func TestPublish_HappyPath_AllTenFieldsPopulated(t *testing.T) {
	payload := []byte("happy-path publish pipeline payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-001:script_json", localPath, payload)

	pubMock := &mockPublisher{
		succeedLocation: finalization.AssetLocation{
			Provider:    "drive",
			FileID:      "drive-file-id-001",
			WebViewLink: "https://drive.google.com/file/d/drive-file-id-001/view",
		},
	}
	prepMock := &mockPreparer{}
	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, err := completion.NewService(pubMock, prepMock, bkMock)
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

	// Side-effects: Prepare + Publish called once; bookkeeper recorded.
	if prepMock.calls.Load() != 1 {
		t.Errorf("Preparer calls: got %d, want 1", prepMock.calls.Load())
	}
	if pubMock.calls.Load() != 1 {
		t.Errorf("Publisher calls: got %d, want 1", pubMock.calls.Load())
	}
	if _, ok := bkMock.records[keyTriplet("job-001", "script_json", va.SHA256)]; !ok {
		t.Error("Bookkeeper: triple not recorded")
	}
}

// Test 2: 401 → retry — Publisher returns transient 401 on FIRST call,
// success on SECOND. Service propagates the post-retry success outcome.
// The concrete ArtifactPublisherAdapter retries 401 internally per P1.5;
// this test verifies the Service correctly forwards the resolved outcome.
func TestPublish_FourOhOne_ThenSuccess(t *testing.T) {
	payload := []byte("401-then-success payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-001:audio_mix", localPath, payload)

	pubMock := &mockPublisher{
		succeedLocation: finalization.AssetLocation{
			Provider: "drive",
			FileID:   "drive-file-id-002",
		},
	}
	pubMock.transientSequence = []error{
		&transientErr{msg: "transient 401 unauthorized"},
		nil,
	}

	prepMock := &mockPreparer{}
	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, err := completion.NewService(pubMock, prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got, err := svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})
	if err != nil {
		t.Fatalf("401-then-success: got %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(output): got %d, want 1", len(got))
	}
	if got[0].Location.FileID != "drive-file-id-002" {
		t.Errorf("Location.FileID: got %q, want %q", got[0].Location.FileID, "drive-file-id-002")
	}

	// Cumulative call count = 2 (1 transient + 1 success post-internal-retry).
	if pubMock.calls.Load() != 2 {
		t.Errorf("Publisher cumulative calls: got %d, want 2", pubMock.calls.Load())
	}
}

// Test 3: Duplicate → idempotent short-circuit. Bookkeeper reports the
// triple is already-published; Service MUST NOT call Prepare/Publish; the
// cached envelope is returned via the output slice position; ErrAlreadyPublished
// surfaces via errors.Is (godlike/07 no-duplicate-side-effects).
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

	pubMock := &mockPublisher{}
	prepMock := &mockPreparer{}

	svc, err := completion.NewService(pubMock, prepMock, bkMock)
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

	if prepMock.calls.Load() != 0 {
		t.Errorf("Preparer calls on short-circuit: got %d, want 0", prepMock.calls.Load())
	}
	if pubMock.calls.Load() != 0 {
		t.Errorf("Publisher calls on short-circuit: got %d, want 0", pubMock.calls.Load())
	}
}

// Test 4: Final-checksum mismatch raises ErrFinalChecksumMismatch.
// Publisher returns success, BUT the on-disk file has been mutated after
// Publish (simulates Drive-write corruption). Service's post-publish
// fail-closed invariant MUST surface ErrFinalChecksumMismatch.
func TestPublish_FinalChecksumMismatch_ErrFinalChecksumMismatch(t *testing.T) {
	payload := []byte("original payload for final-checksum test")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-003:cover_image", localPath, payload)

	mutatedPayload := append([]byte(nil), payload...)
	mutatedPayload = append(mutatedPayload, []byte("EXTRA_MUTATED_TAIL")...)
	if err := os.WriteFile(localPath, mutatedPayload, 0o644); err != nil {
		t.Fatalf("mutate file: %v", err)
	}

	pubMock := &mockPublisher{
		succeedLocation: finalization.AssetLocation{
			Provider: "drive",
			FileID:   "drive-file-id-final-mismatch",
		},
	}
	prepMock := &mockPreparer{}
	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, err := completion.NewService(pubMock, prepMock, bkMock)
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

	// Prepare + Publish succeeded; failure is post-publish recompute.
	if prepMock.calls.Load() != 1 {
		t.Errorf("Preparer calls: got %d, want 1", prepMock.calls.Load())
	}
	if pubMock.calls.Load() != 1 {
		t.Errorf("Publisher calls: got %d, want 1", pubMock.calls.Load())
	}

	// Bookkeeper MUST NOT contain drift triple.
	if _, ok := bkMock.records[keyTriplet("job-003", "cover_image", va.SHA256)]; ok {
		t.Errorf("Bookkeeper recorded drift triple; want recording skipped on final-checksum mismatch")
	}
}

// Test 5: Empty-slice input → ErrPublishEmptySlice. Defensive pre-loop
// input gate fires BEFORE any per-artifact work.
func TestPublish_EmptySlice_ErrPublishEmptySlice(t *testing.T) {
	pubMock := &mockPublisher{}
	prepMock := &mockPreparer{}
	bkMock := &mockBookkeeper{}

	svc, err := completion.NewService(pubMock, prepMock, bkMock)
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
// *mockPublisher/*mockPreparer/*mockBookkeeper, the same `nil` literal
// would wrap a typed-nil POINTER in the interface (interface IS NOT nil),
// and the ctor's nil-check would silently pass through — a godlike/07
// wire-up-bug-detection regression.
func TestNewService_NilPorts_Errors(t *testing.T) {
	cases := []struct {
		name string
		p    completion.Publisher
		pr   completion.Preparer
		b    completion.IdempotencyBookkeeper
	}{
		{"nil publisher", nil, &mockPreparer{}, &mockBookkeeper{}},
		{"nil preparer", &mockPublisher{}, nil, &mockBookkeeper{}},
		{"nil bookkeeper", &mockPublisher{}, &mockPreparer{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := completion.NewService(tc.p, tc.pr, tc.b)
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

// Reserve: time anchor import kept for forward-compat P3 instrumentation.
var _ = sha256.New
