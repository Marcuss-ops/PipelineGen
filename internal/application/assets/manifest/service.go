// Package manifest — Service implementation (PR 6 / PR 7 cutover).
//
// Single Source of Truth for per-asset metadata writes. All three
// call-site writers (clips upload, media processor, Artlist semantic
// enricher) route through this Service. The pre-cutover writers are
// gone; the per-call-site merge of []map[string]any is gone.
//
// Per-path lock (vs. the pre-cutover single global mutex):
//
//   The pre-cutover code serialised ALL writes across ALL clips across
//   ALL folders through a single package-level sync.Mutex. The new
//   Service uses per-path locks keyed by absolute manifest path
//   (local) or "drive:<folderID>" (remote); two writers to DIFFERENT
//   files do not contend, two writers to the SAME file serialise
//   correctly.
//
// Atomic local write (temp + fsync + rename):
//
//   The new Service writes to a uniquely-named temp file in the
//   SAME directory (so os.Rename is atomic on the same fs), calls
//   fsync on the temp file (so its bytes are durable), then
//   atomically renames temp → manifest. Readers never see a
//   partially-written file.
//
// Remote merge-by-AssetID:
//   The adapter's ReplaceManifest is the single boundary that owns
//   the Drive-side file-id lifecycle. The manifest service itself
//   only sees bytes-before and bytes-after; the adapter decides
//   whether to call Files.Update or delete-and-recreate on the
//   target file id.
package manifest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Service is the canonical public surface.
type Service interface {
	// UpsertLocal atomically merges entry into `path`'s metadata.json.
	// `path` is the directory containing the canonical metadata.json
	// (typically the directory where the asset lives). The merge key
	// is Entry.AssetID — existing entries with the same AssetID are
	// REPLACED; new entries are appended. Local-write order:
	// read existing → merge → marshal → atomic temp+fsync+rename.
	UpsertLocal(ctx context.Context, path string, entry Entry) error

	// UpsertRemote atomically merges entry into a folder's
	// metadata.json on Drive. The adapter API is upload-then-replace:
	// if a file with the target filename already exists (non-trashed)
	// in folderID, the adapter updates it via Files.Update; otherwise
	// the adapter creates it. The legacy trash-then-create is REMOVED.
	UpsertRemote(ctx context.Context, folderID string, entry Entry) error
}

// New constructs the canonical impl. drive may be nil for test
// fixtures that only exercise UpsertLocal — a nil drive causes
// UpsertRemote to return ErrRemoteWrite.
func New(drive DriveAdapter, log *zap.Logger) Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &service{
		drive: drive,
		locks: newPathLockRegistry(),
		log:   log,
	}
}

type service struct {
	drive DriveAdapter
	locks *pathLockRegistry
	log   *zap.Logger
}

// pathLockRegistry is a tiny sync.Map-like keyed mutex pool. The
// pre-cutover package-level shared mutexes serialised ALL
// writes globally; the new pool only serializes writes to the SAME
// path/folder. Two writers to different paths run concurrently.
//
// Lifecycle note: locks are never removed from the pool, so the map
// grows monotonically with the number of unique manifest paths ever
// written. For a real workload that's usually < a few thousand clips
// = an acceptable footprint; for unbounded workloads a janitor can
// prune unused locks. PR 7 defers the janitor.
type pathLockRegistry struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newPathLockRegistry() *pathLockRegistry {
	return &pathLockRegistry{locks: make(map[string]*sync.Mutex)}
}

func (r *pathLockRegistry) get(key string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.locks[key]; ok {
		return m
	}
	m := &sync.Mutex{}
	r.locks[key] = m
	return m
}

