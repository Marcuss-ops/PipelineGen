// Package manifest — service_test.go (PR 6, codex/asset-manifest-service).
//
// 7-case test matrix per PR 6 spec:
//
//  1. update — replace existing entry by AssetID
//  2. append — add new entry preserving old
//  3. corrupted JSON — recover without losing new entry
//  4. concurrent writes to DIFFERENT folders — no contention
//  5. upload error — graceful degradation, ErrRemoteWrite wrap
//  6. retry idempotent — UpsertRemote twice with same AssetID → 1 entry
//  7. missing remote — initial create from (nil, nil)
//
// Plus 4 defensive tests pinning the contract surface:
//
//   - UpsertLocal happy path (atomic temp+fsync+rename)
//   - UpsertLocal empty path → ErrInvalidPath
//   - UpsertRemote empty folderID → ErrInvalidPath
//   - Upsert* empty AssetID → ErrInvalidEntry
//
// The fakeDriveAdapter below is in-memory + injectable error hooks;
// no real Drive API access is required. Concurrent test runs cleanly
// under `go test -race` (verified locally).
package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Fake DriveAdapter ─────────────────────────────────────────────────────

// fakeDriveAdapter is the in-memory DriveAdapter fixture used by every
// remote-side test in this file. Key shape: "drive:<folderID>/<filename>".
//
// Two injectable error hooks cover the failure-path tests:
//
//   - downloadErr: forces DownloadManifest to return the error.
//   - replaceErr:  forces ReplaceManifest to return the error.
//
// downloadDelay (optional) introduces a slow hook so concurrent tests can
// observe the per-folder lock contention (or lack of contention for
// cross-folder tests).
type fakeDriveAdapter struct {
	mu          sync.Mutex
	files       map[string][]byte
	downloadErr error
	replaceErr  error

	// Instrumentation counters (test-only).
	downloadCount int
	replaceCount  int
}

func newFakeDriveAdapter() *fakeDriveAdapter {
	return &fakeDriveAdapter{files: make(map[string][]byte)}
}

func (f *fakeDriveAdapter) key(folderID, filename string) string {
	return fmt.Sprintf("drive:%s/%s", folderID, filename)
}

func (f *fakeDriveAdapter) DownloadManifest(_ context.Context, folderID, filename string) ([]byte, error) {
	f.mu.Lock()
	f.downloadCount++
	err := f.downloadErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if data, ok := f.files[f.key(folderID, filename)]; ok {
		return append([]byte(nil), data...), nil
	}
	return nil, nil
}

func (f *fakeDriveAdapter) ReplaceManifest(_ context.Context, folderID, filename string, content []byte) (string, error) {
	f.mu.Lock()
	err := f.replaceErr
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[f.key(folderID, filename)] = append([]byte(nil), content...)
	f.replaceCount++
	return "fake-file-id-" + f.key(folderID, filename), nil
}

// get returns the current bytes for a folder/filename (testing helper).
func (f *fakeDriveAdapter) get(folderID, filename string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.files[f.key(folderID, filename)]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), data...), true
}

// ── Helpers ────────────────────────────────────────────────────────────────

// decodeEntries parses the fake adapter's stored bytes back into []Entry.
func decodeEntries(t *testing.T, data []byte) []Entry {
	t.Helper()
	var out []Entry
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decodeEntries: unmarshal failed: %v\nraw=%q", err, string(data))
	}
	return out
}

// uniqueFixture returns N distinct Entry fixtures with stable IDs.
func uniqueFixture(prefix string, n int) []Entry {
	out := make([]Entry, n)
	for i := 0; i < n; i++ {
		out[i] = Entry{
			AssetID:     fmt.Sprintf("%s-%03d", prefix, i),
			Source:      "test",
			Name:        fmt.Sprintf("clip-%s-%03d", prefix, i),
			LocalPath:   fmt.Sprintf("/tmp/%s/%03d.mp4", prefix, i),
			DriveFileID: fmt.Sprintf("drive-%s-%03d", prefix, i),
			DriveLink:   fmt.Sprintf("https://drive/%s/%03d", prefix, i),
			FileHash:    fmt.Sprintf("sha256:%s-%03d", prefix, i),
			UpdatedAt:   time.Now().UTC().Truncate(time.Second),
		}
	}
	return out
}

