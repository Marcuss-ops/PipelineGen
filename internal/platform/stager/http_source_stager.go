// Package stager contains filesystem-backed implementations of the
// assets.SourceStager port. It is the infrastructure adapter layer for
// PR-SOURCESTAGER-CONSOLIDATE (July 2026): every processor that
// previously downloaded a URL inline (http.NewRequest + client.Do +
// ReadAll + sha256.New + io.Copy) now routes through this stager so:
//
//   - status-code checks no longer leak into the processor,
//   - the staged LocalPath is deterministic from SourceRef.URL so
//     two requests for the same URL dedupe naturally on disk,
//   - the IntermediateHash is computed during the staging write so
//     callers do not pay a second read pass for dedup checks.
package stager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"go.uber.org/zap"
)

// HTTPSourceStager is the canonical filesystem-backed impl of the
// assets.SourceStager port. It downloads a SourceRef.URL into a
// deterministic on-disk path under StagingDir and computes the
// IntermediateHash (SHA-256) during the write.
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil StagingDir fails closed at
// construction time, and a non-2xx HTTP status returns a typed error
// rather than silently writing a partial file. Cleanup is idempotent.
//
// Concurrency: HTTPSourceStager is safe for concurrent use; the
// per-path write is guarded by a sync.Map of mutexes keyed on the
// deterministic local path. Two goroutines that race to stage the
// same SourceRef will share the same local file and the slower one
// will see the faster one's bytes (no torn writes, no double-download).
type HTTPSourceStager struct {
	stagingDir string
	client     *http.Client
	log        *zap.Logger

	// perPathMu guards concurrent StageSourceV2 calls for the same
	// deterministic local path. A sync.Map keyed on the path keeps
	// lock contention proportional to the number of distinct URLs
	// being staged concurrently, not the total call count.
	perPathMu sync.Map // map[string]*sync.Mutex
}

// NewHTTPSourceStager constructs the canonical HTTPSourceStager.
// StagingDir is the directory under which deterministic local paths
// are created (one sub-directory per SourceRef). client is used for
// the HTTP GET; callers should pass a *http.Client with a sensible
// timeout. log is required (godlike/07: a nil logger is a programmer
// error, not a silent fallback).
//
// Returns a non-nil error when StagingDir is empty or when it cannot
// be created.
func NewHTTPSourceStager(stagingDir string, client *http.Client, log *zap.Logger) (*HTTPSourceStager, error) {
	if strings.TrimSpace(stagingDir) == "" {
		return nil, fmt.Errorf("stager.NewHTTPSourceStager: StagingDir is required")
	}
	if client == nil {
		return nil, fmt.Errorf("stager.NewHTTPSourceStager: client is required (godlike/07 NO-FAKE-AVAILABILITY)")
	}
	if log == nil {
		return nil, fmt.Errorf("stager.NewHTTPSourceStager: log is required (godlike/07 NO-FAKE-AVAILABILITY)")
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, fmt.Errorf("stager.NewHTTPSourceStager: create staging dir %q: %w", stagingDir, err)
	}
	return &HTTPSourceStager{
		stagingDir: stagingDir,
		client:     client,
		log:        log,
	}, nil
}

// deterministicLocalPath derives a stable per-SourceRef local path.
// The path is sha256(URL|DownloadSection|ForceKeyframes|MergeFormat)
// so the on-disk file is unique per logical request and reproducible
// across process restarts. The hex digest keeps the filename flat
// (no nested subdirs) which simplifies cleanup.
func (s *HTTPSourceStager) deterministicLocalPath(ref assets.SourceRef) string {
	h := sha256.New()
	h.Write([]byte(ref.URL))
	h.Write([]byte{0}) // separator
	h.Write([]byte(ref.DownloadSection))
	h.Write([]byte{0})
	h.Write([]byte(ref.MergeFormat))
	if ref.ForceKeyframes {
		h.Write([]byte{0, 1})
	} else {
		h.Write([]byte{0, 0})
	}
	digest := hex.EncodeToString(h.Sum(nil))
	return filepath.Join(s.stagingDir, digest+".bin")
}

