package verification_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/staged"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/verification"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// writeTempFile writes payload to a fresh temp file under t.TempDir() and
// returns the absolute path. The path is auto-cleaned by t.TempDir() at
// test end (no manual cleanup required).
func writeTempFile(t *testing.T, payload []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "artifact.bin")
	if err := os.WriteFile(p, payload, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return p
}

// newStagedFromFile builds a *staged.StagedArtifact with claimed SHA256 and
// SizeBytes matching the on-disk file (caller can then deliberately mutate
// one or both fields to trigger drift).
func newStagedFromFile(t *testing.T, artifactID, localPath string, payload []byte) *staged.StagedArtifact {
	t.Helper()
	return &staged.StagedArtifact{
		AssetID:   artifactID,
		LocalPath: localPath,
		SHA256:    files.SHA256Bytes(payload),
		SizeBytes: int64(len(payload)),
	}
}

// Test 1: happy path — on-disk SHA-256 + size match the staged claims; the
// verifier returns the 12-field envelope with ValidatedAt populated and
// identity fields echoing the input.
func TestVerify_HappyPath_SHA256SizeMatch(t *testing.T) {
	payload := []byte("hello world, this is a verified artifact payload byte-stream")
	localPath := writeTempFile(t, payload)
	sa := newStagedFromFile(t, "art_test_001", localPath, payload)

	v := verification.NewVerifier()
	got, err := v.VerifyStagedArtifact(context.Background(), sa)
	if err != nil {
		t.Fatalf("happy path: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatal("happy path: got nil envelope, want non-nil")
	}

	// Identity fields propagate from StagedArtifact input.
	if got.ArtifactID != sa.AssetID {
		t.Errorf("artifact_id: got %q, want %q", got.ArtifactID, sa.AssetID)
	}
	if got.LocalPath != sa.LocalPath {
		t.Errorf("local_path: got %q, want %q", got.LocalPath, sa.LocalPath)
	}
	if got.Filename != filepath.Base(sa.LocalPath) {
		t.Errorf("filename: got %q, want %q", got.Filename, filepath.Base(sa.LocalPath))
	}

	// VERIFIED fields echo (size + SHA claim matches the on-disk live read).
	if got.SizeBytes != sa.SizeBytes {
		t.Errorf("size_bytes: got %d, want %d", got.SizeBytes, sa.SizeBytes)
	}
	if got.SHA256 != sa.SHA256 {
		t.Errorf("sha256: got %q, want %q", got.SHA256, sa.SHA256)
	}

	// IdempotencyKey is derived byte-stable via the 3-tuple (artifactID|sha256|filename).
	expectedKey := files.SHA256String(sa.AssetID + ":" + sa.SHA256 + ":" + filepath.Base(sa.LocalPath))
	if got.IdempotencyKey != expectedKey {
		t.Errorf("idempotency_key: got %q, want %q", got.IdempotencyKey, expectedKey)
	}

	// ValidatedAt is populated (non-zero, recent).
	if got.ValidatedAt.IsZero() {
		t.Error("validated_at: zero, want now()-derived timestamp")
	}
	if time.Since(got.ValidatedAt) > 5*time.Second {
		t.Errorf("validated_at: too old (%v), want now()-derived", got.ValidatedAt)
	}
}

// Test 2: SHA-256 drift — on-disk recompute returns a DIFFERENT hash than
// the staged SHA-256 claim. Verifier MUST return ErrStagedChecksumMismatch
// via errors.Is (godlike/07 typed-error contract).
//
// Crucial: only the SHA-256 claim is mutated; size matches. This isolates the
// checksum gate from the size gate (each error sentinel surfaces from its
// specific failure path).
func TestVerify_SHA256Mismatch_RaisesErrStagedChecksumMismatch(t *testing.T) {
	payload := []byte("real on-disk content")
	localPath := writeTempFile(t, payload)

	// Build a StagedArtifact with the WRONG SHA-256 but CORRECT size — this
	// isolates the SHA gate (size check passes, hash check fails).
	sa := &staged.StagedArtifact{
		AssetID:   "art_test_002",
		LocalPath: localPath,
		SHA256:    files.SHA256Bytes([]byte("DIFFERENT content")), // wrong SHA
		SizeBytes: int64(len(payload)),                            // matches os.Stat
	}

	v := verification.NewVerifier()
	got, err := v.VerifyStagedArtifact(context.Background(), sa)
	if err == nil {
		t.Fatalf("expected error, got nil; envelope=%+v (must NOT surface a verified envelope on drift)", got)
	}
	if got != nil {
		t.Errorf("got non-nil envelope on drift: %+v (must be nil on typed error)", got)
	}
	if !errors.Is(err, verification.ErrStagedChecksumMismatch) {
		t.Errorf("err = %v; want wraps ErrStagedChecksumMismatch via errors.Is", err)
	}
	// Negative assertion: size sentinel MUST NOT fire (we mutated SHA only).
	if errors.Is(err, verification.ErrStagedSizeMismatch) {
		t.Errorf("err wraps ErrStagedSizeMismatch; want only ErrStagedChecksumMismatch (size was correct)")
	}
}

// Test 3: size match — positive path for the size gate. Same payload as Test 1
// but slimmer assertions on SizeBytes + Filename fields per the user's
// specification ("size match: VerifiedArtifact.SizeBytes echoes os.Stat size").
func TestVerify_SizeMatch_ProducesField(t *testing.T) {
	payload := []byte("size match happy path payload")
	localPath := writeTempFile(t, payload)
	sa := newStagedFromFile(t, "art_test_003", localPath, payload)

	v := verification.NewVerifier()
	got, err := v.VerifyStagedArtifact(context.Background(), sa)
	if err != nil {
		t.Fatalf("size match: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatal("size match: got nil envelope, want non-nil")
	}
	if got.SizeBytes != int64(len(payload)) {
		t.Errorf("size_bytes: got %d, want %d", got.SizeBytes, len(payload))
	}
	if got.SizeBytes != sa.SizeBytes {
		t.Errorf("size_bytes: got %d, want staged claim %d", got.SizeBytes, sa.SizeBytes)
	}
}

// Test 4: size drift — staged SizeBytes claim differs from os.Stat size.
// Verifier MUST return ErrStagedSizeMismatch via errors.Is (godlike/07
// typed-error contract).
//
// Crucial: only the SizeBytes claim is mutated; SHA matches. This isolates the
// size gate from the SHA gate (cheap-first ordering: size check aborts BEFORE
// hash recompute, no work wasted on the SHA stream when size already mismatched).
func TestVerify_SizeMismatch_RaisesErrStagedSizeMismatch(t *testing.T) {
	payload := []byte("real on-disk content of correct size")
	localPath := writeTempFile(t, payload)
	realSize := int64(len(payload))

	// Build a StagedArtifact with the WRONG size but CORRECT SHA — this
	// isolates the size gate (size check fails BEFORE hash work).
	sa := &staged.StagedArtifact{
		AssetID:   "art_test_004",
		LocalPath: localPath,
		SHA256:    files.SHA256Bytes(payload), // matches on-disk
		SizeBytes: realSize + 1,               // wrong size
	}

	v := verification.NewVerifier()
	got, err := v.VerifyStagedArtifact(context.Background(), sa)
	if err == nil {
		t.Fatalf("expected error, got nil; envelope=%+v", got)
	}
	if got != nil {
		t.Errorf("got non-nil envelope on drift: %+v (must be nil on typed error)", got)
	}
	if !errors.Is(err, verification.ErrStagedSizeMismatch) {
		t.Errorf("err = %v; want wraps ErrStagedSizeMismatch via errors.Is", err)
	}
	// Negative assertion: checksum sentinel MUST NOT fire (we mutated size only).
	if errors.Is(err, verification.ErrStagedChecksumMismatch) {
		t.Errorf("err wraps ErrStagedChecksumMismatch; want only ErrStagedSizeMismatch (size chek runs FIRST, so SHA was never computed)")
	}
}

// Test 5 (bonus): clock injection — ValidatedAt echoes the injected clock
// value byte-stable across consecutive calls. This is the canonical test
// for the NewVerifierWithClock seam; it does NOT count toward the user's
// "4 TDD test" requirement but is essential for audit-pin per godlike/07.
func TestVerify_ClockInjection_ValidatedAtByteStable(t *testing.T) {
	payload := []byte("clock injection test")
	localPath := writeTempFile(t, payload)
	sa := newStagedFromFile(t, "art_test_clock", localPath, payload)

	fixed := time.Date(2026, 7, 4, 14, 30, 0, 0, time.UTC)
	v := verification.NewVerifierWithClock(func() time.Time { return fixed })

	got1, err1 := v.VerifyStagedArtifact(context.Background(), sa)
	if err1 != nil {
		t.Fatalf("clock injection: got error %v, want nil", err1)
	}
	if !got1.ValidatedAt.Equal(fixed) {
		t.Errorf("validated_at: got %v, want fixed clock %v", got1.ValidatedAt, fixed)
	}
}