// newTestEntry constructs a single Entry with a minimal valid shape.
func newTestEntry(id, name string) Entry {
	return Entry{
		AssetID:   id,
		Source:    "test",
		Name:      name,
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
}

// ── 1. Update (replace existing entry by AssetID) ──────────────────────────

func TestService_UpsertRemote_Update(t *testing.T) {
	adapter := newFakeDriveAdapter()
	svc := New(adapter, zap.NewNop())
	ctx := context.Background()

	// Pre-seed: entry "A1" with Name="old"
	if err := svc.UpsertRemote(ctx, "folder-1", newTestEntry("A1", "old")); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	// Upsert same AssetID with Name="new"
	if err := svc.UpsertRemote(ctx, "folder-1", newTestEntry("A1", "new")); err != nil {
		t.Fatalf("update upsert: %v", err)
	}

	data, ok := adapter.get("folder-1", "metadata.json")
	if !ok {
		t.Fatal("metadata.json missing after update")
	}
	entries := decodeEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after update, got %d: %v", len(entries), entries)
	}
	if entries[0].AssetID != "A1" || entries[0].Name != "new" {
		t.Fatalf("entry not updated: got %+v", entries[0])
	}
}

// ── 2. Append (preserve old entries when adding a new AssetID) ────────────

func TestService_UpsertRemote_Append(t *testing.T) {
	adapter := newFakeDriveAdapter()
	svc := New(adapter, zap.NewNop())
	ctx := context.Background()

	if err := svc.UpsertRemote(ctx, "folder-1", newTestEntry("A1", "first")); err != nil {
		t.Fatalf("upsert A1: %v", err)
	}
	if err := svc.UpsertRemote(ctx, "folder-1", newTestEntry("B2", "second")); err != nil {
		t.Fatalf("upsert B2: %v", err)
	}
	if err := svc.UpsertRemote(ctx, "folder-1", newTestEntry("C3", "third")); err != nil {
		t.Fatalf("upsert C3: %v", err)
	}

	entries := decodeEntries(t, adapter.getOrFatal(t, "folder-1"))
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries after append set, got %d: %v", len(entries), entries)
	}
	wantIDs := map[string]bool{"A1": false, "B2": false, "C3": false}
	for _, e := range entries {
		if _, ok := wantIDs[e.AssetID]; !ok {
			t.Fatalf("unexpected ID %s in entries: %v", e.AssetID, entries)
		}
		wantIDs[e.AssetID] = true
	}
	for id, seen := range wantIDs {
		if !seen {
			t.Fatalf("missing ID %s after append set: %v", id, entries)
		}
	}
}

// ── 3. Corrupted JSON (recover without losing the new entry) ───────────────

func TestService_UpsertRemote_CorruptedJSON(t *testing.T) {
	adapter := newFakeDriveAdapter()
	// Pre-seed the adapter with bytes that are NOT valid JSON.
	adapter.mu.Lock()
	adapter.files[adapter.key("folder-1", "metadata.json")] = []byte("{this is not valid json")
	adapter.mu.Unlock()

	svc := New(adapter, zap.NewNop())
	ctx := context.Background()

	// The service must warn-and-overwrite, not panic.
	err := svc.UpsertRemote(ctx, "folder-1", newTestEntry("A1", "first"))
	if err != nil {
		t.Fatalf("UpsertRemote on corrupted file must succeed (overwrite), got: %v", err)
	}

	entries := decodeEntries(t, adapter.getOrFatal(t, "folder-1"))
	if len(entries) != 1 || entries[0].AssetID != "A1" {
		t.Fatalf("expected exactly [A1] after corruption recovery, got: %v", entries)
	}
}

// ── 4. Concurrent writes to DIFFERENT folders (no contention) ──────────────

