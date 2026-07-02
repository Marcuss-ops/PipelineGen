// Package creator — adapter_test.go (P0 Commit 6, July 2026).
//
// Test surface for the Creator-side ArtifactUploader adapter.
// Internal test package so we can access the private jobBrokerClient
// interface + newWithClient unexported constructor directly (mirrors
// the canonical AGENTS.md Pattern 0 dependency-injection convention
// without polluting the public surface with a test-only constructor).
package creator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
)

// ── Stub jobBrokerClient (small, in-test, satisfies the private interface) ─

type stubBroker struct {
	// Server-side response shapes (configurable per test).
	nextPrepareSession  *remote.UploadSession
	nextPrepareErr      error
	nextUploadSession   *remote.UploadSession
	nextUploadErr       error
	nextFinalizeSession *remote.UploadSession
	nextFinalizeErr     error

	// Call counters + last-args captured for assertions.
	prepareCalls          int
	uploadCalls           int
	finalizeCalls         int
	lastUploadLocalPath   string
	lastUploadIdemKey     string
	lastFinalizeSha256Hex string
	lastFinalizeIdemKey   string
	lastUploadSessionID   string
	lastFinalizeSessionID string
}

func (s *stubBroker) PrepareArtifactUpload(ctx remote.PrepareContext) (*remote.UploadSession, error) {
	s.prepareCalls++
	if s.nextPrepareErr != nil {
		return nil, s.nextPrepareErr
	}
	return s.nextPrepareSession, nil
}

func (s *stubBroker) UploadArtifactFile(ctx remote.PrepareContext, sessionID, localPath, idempotencyKey string) (*remote.UploadSession, error) {
	s.uploadCalls++
	s.lastUploadSessionID = sessionID
	s.lastUploadLocalPath = localPath
	s.lastUploadIdemKey = idempotencyKey
	if s.nextUploadErr != nil {
		return nil, s.nextUploadErr
	}
	return s.nextUploadSession, nil
}

func (s *stubBroker) FinalizeArtifactUpload(ctx remote.PrepareContext, sessionID, sha256Hex, idempotencyKey string) (*remote.UploadSession, error) {
	s.finalizeCalls++
	s.lastFinalizeSessionID = sessionID
	s.lastFinalizeSha256Hex = sha256Hex
	s.lastFinalizeIdemKey = idempotencyKey
	if s.nextFinalizeErr != nil {
		return nil, s.nextFinalizeErr
	}
	return s.nextFinalizeSession, nil
}

// ── Session helpers ────────────────────────────────────────────────────

func mkInitialSession() *remote.UploadSession {
	return &remote.UploadSession{
		ID: "sess-001", LeaseID: "lease-001", ArtifactID: "job-001:script_json",
		State: remote.StateUploadPreparing,
	}
}

func mkUploadedSession() *remote.UploadSession {
	return &remote.UploadSession{
		ID: "sess-001", LeaseID: "lease-001", ArtifactID: "job-001:script_json",
		State: remote.StateUploadUploaded,
	}
}

func mkFinalizedSession() *remote.UploadSession {
	return &remote.UploadSession{
		ID: "sess-001", LeaseID: "lease-001", ArtifactID: "job-001:script_json",
		State: remote.StateUploadFinalized,
	}
}

func mkContext() remote.PrepareContext {
	return remote.PrepareContext{
		JobID:        "job-001",
		LeaseID:      "lease-001",
		ArtifactID:   "job-001:script_json",
		ArtifactKind: "script_json",
		Filename:     "script.json",
		MIMEType:     "application/json",
		SizeBytes:    1234,
		SHA256:       "abc12345",
	}
}

// ── Tests ──────────────────────────────────────────────────────────────

// TestAdapter_Prepare_HappyPath — server returns PREPARING → adapter
// returns the same envelope + idempotency key derived correctly.
func TestAdapter_Prepare_HappyPath(t *testing.T) {
	broker := &stubBroker{
		nextPrepareSession: mkInitialSession(),
	}
	a := newWithClient(broker, zap.NewNop())

	ctx := mkContext()
	session, err := a.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if session == nil {
		t.Fatal("Prepare returned nil session with non-nil error")
	}
	if session.State != remote.StateUploadPreparing {
		t.Errorf("session.State = %q; want PREPARING", session.State)
	}
	if broker.prepareCalls != 1 {
		t.Errorf("prepareCalls = %d; want 1", broker.prepareCalls)
	}
}