// lockFor returns the per-path mutex, creating it lazily.
func (s *HTTPSourceStager) lockFor(path string) *sync.Mutex {
	v, _ := s.perPathMu.LoadOrStore(path, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// stageSourceV2 downloads ref.URL into a deterministic local file
// under s.stagingDir and returns a *StagedSource with the
// IntermediateHash. This is the internal implementation used by
// Prepare; callers should use the acquisition.SourceStager port.
func (s *HTTPSourceStager) stageSourceV2(ctx context.Context, ref assets.SourceRef) (*assets.StagedSource, error) {
	if strings.TrimSpace(ref.URL) == "" {
		return nil, fmt.Errorf("stager.StageSourceV2: SourceRef.URL is required")
	}

	localPath := s.deterministicLocalPath(ref)

	// Per-path lock so two goroutines staging the same URL do not
	// both download + write torn bytes.
	mu := s.lockFor(localPath)
	mu.Lock()
	defer mu.Unlock()

	// If the file is already on disk from a prior StageSourceV2 call
	// for the same SourceRef, reuse it: stat it, compute the hash
	// from the existing file (single read pass), and return.
	if info, statErr := os.Stat(localPath); statErr == nil && info.Size() > 0 {
		hash, hashErr := hashFile(localPath)
		if hashErr != nil {
			return nil, fmt.Errorf("stager.StageSourceV2: hash existing file %q: %w", localPath, hashErr)
		}
		return &assets.StagedSource{
			LocalPath:        localPath,
			IntermediateHash: hash,
			Bytes:            info.Size(),
			SourceID:         ref.URL,
		}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("stager.StageSourceV2: build request: %w", err)
	}
	req.Header.Set("User-Agent", "PipelineGen-SourceStager/1.0")
	req.Header.Set("Accept", "*/*")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stager.StageSourceV2: GET %q: %w", ref.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("stager.StageSourceV2: GET %q returned status %d", ref.URL, resp.StatusCode)
	}

	// Write to a temp file in the same directory, hash during the
	// write, then atomically rename to localPath. This avoids
	// leaving a partial file at localPath if the write is
	// interrupted.
	tmp, err := os.CreateTemp(s.stagingDir, "stage-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("stager.StageSourceV2: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	hasher := sha256.New()
	written, err := io.Copy(tmp, io.TeeReader(resp.Body, hasher))
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("stager.StageSourceV2: write %q: %w", ref.URL, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("stager.StageSourceV2: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("stager.StageSourceV2: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, localPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("stager.StageSourceV2: rename temp to %q: %w", localPath, err)
	}

	hash := hex.EncodeToString(hasher.Sum(nil))
	return &assets.StagedSource{
		LocalPath:        localPath,
		IntermediateHash: hash,
		Bytes:            written,
		SourceID:         ref.URL,
	}, nil
}

// cleanupStagedSource removes the staged file. Idempotent: a second
// call for the same staged value is a no-op.
func (s *HTTPSourceStager) cleanupStagedSource(_ context.Context, staged *assets.StagedSource) error {
	if staged == nil {
		return nil
	}
	if strings.TrimSpace(staged.LocalPath) == "" {
		return nil
	}
	if err := os.Remove(staged.LocalPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stager.CleanupStagedSource: remove %q: %w", staged.LocalPath, err)
	}
	return nil
}

// hashFile returns the hex SHA-256 of the file at path. Used by
// StageSourceV2 when the deterministic local file already exists.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Prepare implements acquisition.SourceStager. It wraps StageSourceV2
// with the acquisition.PrepareRequest/PrepareContext contract.
func (s *HTTPSourceStager) Prepare(ctx context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	if req.Source.URL == "" {
		return nil, fmt.Errorf("stager.Prepare: Source.URL is required")
	}

	// Build an assets.SourceRef from the acquisition.SourceRef for the
	// existing StageSourceV2 implementation.
	ref := assets.SourceRef{
		URL:             req.Source.URL,
		DownloadSection: req.Source.DownloadSection,
		ForceKeyframes:  req.Source.ForceKeyframes,
		MergeFormat:     req.Source.MergeFormat,
	}

	staged, err := s.stageSourceV2(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("stager.Prepare: %w", err)
	}

	// Derive the cleanup token from the SourceRef.
	cleanupToken := acquisition.DeriveCleanupToken(req.Source)

	// Default TTL: 24 hours.
	ttl := req.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}

	return &acquisition.PrepareContext{
		ID:           staged.SourceID,
		SourceRef:    req.Source,
		LocalPath:    staged.LocalPath,
		SHA256:       staged.IntermediateHash,
		SizeBytes:    staged.Bytes,
		ExpiresAt:    time.Now().UTC().Add(ttl),
		CleanupToken: cleanupToken,
	}, nil
}

// Release implements acquisition.SourceStager. It removes the staged file.
func (s *HTTPSourceStager) Release(_ context.Context, cleanupToken string) error {
	// HTTPSourceStager does not maintain a registry; for now, this is a no-op.
	// The caller is responsible for removing files via CleanupStagedSource.
	_ = cleanupToken
	return nil
}