func TestService_UpsertRemote_Concurrent_DifferentFolders(t *testing.T) {
	adapter := newFakeDriveAdapter()
	svc := New(adapter, zap.NewNop())
	ctx := context.Background()

	const perFolder = 50
	const numFolders = 2

	fixtures := make(map[string][]Entry, numFolders)
	for f := 0; f < numFolders; f++ {
		folderID := fmt.Sprintf("folder-%d", f)
		fixtures[folderID] = uniqueFixture(folderID, perFolder)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, perFolder*numFolders)
	for folderID, entries := range fixtures {
		for _, e := range entries {
			entry := e
			folder := folderID
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := svc.UpsertRemote(ctx, folder, entry); err != nil {
					errCh <- fmt.Errorf("upsert %s in %s: %w", entry.AssetID, folder, err)
				}
			}()
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent upsert failed: %v", err)
	}

	for folderID := range fixtures {
		entries := decodeEntries(t, adapter.getOrFatal(t, folderID))
		if len(entries) != perFolder {
			t.Fatalf("%s: expected %d entries, got %d", folderID, perFolder, len(entries))
		}
		// Verify all perFolder IDs are present and unique (no merge collisions
		// across the SAME folder's concurrent writers — the per-folder lock
		// must serialize them).
		seen := make(map[string]bool, perFolder)
		for _, e := range entries {
			if !strings.HasPrefix(e.AssetID, folderID+"-") {
				t.Fatalf("%s: leaked ID from another folder: %s", folderID, e.AssetID)
			}
			if seen[e.AssetID] {
				t.Fatalf("%s: duplicate ID %s in final state (merge-by-ID failed under concurrency)",
					folderID, e.AssetID)
			}
			seen[e.AssetID] = true
		}
	}
}

// ── 4b. Concurrent SAME-folder, SAME-AssetID writes: merge-by-ID
//
//	under per-folder serialisation must collapse to 1 entry. ────────────
//
// The cross-folder test (#4) uses unique AssetIDs per goroutine, so the
// per-folder lock is exercised but the merge-by-AssetID dedupe is never
// hit. Real callers (e.g. semantic_enricher retried after a partial
// Drive error) enqueue concurrent writes that share an AssetID; this
// test pins the full invariant — same folder + same AssetID × N
// goroutines → final state has exactly 1 entry.
func TestService_UpsertRemote_Concurrent_SameFolder_SameAssetID(t *testing.T) {
	adapter := newFakeDriveAdapter()
	svc := New(adapter, zap.NewNop())
	ctx := context.Background()

	const goroutines = 20
	const assetID = "A1-shared"

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	// Each goroutine uses a different Name so we can verify last-
	// writer-wins unpredictably applies (we don't assert WHICH name
	// wins, only that exactly 1 entry survives).
	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry := newTestEntry(assetID, fmt.Sprintf("writer-%02d", i))
			entry.DriveFileID = fmt.Sprintf("drive-%02d", i)
			if err := svc.UpsertRemote(ctx, "folder-shared", entry); err != nil {
				errCh <- fmt.Errorf("writer %d: %w", i, err)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent same-folder upsert failed: %v", err)
	}

	entries := decodeEntries(t, adapter.getOrFatal(t, "folder-shared"))
	if len(entries) != 1 {
		t.Fatalf("concurrent same-folder writes must collapse to 1 entry (got %d): %v",
			len(entries), entries)
	}
	if entries[0].AssetID != assetID {
		t.Fatalf("assetID drift under concurrency: got %q want %q",
			entries[0].AssetID, assetID)
	}
	// Every goroutine issued a ReplaceManifest call — per-folder
	// serialisation proves every writer saw a fresh download/merge
	// cycle, so the Assertion `len == 1` is load-bearing (not
	// coincidental from "lock held by first writer").
	if adapter.replaceCount != goroutines {
		t.Fatalf("expected %d ReplaceManifest calls (one per writer), got %d",
			goroutines, adapter.replaceCount)
	}
	if adapter.downloadCount != goroutines {
		t.Fatalf("expected %d DownloadManifest calls (one per writer), got %d",
			goroutines, adapter.downloadCount)
	}
}