// TestAdapter_Prepare_ServerReturnsWrongState — server returns a
// non-PREPARING state → adapter wraps as *IllegalTransitionError.
func TestAdapter_Prepare_ServerReturnsWrongState(t *testing.T) {
	// Server mis-returns UPLOADED on Prepare (protocol violation).
	wrong := &remote.UploadSession{
		ID:         "sess-001",
		LeaseID:    "lease-001",
		ArtifactID: "job-001:script_json",
		State:      remote.StateUploadUploaded,
	}
	broker := &stubBroker{nextPrepareSession: wrong}
	a := newWithClient(broker, zap.NewNop())

	ctx := mkContext()
	_, err := a.Prepare(ctx)
	if err == nil {
		t.Fatal("expected error when server returns non-PREPARING state on Prepare")
	}
	var ite *remote.IllegalTransitionError
	if !errors.As(err, &ite) {
		t.Fatalf("error must wrap *IllegalTransitionError; got %T: %v", err, err)
	}
	if ite.From != remote.StateUploadPreparing || ite.To != remote.StateUploadUploaded {
		t.Errorf("expected (From=PREPARING, To=UPLOADED); got (%s, %s)", ite.From, ite.To)
	}
}

// TestAdapter_Prepare_EmptyJobID — empty JobID yields fail-closed
// error per godlike/07 no-fake-availability.
func TestAdapter_Prepare_EmptyJobID(t *testing.T) {
	broker := &stubBroker{}
	a := newWithClient(broker, zap.NewNop())

	ctx := mkContext()
	ctx.JobID = "" // empty
	_, err := a.Prepare(ctx)
	if err == nil {
		t.Fatal("expected error for empty JobID")
	}
	if !errors.Is(err, remote.ErrArtifactUploaderNotConfigured) {
		t.Errorf("error should match ErrArtifactUploaderNotConfigured; got %v", err)
	}
}

// TestAdapter_Upload_IllegalTransition_FromFinalized — sticky-terminal
// gate rejects Upload on a FINALIZED session.
func TestAdapter_Upload_IllegalTransition_FromFinalized(t *testing.T) {
	broker := &stubBroker{}
	a := newWithClient(broker, zap.NewNop())

	finalized := mkFinalizedSession()
	ctx := mkContext()

	_, err := a.Upload(ctx, *finalized, "/tmp/script.json")
	if err == nil {
		t.Fatal("expected error when uploading from FINALIZED session")
	}
	var ite *remote.IllegalTransitionError
	if !errors.As(err, &ite) {
		t.Fatalf("error should be typed *IllegalTransitionError; got %T", err)
	}
	// Pre-call rejection: broker.uploadCalls must be 0.
	if broker.uploadCalls != 0 {
		t.Errorf("broker.uploadCalls = %d; want 0 (pre-call rejection)", broker.uploadCalls)
	}
}

// TestAdapter_Upload_IdempotencyKeyDerived — adapter derives the key
// from (JobID, ArtifactID, SHA256) and threads it through to broker.
func TestAdapter_Upload_IdempotencyKeyDerived(t *testing.T) {
	broker := &stubBroker{nextUploadSession: mkUploadedSession()}
	a := newWithClient(broker, zap.NewNop())

	ctx := mkContext()
	prepSession := mkInitialSession()

	tmpFile := writeTempFile(t, []byte("hello world\n"))
	_, err := a.Upload(ctx, *prepSession, tmpFile)
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	expectedKey := remote.ArtifactIdempotencyKey("job-001", "job-001:script_json", "abc12345")
	if broker.lastUploadIdemKey != expectedKey {
		t.Errorf("lastUploadIdemKey = %q; want %q", broker.lastUploadIdemKey, expectedKey)
	}
	if broker.lastUploadLocalPath != tmpFile {
		t.Errorf("lastUploadLocalPath = %q; want %q", broker.lastUploadLocalPath, tmpFile)
	}
	if broker.lastUploadSessionID != "sess-001" {
		t.Errorf("lastUploadSessionID = %q; want sess-001", broker.lastUploadSessionID)
	}
}

// TestAdapter_Finalize_IdempotencyKeyAndSha256 — adapter threads both
// sha256 + idempotency-key verbatim to broker.
func TestAdapter_Finalize_IdempotencyKeyAndSha256(t *testing.T) {
	broker := &stubBroker{nextFinalizeSession: mkFinalizedSession()}
	a := newWithClient(broker, zap.NewNop())

	ctx := mkContext()
	uploaded := mkUploadedSession()

	_, err := a.Finalize(ctx, *uploaded)
	if err != nil {
		t.Fatalf("Finalize returned error: %v", err)
	}
	if broker.lastFinalizeSha256Hex != "abc12345" {
		t.Errorf("lastFinalizeSha256Hex = %q; want abc12345", broker.lastFinalizeSha256Hex)
	}
	expectedKey := remote.ArtifactIdempotencyKey("job-001", "job-001:script_json", "abc12345")
	if broker.lastFinalizeIdemKey != expectedKey {
		t.Errorf("lastFinalizeIdemKey = %q; want %q", broker.lastFinalizeIdemKey, expectedKey)
	}
	if broker.lastFinalizeSessionID != "sess-001" {
		t.Errorf("lastFinalizeSessionID = %q; want sess-001", broker.lastFinalizeSessionID)
	}
}

