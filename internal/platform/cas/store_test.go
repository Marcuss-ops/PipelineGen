package cas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
)

// newTestStore builds a CAS store on a temp root and reuses the REAL
// LocalStore (internal/infrastructure/artifacts) as the staging write path,
// exactly as production composition would wire it.
func newTestStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "cas")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.Chmod(workspace, 0o700); err != nil {
		t.Fatalf("chmod workspace: %v", err)
	}
	stager, err := artifacts.NewLocalStore(artifacts.Config{Workspace: workspace})
	if err != nil {
		t.Fatalf("build LocalStore stager: %v", err)
	}
	store, err := NewStore(Config{Root: root, Stager: stager})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, root, workspace
}

// assertWorkspaceClean fails if the stager workspace still holds staged
// files (Put must clean up the workspace artifact on every path).
func assertWorkspaceClean(t *testing.T, workspace string) {
	t.Helper()
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".partial" {
			t.Fatalf("workspace not clean after Put: leftover %q", e.Name())
		}
	}
}

func digest(t *testing.T, data []byte) string {
	t.Helper()
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestNewStoreValidation(t *testing.T) {
	if _, err := NewStore(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty Root: want ErrInvalidConfig, got %v", err)
	}
	// The workspace must be a not-yet-existing path: the LocalStore creates
	// it with 0700 and rejects pre-existing dirs with looser modes.
	stager, err := artifacts.NewLocalStore(artifacts.Config{Workspace: filepath.Join(t.TempDir(), "ws")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(Config{Root: t.TempDir()}); !errors.Is(err, ErrStagerRequired) {
		t.Fatalf("nil Stager: want ErrStagerRequired, got %v", err)
	}
	_ = stager
}

func TestPutRoundTrip(t *testing.T) {
	store, _, workspace := newTestStore(t)
	ctx := context.Background()
	payload := []byte("hello content addressed storage")

	obj, err := store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	want := digest(t, payload)
	if obj.SHA256 != want || !obj.Exists || obj.Dedup {
		t.Fatalf("Put object = %+v, want sha256=%s exists dedup=false", obj, want)
	}
	if obj.SizeBytes != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", obj.SizeBytes, len(payload))
	}

	rc, err := store.Open(ctx, want)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, payload)
	}
	assertWorkspaceClean(t, workspace)
}

func TestShardLayout(t *testing.T) {
	store, root, _ := newTestStore(t)
	ctx := context.Background()
	payload := []byte("shard-layout-check")

	obj, err := store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	// <root>/<ab>/<cd>/<sha256> with ab = first 2 hex, cd = next 2.
	want := filepath.Join(root, obj.SHA256[0:2], obj.SHA256[2:4], obj.SHA256)
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("object not at sharded address %s: %v", want, err)
	}
	if info.Size() != int64(len(payload)) {
		t.Fatalf("on-disk size = %d, want %d", info.Size(), len(payload))
	}
	// Address resolves via LocalPath too.
	resolved, err := store.LocalPath(obj.SHA256)
	if err != nil || resolved != want {
		t.Fatalf("LocalPath = %q (%v), want %q", resolved, err, want)
	}
}

func TestPutDedup(t *testing.T) {
	store, _, workspace := newTestStore(t)
	ctx := context.Background()
	payload := []byte("same bytes twice")

	first, err := store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Exists || !second.Dedup {
		t.Fatalf("second Put = %+v, want exists + dedup", second)
	}
	if second.SHA256 != first.SHA256 {
		t.Fatalf("addresses differ: %s vs %s", second.SHA256, first.SHA256)
	}
	// Exactly one object on disk (global byte deduplication).
	scan, err := store.IntegrityScan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Total != 1 {
		t.Fatalf("objects on disk after dedup = %d, want 1", scan.Total)
	}
	assertWorkspaceClean(t, workspace)
}

func TestPutCorruptionOnExistingAddress(t *testing.T) {
	store, root, _ := newTestStore(t)
	ctx := context.Background()
	payload := []byte("AAAA")
	want := digest(t, payload)

	// Plant DIFFERENT bytes at the canonical address (simulates a tampered
	// or corrupted store).
	dir := filepath.Join(root, want[0:2], want[2:4])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, want), []byte("BBBB"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Put(ctx, bytes.NewReader(payload)); !errors.Is(err, ErrCorruption) {
		t.Fatalf("Put over corrupt address: want ErrCorruption, got %v", err)
	}
}

func TestVerifyVerifiedAndCorrupt(t *testing.T) {
	store, root, _ := newTestStore(t)
	ctx := context.Background()
	payload := []byte("verify me")

	obj, err := store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Verify(ctx, obj.SHA256)
	if err != nil || !res.Exists || !res.Verified {
		t.Fatalf("Verify on intact object: %+v err=%v", res, err)
	}

	// Corrupt the object on disk.
	target := filepath.Join(root, obj.SHA256[0:2], obj.SHA256[2:4], obj.SHA256)
	if err := os.WriteFile(target, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = store.Verify(ctx, obj.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Exists || res.Verified {
		t.Fatalf("Verify on corrupted object: %+v, want exists + not verified", res)
	}
}

func TestOpenMissingObject(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Open(ctx, strings.Repeat("a", 64)); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("Open missing: want ErrObjectNotFound, got %v", err)
	}
	obj, err := store.Stat(ctx, strings.Repeat("a", 64))
	if err != nil || obj.Exists {
		t.Fatalf("Stat missing: %+v err=%v, want exists=false", obj, err)
	}
	if ok, err := store.Exists(ctx, strings.Repeat("a", 64)); err != nil || ok {
		t.Fatalf("Exists missing: ok=%v err=%v", ok, err)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	payload := []byte("to be deleted")

	obj, err := store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, obj.SHA256); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, obj.SHA256); err != nil {
		t.Fatalf("second Delete must be a no-op: %v", err)
	}
	if ok, _ := store.Exists(ctx, obj.SHA256); ok {
		t.Fatal("object still exists after Delete")
	}
}