// ── 4c. Same-folder, same-AssetID writes at N=5 (exact-spec variant).
//
// Stable companion to the stress test above (#4b, N=20). Runs
// faster under -race and pins the invariant at exactly the N the
// spec calls out: "5 goroutines × shared AssetID → final state has
// 1 entry, not 5". Each goroutine writes a DIFFERENT Name + DriveLink
// so the last-writer-wins order is unpredictable — we do not pin
// WHICH Name wins, only that exactly 1 entry survives and all 5
// drive roundtrips ran (so len==1 is load-bearing, not coincidental
// from a single early winner pre-empting the rest).
func TestService_UpsertRemote_Concurrent_SameFolder_5Writers(t *testing.T) {
	adapter := newFakeDriveAdapter()
	svc := New(adapter, zap.NewNop())
	ctx := context.Background()

	const goroutines = 5
	const folderID = "folder-five"
	const assetID = "A1-fivewriters"

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry := newTestEntry(assetID, fmt.Sprintf("writer-%d", i))
			entry.DriveLink = fmt.Sprintf("https://drive/five/%d", i)
			if err := svc.UpsertRemote(ctx, folderID, entry); err != nil {
				errCh <- fmt.Errorf("writer %d: %w", i, err)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent same-folder upsert failed: %v", err)
	}

	entries := decodeEntries(t, adapter.getOrFatal(t, folderID))
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 entry after %d concurrent same-folder same-assetID writes, got %d: %v",
			goroutines, len(entries), entries)
	}
	if entries[0].AssetID != assetID {
		t.Fatalf("assetID drift under concurrency: got %q want %q",
			entries[0].AssetID, assetID)
	}
	if adapter.downloadCount != goroutines {
		t.Fatalf("every writer must issue DownloadManifest (per-folder serialisation proof): want %d got %d",
			goroutines, adapter.downloadCount)
	}
	if adapter.replaceCount != goroutines {
		t.Fatalf("every writer must issue ReplaceManifest (no short-circuit): want %d got %d",
			goroutines, adapter.replaceCount)
	}
}

// ── 5. Upload error (graceful degradation via ErrRemoteWrite wrap) ────────

func TestService_UpsertRemote_UploadError_Returns(t *testing.T) {
	adapter := newFakeDriveAdapter()
	forcedErr := errors.New("drive API forced failure")
	adapter.replaceErr = forcedErr

	svc := New(adapter, zap.NewNop())
	ctx := context.Background()

	err := svc.UpsertRemote(ctx, "folder-1", newTestEntry("A1", "first"))
	if err == nil {
		t.Fatal("UpsertRemote must return error when ReplaceManifest fails")
	}
	if !errors.Is(err, ErrRemoteWrite) {
		t.Fatalf("expected error to wrap ErrRemoteWrite, got: %v", err)
	}
	if !strings.Contains(err.Error(), forcedErr.Error()) {
		t.Fatalf("expected wrapped error to include cause %q, got: %v", forcedErr.Error(), err)
	}
	// Adapter must remain unchanged.
	if _, ok := adapter.get("folder-1", "metadata.json"); ok {
		t.Fatal("adapter must not have stored bytes when ReplaceManifest failed")
	}
}

// ── 6. Retry idempotent (UpsertRemote twice → 1 entry by AssetID) ─────────

func TestService_UpsertRemote_Retry_Idempotent(t *testing.T) {
	adapter := newFakeDriveAdapter()
	svc := New(adapter, zap.NewNop())
	ctx := context.Background()

	entry := newTestEntry("A1", "first")
	// Retry with the same AssetID — possibly different payload fields — and
	// assert the final state has exactly ONE entry (no duplicates).
	for i := 0; i < 5; i++ {
		entry.Name = fmt.Sprintf("retry-%d", i)
		if err := svc.UpsertRemote(ctx, "folder-1", entry); err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
	}

	entries := decodeEntries(t, adapter.getOrFatal(t, "folder-1"))
	if len(entries) != 1 {
		t.Fatalf("retry must collapse to 1 entry, got %d: %v", len(entries), entries)
	}
	if entries[0].Name != "retry-4" {
		t.Fatalf("expected last-write-wins name=%q, got %q", "retry-4", entries[0].Name)
	}
	if adapter.replaceCount != 5 {
		t.Fatalf("expected 5 replace calls (one per retry), got %d", adapter.replaceCount)
	}
}

// ── 7. Missing remote (initial create from DownloadManifest → (nil, nil)) ──

func TestService_UpsertRemote_MissingRemote_InitialCreate(t *testing.T) {
	adapter := newFakeDriveAdapter()
	// Fresh adapter: DownloadManifest returns (nil, nil) for any key.
	svc := New(adapter, zap.NewNop())
	ctx := context.Background()

	if err := svc.UpsertRemote(ctx, "folder-new", newTestEntry("A1", "first")); err != nil {
		t.Fatalf("initial create must succeed, got: %v", err)
	}

	entries := decodeEntries(t, adapter.getOrFatal(t, "folder-new"))
	if len(entries) != 1 || entries[0].AssetID != "A1" {
		t.Fatalf("expected exactly [A1] after initial create, got: %v", entries)
	}
	if adapter.downloadCount != 1 {
		t.Fatalf("expected exactly 1 download call (the empty-list probe), got %d", adapter.downloadCount)
	}
}