// TestAdapter_FullHappyPath_ThreeCalls — full 3-phase progression
// (Prepare → Upload → Finalize) with stub returning canonical states.
func TestAdapter_FullHappyPath_ThreeCalls(t *testing.T) {
	broker := &stubBroker{
		nextPrepareSession:  mkInitialSession(),
		nextUploadSession:   mkUploadedSession(),
		nextFinalizeSession: mkFinalizedSession(),
	}
	a := newWithClient(broker, zap.NewNop())

	ctx := mkContext()

	// Phase 1: Prepare
	session, err := a.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if session.State != remote.StateUploadPreparing {
		t.Errorf("post-Prepare state = %q; want PREPARING", session.State)
	}

	// Phase 2: Upload (write a temp file for the localPath arg)
	tmpFile := writeTempFile(t, []byte("full-happy-path\n"))
	session, err = a.Upload(ctx, *session, tmpFile)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if session.State != remote.StateUploadUploaded {
		t.Errorf("post-Upload state = %q; want UPLOADED", session.State)
	}

	// Phase 3: Finalize
	session, err = a.Finalize(ctx, *session)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if session.State != remote.StateUploadFinalized {
		t.Errorf("post-Finalize state = %q; want FINALIZED", session.State)
	}

	// Each phase called broker exactly once.
	if broker.prepareCalls != 1 {
		t.Errorf("prepareCalls = %d; want 1", broker.prepareCalls)
	}
	if broker.uploadCalls != 1 {
		t.Errorf("uploadCalls = %d; want 1", broker.uploadCalls)
	}
	if broker.finalizeCalls != 1 {
		t.Errorf("finalizeCalls = %d; want 1", broker.finalizeCalls)
	}
}

// TestAdapter_NilReceiver_ReturnsNotConfigured — nil-receiver methods
// return ErrArtifactUploaderNotConfigured per godlike/07 no-fake-
// availability (graceful failure on a partially-wired adapter).
func TestAdapter_NilReceiver_ReturnsNotConfigured(t *testing.T) {
	var a *Adapter // nil
	ctx := mkContext()
	prepSess := mkInitialSession()

	if _, err := a.Prepare(ctx); err == nil || !errors.Is(err, remote.ErrArtifactUploaderNotConfigured) {
		t.Errorf("nil.Prepare: expected ErrArtifactUploaderNotConfigured; got %v", err)
	}
	if _, err := a.Upload(ctx, *prepSess, "/tmp/x"); err == nil || !errors.Is(err, remote.ErrArtifactUploaderNotConfigured) {
		t.Errorf("nil.Upload: expected ErrArtifactUploaderNotConfigured; got %v", err)
	}
	if _, err := a.Finalize(ctx, *prepSess); err == nil || !errors.Is(err, remote.ErrArtifactUploaderNotConfigured) {
		t.Errorf("nil.Finalize: expected ErrArtifactUploaderNotConfigured; got %v", err)
	}
}

// TestAdapter_Prepare_EmptyArtifactID_IdemKeyEmpty — when the
// canonical derivation returns empty (godlike/07 empty-marker case),
// Prepare fails closed with ErrArtifactIdempotencyKeyConflict.
func TestAdapter_Prepare_EmptyArtifactID_IdemKeyEmpty(t *testing.T) {
	broker := &stubBroker{nextPrepareSession: mkInitialSession()}
	a := newWithClient(broker, zap.NewNop())

	ctx := mkContext()
	ctx.ArtifactID = "" // empty => key derivation returns empty marker

	_, err := a.Prepare(ctx)
	if err == nil {
		t.Fatal("expected error when ArtifactID is empty")
	}
	if !errors.Is(err, remote.ErrArtifactIdempotencyKeyConflict) {
		t.Errorf("expected ErrArtifactIdempotencyKeyConflict; got %v", err)
	}
}

// writeTempFile writes content to a fresh temp file under t.TempDir()
// and returns the path. Uses t.TempDir() for cleanup-on-test-end.
func writeTempFile(t *testing.T, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.bin")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// fmt import is preserved for potential future error-formatting tests.
var _ = fmt.Sprintf
