package cliprender

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// countingHasher wraps the canonical SHA-256 implementation and counts how
// many times a full read actually happened, so the memo contract is provable.
type countingHasher struct {
	calls int
}

func (c *countingHasher) hash(path string) (string, int64, error) {
	c.calls++
	return digest.SHA256File(path)
}

func TestContentVerifier_MemoizesUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mp4")
	content := []byte("content-addressed bytes")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	counted := &countingHasher{}
	verifier := NewContentVerifier(counted.hash)

	firstSHA, firstSize, err := verifier.Verify(path)
	if err != nil {
		t.Fatalf("first verify: %v", err)
	}
	secondSHA, secondSize, err := verifier.Verify(path)
	if err != nil {
		t.Fatalf("second verify: %v", err)
	}
	if counted.calls != 1 {
		t.Fatalf("full-file hashes = %d, want 1 (second verify must be served from the memo)", counted.calls)
	}
	if firstSHA != secondSHA || firstSize != secondSize || firstSize != int64(len(content)) {
		t.Fatalf("memo mismatch: first=(%s,%d) second=(%s,%d)", firstSHA, firstSize, secondSHA, secondSize)
	}
	if want := digest.SHA256Bytes(content); firstSHA != want {
		t.Fatalf("sha = %s, want %s", firstSHA, want)
	}
}

func TestContentVerifier_InvalidatesOnSizeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(path, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	counted := &countingHasher{}
	verifier := NewContentVerifier(counted.hash)
	if _, _, err := verifier.Verify(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("much longer replacement bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotSHA, _, err := verifier.Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	if counted.calls != 2 {
		t.Fatalf("full-file hashes = %d, want 2 (a changed size must force re-verification)", counted.calls)
	}
	if want := digest.SHA256Bytes([]byte("much longer replacement bytes")); gotSHA != want {
		t.Fatalf("sha = %s, want %s", gotSHA, want)
	}
}

func TestContentVerifier_InvalidatesOnModTimeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(path, []byte("same bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	counted := &countingHasher{}
	verifier := NewContentVerifier(counted.hash)
	if _, _, err := verifier.Verify(path); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if _, _, err := verifier.Verify(path); err != nil {
		t.Fatal(err)
	}
	if counted.calls != 2 {
		t.Fatalf("full-file hashes = %d, want 2 (a changed modtime must invalidate the memo)", counted.calls)
	}
}

func TestContentVerifier_FailsClosedOnMissingOrDirectory(t *testing.T) {
	verifier := NewContentVerifier(nil)
	if _, _, err := verifier.Verify(filepath.Join(t.TempDir(), "nope.mp4")); err == nil {
		t.Fatal("missing file must fail closed")
	}
	if _, _, err := verifier.Verify(t.TempDir()); err == nil {
		t.Fatal("directory must fail closed")
	}
	if _, _, err := verifier.Verify(""); err == nil {
		t.Fatal("empty path must fail closed")
	}
}

func TestContentVerifier_NilReceiverStillHashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mp4")
	content := []byte("nil verifier bytes")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	var verifier *ContentVerifier
	sha, size, err := verifier.Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha != digest.SHA256Bytes(content) || size != int64(len(content)) {
		t.Fatalf("nil verifier verify = (%s,%d)", sha, size)
	}
}