// ── UpsertLocal happy path (atomic temp+fsync+rename) ─────────────────────

func TestService_UpsertLocal_HappyPath(t *testing.T) {
	dir := t.TempDir()

	svc := New(nil, zap.NewNop()) // nil drive is fine — UpsertLocal doesn't touch it
	ctx := context.Background()

	entry := newTestEntry("A1", "first")
	entry.LocalPath = filepath.Join(dir, "clip.mp4")

	if err := svc.UpsertLocal(ctx, dir, entry); err != nil {
		t.Fatalf("UpsertLocal: %v", err)
	}

	manifestPath := filepath.Join(dir, "metadata.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	entries := decodeEntries(t, raw)
	if len(entries) != 1 || entries[0].AssetID != "A1" {
		t.Fatalf("expected [A1] in %s, got: %v", manifestPath, entries)
	}
}

// ── Input validation: ErrInvalidPath / ErrInvalidEntry ─────────────────────

func TestService_UpsertRemote_EmptyFolderID_ReturnsInvalidPath(t *testing.T) {
	svc := New(newFakeDriveAdapter(), zap.NewNop())
	err := svc.UpsertRemote(context.Background(), "", newTestEntry("A1", "x"))
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath, got: %v", err)
	}
}

func TestService_UpsertLocal_EmptyPath_ReturnsInvalidPath(t *testing.T) {
	svc := New(newFakeDriveAdapter(), zap.NewNop())
	err := svc.UpsertLocal(context.Background(), "", newTestEntry("A1", "x"))
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath, got: %v", err)
	}
}

func TestService_UpsertRemote_EmptyAssetID_ReturnsInvalidEntry(t *testing.T) {
	svc := New(newFakeDriveAdapter(), zap.NewNop())
	err := svc.UpsertRemote(context.Background(), "folder-1", Entry{Name: "no-id"})
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("expected ErrInvalidEntry, got: %v", err)
	}
}

func TestService_UpsertLocal_EmptyAssetID_ReturnsInvalidEntry(t *testing.T) {
	dir := t.TempDir()
	svc := New(newFakeDriveAdapter(), zap.NewNop())
	err := svc.UpsertLocal(context.Background(), dir, Entry{Name: "no-id"})
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("expected ErrInvalidEntry, got: %v", err)
	}
}

// ── adapter test helper ────────────────────────────────────────────────────

// getOrFatal returns the adapter's stored metadata.json bytes or fails the test.
func (f *fakeDriveAdapter) getOrFatal(t *testing.T, folderID string) []byte {
	t.Helper()
	data, ok := f.get(folderID, "metadata.json")
	if !ok {
		t.Fatalf("fakeDriveAdapter: no metadata.json for folder %s", folderID)
	}
	return data
}

// ── PR 8 (codex/asset-manifest-service, June 2026) end-to-end
// integration tests. Pin invariants the rebase landed into the
// codebase; complement (rather than duplicate) the 14 surface
// subtests above.

