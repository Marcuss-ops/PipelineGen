package digest

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingVerifierHasher counts real full-file reads so the memo contract is
// provable: the whole point of Verifier is that N verifications of one
// unchanged file perform ONE read.
type countingVerifierHasher struct{ calls int }

func (c *countingVerifierHasher) hash(path string) (string, int64, error) {
	c.calls++
	return SHA256File(path)
}

func TestVerifier_MemoizesUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mp4")
	content := []byte("content-addressed bytes")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	counted := &countingVerifierHasher{}
	verifier := NewVerifier(counted.hash)

	firstSHA, firstSize, err := verifier.Verify(path)
	if err != nil {
		t.Fatalf("first verify: %v", err)
	}
	secondSHA, secondSize, err := verifier.Verify(path)
	if err != nil {
		t.Fatalf("second verify: %v", err)
	}
	if counted.calls != 1 {
		t.Fatalf("full-file hashes = %d, want 1 (the second verify must be served from the memo)", counted.calls)
	}
	if firstSHA != secondSHA || firstSize != secondSize || firstSize != int64(len(content)) {
		t.Fatalf("memo mismatch: first=(%s,%d) second=(%s,%d)", firstSHA, firstSize, secondSHA, secondSize)
	}
	if want := SHA256Bytes(content); firstSHA != want {
		t.Fatalf("sha = %s, want %s", firstSHA, want)
	}
}

type gatedVerifierHasher struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *gatedVerifierHasher) hash(path string) (string, int64, error) {
	h.calls.Add(1)
	h.once.Do(func() { close(h.started) })
	<-h.release
	return SHA256File(path)
}

func TestVerifier_CoalescesConcurrentCacheMisses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(path, []byte("concurrent content-addressed bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	hasher := &gatedVerifierHasher{started: make(chan struct{}), release: make(chan struct{})}
	verifier := NewVerifier(hasher.hash)

	const callers = 16
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := verifier.Verify(path)
			errs <- err
		}()
	}
	close(start)
	<-hasher.started
	close(hasher.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent verify: %v", err)
		}
	}
	if got := hasher.calls.Load(); got != 1 {
		t.Fatalf("full-file hashes = %d, want 1 under concurrent cache miss", got)
	}
}

func TestVerifier_InvalidatesOnContentChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(path, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	counted := &countingVerifierHasher{}
	verifier := NewVerifier(counted.hash)
	if _, _, err := verifier.Verify(path); err != nil {
		t.Fatal(err)
	}
	replacement := []byte("a much longer replacement payload")
	if err := os.WriteFile(path, replacement, 0o644); err != nil {
		t.Fatal(err)
	}
	gotSHA, gotSize, err := verifier.Verify(path)
	if err != nil {
		t.Fatalf("verify after rewrite: %v", err)
	}
	if counted.calls != 2 {
		t.Fatalf("full-file hashes = %d, want 2 (a changed file must force re-verification)", counted.calls)
	}
	if want := SHA256Bytes(replacement); gotSHA != want {
		t.Fatalf("sha = %s, want %s", gotSHA, want)
	}
	if gotSize != int64(len(replacement)) {
		t.Fatalf("size = %d, want %d", gotSize, len(replacement))
	}
}

// TestVerifier_InvalidatesOnModTimeChange pins the invalidation key: the cache is
// valid only while BOTH size and modification time match, so a same-size
// replacement still re-reads.
func TestVerifier_InvalidatesOnModTimeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mp4")
	content := []byte("same size payload A")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	counted := &countingVerifierHasher{}
	verifier := NewVerifier(counted.hash)
	if _, _, err := verifier.Verify(path); err != nil {
		t.Fatal(err)
	}
	// Same length, different bytes, deliberately back-dated so ONLY the
	// modification time differs from the memoized entry.
	replacement := []byte("same size payload B")
	if err := os.WriteFile(path, replacement, 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	gotSHA, _, err := verifier.Verify(path)
	if err != nil {
		t.Fatalf("verify after mtime change: %v", err)
	}
	if counted.calls != 2 {
		t.Fatalf("full-file hashes = %d, want 2 (a changed mtime must force re-verification)", counted.calls)
	}
	if want := SHA256Bytes(replacement); gotSHA != want {
		t.Fatalf("sha = %s, want %s", gotSHA, want)
	}
}

func TestVerifier_FailsClosedOnMissingFileAndDirectory(t *testing.T) {
	verifier := NewVerifier(nil)
	if _, _, err := verifier.Verify(filepath.Join(t.TempDir(), "absent.mp4")); err == nil {
		t.Fatal("verify of a missing file must fail, not return an empty digest")
	}
	if _, _, err := verifier.Verify(t.TempDir()); err == nil {
		t.Fatal("verify of a directory must fail")
	}
	if _, _, err := verifier.Verify(""); err == nil {
		t.Fatal("verify of an empty path must fail")
	}
}

// TestNilVerifierStillHashes pins that the type is safe as an optional
// dependency: a nil *Verifier must still produce the canonical digest instead of
// panicking or reporting a false cache hit.
func TestNilVerifierStillHashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mp4")
	content := []byte("nil verifier bytes")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	var verifier *Verifier
	sha, size, err := verifier.Verify(path)
	if err != nil {
		t.Fatalf("nil verifier verify: %v", err)
	}
	if sha != SHA256Bytes(content) || size != int64(len(content)) {
		t.Fatalf("nil verifier returned (%s,%d), want (%s,%d)", sha, size, SHA256Bytes(content), len(content))
	}
}