func TestIntegrityScan(t *testing.T) {
	store, root, _ := newTestStore(t)
	ctx := context.Background()

	a, err := store.Put(ctx, bytes.NewReader([]byte("object-a")))
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Put(ctx, bytes.NewReader([]byte("object-b")))
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt b on disk.
	target := filepath.Join(root, b.SHA256[0:2], b.SHA256[2:4], b.SHA256)
	if err := os.WriteFile(target, []byte("XX"), 0o600); err != nil {
		t.Fatal(err)
	}

	scan, err := store.IntegrityScan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Total != 2 || scan.Verified != 1 || scan.Corrupt != 1 || scan.Unreadable != 0 {
		t.Fatalf("scan = total=%d verified=%d corrupt=%d unreadable=%d, want 2/1/1/0",
			scan.Total, scan.Verified, scan.Corrupt, scan.Unreadable)
	}
	// a must be the verified entry.
	var foundA, foundB bool
	for _, e := range scan.Entries {
		if e.SHA256 == a.SHA256 && e.Verified {
			foundA = true
		}
		if e.SHA256 == b.SHA256 && !e.Verified && e.Error == "" {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Fatalf("scan entries missing expected classifications: %+v", scan.Entries)
	}
}

func TestInvalidSHA256Addresses(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	for _, bad := range []string{"", "abc", strings.Repeat("a", 63), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		if _, err := store.Open(ctx, bad); !errors.Is(err, ErrInvalidSHA256) {
			t.Fatalf("Open(%q): want ErrInvalidSHA256, got %v", bad, err)
		}
		if _, err := store.Verify(ctx, bad); !errors.Is(err, ErrInvalidSHA256) {
			t.Fatalf("Verify(%q): want ErrInvalidSHA256, got %v", bad, err)
		}
		if _, err := store.Stat(ctx, bad); !errors.Is(err, ErrInvalidSHA256) {
			t.Fatalf("Stat(%q): want ErrInvalidSHA256, got %v", bad, err)
		}
	}
}

func TestPutEmptyContent(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Put(ctx, strings.NewReader("")); err == nil {
		t.Fatal("Put of empty content must fail")
	}
	if _, err := store.Put(ctx, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Put(nil reader): want ErrInvalidInput, got %v", err)
	}
}

func TestNotWired(t *testing.T) {
	var store *Store
	ctx := context.Background()
	if _, err := store.Put(ctx, strings.NewReader("x")); !errors.Is(err, ErrNotWired) {
		t.Fatalf("nil store Put: want ErrNotWired, got %v", err)
	}
	if _, err := store.Open(ctx, strings.Repeat("a", 64)); !errors.Is(err, ErrNotWired) {
		t.Fatalf("nil store Open: want ErrNotWired, got %v", err)
	}
}

func TestConcurrentPutDedup(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	payload := []byte("concurrent dedup burst")

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	dedupHits := 0
	var mu sync.Mutex
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			obj, err := store.Put(ctx, bytes.NewReader(payload))
			if err != nil {
				errs <- err
				return
			}
			if obj.Dedup {
				mu.Lock()
				dedupHits++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Put failed: %v", err)
	}

	scan, err := store.IntegrityScan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Total != 1 || scan.Corrupt != 0 {
		t.Fatalf("after concurrent puts: total=%d corrupt=%d, want 1/0", scan.Total, scan.Corrupt)
	}
	if dedupHits == 0 {
		t.Fatal("expected at least one dedup hit among concurrent writers")
	}
}

// TestIntegrityScanMisplacedObject verifies the scan classifies an object
// whose bytes match its name but that sits in the wrong shard directory as
// Misplaced (unreachable via Open, which resolves by address).
func TestIntegrityScanMisplacedObject(t *testing.T) {
	store, root, _ := newTestStore(t)
	ctx := context.Background()
	payload := []byte("misplaced object")

	obj, err := store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	// Move the object to a wrong shard dir while keeping the 64-hex name.
	source := filepath.Join(root, obj.SHA256[0:2], obj.SHA256[2:4], obj.SHA256)
	wrongDir := filepath.Join(root, "ff", "ff")
	if err := os.MkdirAll(wrongDir, 0o700); err != nil {
		t.Fatal(err)
	}
	wrongPath := filepath.Join(wrongDir, obj.SHA256)
	if err := os.Rename(source, wrongPath); err != nil {
		t.Fatal(err)
	}

	scan, err := store.IntegrityScan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Total != 1 || scan.Verified != 0 || scan.Misplaced != 1 || scan.Corrupt != 0 {
		t.Fatalf("scan = total=%d verified=%d misplaced=%d corrupt=%d, want 1/0/1/0",
			scan.Total, scan.Verified, scan.Misplaced, scan.Corrupt)
	}
}