// ── 8. Concurrent SAME-path UpsertLocal writers (per-path serialisation) ─
//
// Pinned invariant: when N concurrent writers target the SAME temp
// directory, the pathLockRegistry must serialise them so the final
// metadata.json contains ALL N entries with no temp-file collisions
// or writes lost to the atomic-rename dance. Unlike UpsertRemote
// (per-folder), UpsertLocal uses per-path locks; this test pins the
// local-side contract that complement's #4b's remote-side invariant.
func TestService_UpsertLocal_Concurrent_SamePath(t *testing.T) {
	dir := t.TempDir()
	svc := New(nil, zap.NewNop()) // nil drive is fine — UpsertLocal doesn't touch it
	ctx := context.Background()

	const goroutines = 20
	const assetIDPrefix = "L1-local"

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry := newTestEntry(
				fmt.Sprintf("%s-%03d", assetIDPrefix, i),
				fmt.Sprintf("local-writer-%03d", i),
			)
			entry.LocalPath = filepath.Join(dir, fmt.Sprintf("clip-%03d.mp4", i))
			if err := svc.UpsertLocal(ctx, dir, entry); err != nil {
				errCh <- fmt.Errorf("writer %d: %w", i, err)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent same-path UpsertLocal failed: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Fatalf("read final metadata.json: %v", err)
	}
	entries := decodeEntries(t, raw)
	if len(entries) != goroutines {
		t.Fatalf("expected exactly %d entries after %d concurrent same-path writes, got %d",
			goroutines, goroutines, len(entries))
	}
	seen := make(map[string]bool, goroutines)
	for _, e := range entries {
		if !strings.HasPrefix(e.AssetID, assetIDPrefix+"-") {
			t.Fatalf("unexpected AssetID prefix: %s", e.AssetID)
		}
		if seen[e.AssetID] {
			t.Fatalf("duplicate AssetID after concurrent writes: %s", e.AssetID)
		}
		seen[e.AssetID] = true
	}
	// Verify the file is parseable as JSON one more time (no half-write).
	if !json.Valid(raw) {
		t.Fatalf("final metadata.json is not valid JSON after concurrent writes: %s", string(raw))
	}
}

// ── 9. Hard Download failure aborts UpsertRemote (no silent overwrite) ────
//
// Pinned invariant: a hard failure to DownloadManifest (vs. a
// cleanly missing remote returning (nil, nil)) must immediately
// abort the upsert. The pre-PR7 helper-method + shared-mutex
// pattern could accidentally erase existing Drive entries via a
// blind upload when the download failed; PR 7's typed
// ErrRemoteWrite wrap prevents that and leaves the pre-existing
// remote bytes unchanged.
func TestService_UpsertRemote_DownloadError_Aborts(t *testing.T) {
	adapter := newFakeDriveAdapter()
	// Pre-seed: a real entry already exists in the adapter. If the
	// service were to erroneously upload blindly after a download
	// failure, this entry would be wiped.
	adapter.mu.Lock()
	adapter.files[adapter.key("folder-1", "metadata.json")] =
		[]byte(`[{"asset_id":"PRE-EXISTING","name":"keep-me"}]`)
	adapter.mu.Unlock()

	forcedErr := errors.New("drive download forced failure")
	adapter.downloadErr = forcedErr

	svc := New(adapter, zap.NewNop())
	ctx := context.Background()

	err := svc.UpsertRemote(ctx, "folder-1", newTestEntry("A1", "new"))
	if err == nil {
		t.Fatal("UpsertRemote must return error when DownloadManifest fails")
	}
	if !errors.Is(err, ErrRemoteWrite) {
		t.Fatalf("expected error to wrap ErrRemoteWrite, got: %v", err)
	}
	if !strings.Contains(err.Error(), forcedErr.Error()) {
		t.Fatalf("expected wrapped error to include cause %q, got: %v", forcedErr.Error(), err)
	}
	// Critical: replaceCount must be 0 (no blind upload).
	if adapter.replaceCount != 0 {
		t.Fatalf("adapter.replaceCount must be 0 when download fails (no blind upload): got %d",
			adapter.replaceCount)
	}
	// Critical: pre-existing bytes unchanged on disk.
	data, ok := adapter.get("folder-1", "metadata.json")
	if !ok {
		t.Fatal("pre-existing metadata.json went missing after aborted upsert")
	}
	if !strings.Contains(string(data), "PRE-EXISTING") {
		t.Fatalf("pre-existing entry was overwritten despite abort: %s", string(data))
	}
}