// UpsertLocal: per-path locked read-merger-write with atomic rename.
func (s *service) UpsertLocal(ctx context.Context, path string, entry Entry) error {
	if path == "" {
		return ErrInvalidPath
	}
	if entry.AssetID == "" {
		return fmt.Errorf("%w: AssetID required", ErrInvalidEntry)
	}
	dir, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("%w: abs path: %v", ErrLocalWrite, err)
	}
	manifestPath := filepath.Join(dir, "metadata.json")
	mu := s.locks.get(manifestPath)
	mu.Lock()
	defer mu.Unlock()

	// Read existing (best-effort; missing file → empty merge target).
	var existing []Entry
	if data, rerr := os.ReadFile(manifestPath); rerr == nil {
		if jerr := json.Unmarshal(data, &existing); jerr != nil {
			s.log.Warn("manifest: parse existing failed; treating as empty",
				zap.String("path", manifestPath), zap.Error(jerr))
			existing = nil
		}
	}

	merged := mergeByAssetID(existing, entry)
	return s.atomicWrite(manifestPath, merged)
}

// UpsertRemote: per-folder locked read-via-adapter → merge → adapter
// replace (which handles upload-then-replace).
func (s *service) UpsertRemote(ctx context.Context, folderID string, entry Entry) error {
	if folderID == "" {
		return ErrInvalidPath
	}
	if entry.AssetID == "" {
		return fmt.Errorf("%w: AssetID required", ErrInvalidEntry)
	}
	if s.drive == nil {
		return fmt.Errorf("%w: drive adapter not wired", ErrRemoteWrite)
	}
	lockKey := "drive:" + folderID
	mu := s.locks.get(lockKey)
	mu.Lock()
	defer mu.Unlock()

	const metaFilename = "metadata.json"
	existingBytes, err := s.drive.DownloadManifest(ctx, folderID, metaFilename)
	if err != nil {
		return fmt.Errorf("%w: download existing: %v", ErrRemoteWrite, err)
	}
	var existing []Entry
	if len(existingBytes) > 0 {
		if jerr := json.Unmarshal(existingBytes, &existing); jerr != nil {
			s.log.Warn("manifest: parse existing remote failed; treating as empty",
				zap.String("folder_id", folderID), zap.Error(jerr))
			existing = nil
		}
	}
	merged := mergeByAssetID(existing, entry)

	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: marshal: %v", ErrRemoteWrite, err)
	}
	if _, err := s.drive.ReplaceManifest(ctx, folderID, metaFilename, data); err != nil {
		return fmt.Errorf("%w: replace: %v", ErrRemoteWrite, err)
	}
	s.log.Info("manifest: remote upsert ok",
		zap.String("folder_id", folderID),
		zap.String("asset_id", entry.AssetID),
		zap.Int("entries", len(merged)))
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────

// mergeByAssetID replaces any existing entry with the same AssetID;
// appends the new entry otherwise. Pure function — no I/O.
func mergeByAssetID(existing []Entry, entry Entry) []Entry {
	for i, e := range existing {
		if e.AssetID == entry.AssetID {
			existing[i] = entry
			return existing
		}
	}
	return append(existing, entry)
}

// atomicWrite writes <entries> to <manifestPath> via temp file +
// fsync + rename. The temp file uses a uniqifier so concurrent
// writers to the SAME manifest (which can't happen here because we
// hold the per-path mutex) cannot collide; the rename is atomic
// w.r.t. readers on the same filesystem.
//
// On any failure the temp is removed before returning so no orphan
// .tmp.NNNN files accumulate.
func (s *service) atomicWrite(manifestPath string, entries []Entry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: marshal: %v", ErrLocalWrite, err)
	}
	dir := filepath.Dir(manifestPath)
	base := filepath.Base(manifestPath)
	tempPath := filepath.Join(dir, fmt.Sprintf("%s.tmp.%d", base, time.Now().UnixNano()))
	f, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("%w: open temp: %v", ErrLocalWrite, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("%w: write temp: %v", ErrLocalWrite, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("%w: fsync temp: %v", ErrLocalWrite, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("%w: close temp: %v", ErrLocalWrite, err)
	}
	if err := os.Rename(tempPath, manifestPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("%w: rename: %v", ErrLocalWrite, err)
	}
	s.log.Info("manifest: local upsert ok",
		zap.String("path", manifestPath),
		zap.Int("entries", len(entries)))
	return nil
}