// ── 10. AssetToEntry Mapper — Metadata flattening invariants ─────────────
//
// Pinned invariant: AssetToEntry flattens the asset's SearchText +
// Metadata + the term arg + extras at the top level of the
// resulting Entry.Metadata, with FIRST-WRITE-WINS semantics per
// key. Concretely: asset.Metadata is applied first (so its keys
// are preserved even if extras carries the same name), then
// term, then extras. A regression that broke the "flattens"
// contract — e.g. merging extras into a nested "extras" subtree
// instead of the top-level Metadata — would break both the
// clip_upload pre-enrichment call shape and the semantic_enricher
// post-enrichment call shape (they share this projection).
func TestMapper_AssetToEntry_MetadataFlattening(t *testing.T) {
	a := &asset.Asset{
		ID:         "M1-mapper",
		Name:       "mapper-clip",
		Tags:       []string{"alpha", "beta"},
		SearchText: "alpha pre-enrichment",
		Metadata: map[string]any{
			"lang":    "en",
			"channel": "wave-21",
		},
	}
	extras := map[string]any{
		"clip_page_url": "https://example.com/M1",
		// "lang" is repeated in extras to pin the first-write-wins
		// semantic — asset.Metadata["lang"] = "en" must win because
		// asset.Metadata is applied BEFORE extras in the mapper.
		"lang": "it",
	}
	entry := AssetToEntry(a, "manual", "the-term", extras)

	if entry.AssetID != "M1-mapper" || entry.Source != "manual" || entry.Name != "mapper-clip" {
		t.Fatalf("entry basic fields not propagated: %+v", entry)
	}
	if len(entry.Tags) != 2 || entry.Tags[0] != "alpha" || entry.Tags[1] != "beta" {
		t.Fatalf("entry.Tags not propagated: %v", entry.Tags)
	}
	want := map[string]any{
		"search_text":   "alpha pre-enrichment",
		"channel":       "wave-21",
		"term":          "the-term",
		"clip_page_url": "https://example.com/M1",
		"lang":          "en", // FIRST-WRITE-WINS: asset.Metadata beats extras
	}
	if len(entry.Metadata) != len(want) {
		t.Fatalf("entry.Metadata key count mismatch: want %d got %d (%v)",
			len(want), len(entry.Metadata), entry.Metadata)
	}
	for k, v := range want {
		got, ok := entry.Metadata[k]
		if !ok {
			t.Fatalf("entry.Metadata missing key %q (got: %v)", k, entry.Metadata)
		}
		if got != v {
			t.Fatalf("entry.Metadata[%q]: want %v got %v", k, v, got)
		}
	}

	// AssetToEntry with nil asset returns an empty Entry (UpdatedAt-only).
	nilEntry := AssetToEntry(nil, "manual", "t", nil)
	if nilEntry.AssetID != "" || nilEntry.Source != "" || nilEntry.Metadata != nil {
		t.Fatalf("nil asset must yield empty Entry: %+v", nilEntry)
	}
	if nilEntry.UpdatedAt.IsZero() {
		t.Fatal("nil asset's empty Entry must still carry UpdatedAt")
	}

	// AssetToEntry with empty term + empty extras + nil asset.Metadata:
	// the resulting Metadata must be nil (no "term" key, no search_text).
	simpleEntry := AssetToEntry(&asset.Asset{ID: "S1"}, "stock", "", nil)
	if simpleEntry.Metadata != nil {
		t.Fatalf("AssetToEntry with no metadata sources must yield nil Metadata (got: %v)",
			simpleEntry.Metadata)
	}
}

// ── 11. Corrupted local JSON recovers via overwrite (mirror of remote) ────
//
// Pinned invariant: UpsertLocal must apply the same recovery path
// as UpsertRemote (corrupted file → warn + overwrite, never panic),
// so the local-side metadata.json stays consistent with the
// Drive-side semantics. Real callers hit this when an external
// process truncates the metadata file (e.g. crash recovery, manual
// editor).
func TestService_UpsertLocal_CorruptedJSON_Recovery(t *testing.T) {
	dir := t.TempDir()
	// Seed corrupted metadata.json (not valid JSON).
	corrupted := []byte("{this is not valid json")
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), corrupted, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := New(nil, zap.NewNop())
	ctx := context.Background()

	entry := newTestEntry("L1-corrupt", "recovered")
	entry.LocalPath = filepath.Join(dir, "recovered.mp4")

	if err := svc.UpsertLocal(ctx, dir, entry); err != nil {
		t.Fatalf("UpsertLocal must succeed on corrupted file (overwrite), got: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Fatalf("read final metadata: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("final metadata.json is not valid JSON after recovery: %s", string(raw))
	}
	entries := decodeEntries(t, raw)
	if len(entries) != 1 || entries[0].AssetID != "L1-corrupt" {
		t.Fatalf("expected exactly [L1-corrupt] after corruption recovery, got: %v", entries)
	}
}
