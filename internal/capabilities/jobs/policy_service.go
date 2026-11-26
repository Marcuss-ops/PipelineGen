package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/driveurl"
)

// AssetDetailsLookup is the consumer-owned detail lookup for the asset transfer
// service. It is an interface rather than the concrete SQLite *detail.Service
// so the PostgreSQL media SSOT store can serve it: PostgreSQL is the media
// SSOT, and the SQLite service is selected only in the documented
// media-disabled degrade mode.
type AssetDetailsLookup interface {
	Get(ctx context.Context, id string) (*asset.Details, error)
}

type AssetTransferServiceImpl struct {
	// workerOutputs, images and voiceovers are consumer-owned ports (see
	// asset_transfer_ports.go). The capability no longer names a SQLite type.
	workerOutputs WorkerOutputStore
	querySvc      AssetDetailsLookup
	images        ImageAssetLookup
	voiceovers    VoiceoverLookup
	uploadRoot    string
	// sourceStager is the canonical port for staging remote URLs
	// into deterministic local files (PR-SOURCESTAGER-CONSOLIDATE,
	// July 2026). fetch routes web URL downloads through it so the
	// inline `http.NewRequest + client.Do` boilerplate no longer
	// leaks into the processor. Nil is tolerated (test fixture,
	// partial deploy); fetch fails closed with a typed error when
	// nil (godlike/07).
	sourceStager acquisition.SourceStager
	log          *zap.Logger
}

type UploadResponse struct {
	UploadID string `json:"upload_id"`
	URL      string `json:"url,omitempty"`
}

type resolvedAsset struct {
	AssetID      string
	Filename     string
	LocalPath    string
	DriveLink    string
	DownloadLink string
}

func NewAssetTransferService(workerOutputs WorkerOutputStore, querySvc AssetDetailsLookup, images ImageAssetLookup, voiceovers VoiceoverLookup, log *zap.Logger) *AssetTransferServiceImpl {
	return NewAssetTransferServiceWithUploadRoot(workerOutputs, querySvc, images, voiceovers, "", log)
}

func NewAssetTransferServiceWithUploadRoot(workerOutputs WorkerOutputStore, querySvc AssetDetailsLookup, images ImageAssetLookup, voiceovers VoiceoverLookup, uploadRoot string, log *zap.Logger) *AssetTransferServiceImpl {
	if strings.TrimSpace(uploadRoot) == "" {
		uploadRoot = filepath.Join(os.TempDir(), "pipelinegen", "worker-uploads")
	}
	return &AssetTransferServiceImpl{
		workerOutputs: workerOutputs,
		querySvc:      querySvc,
		images:        images,
		voiceovers:    voiceovers,
		uploadRoot:    uploadRoot,
		log:           log,
	}
}

// WithSourceStager injects the canonical SourceStager used by fetch
// to stage remote URLs into deterministic local files
// (PR-SOURCESTAGER-CONSOLIDATE, July 2026). It is a setter (not a
// constructor arg) to keep the NewServiceWithUploadRoot signature
// stable for existing call sites. A nil stager keeps fetch failing
// closed with a typed error per godlike/07.
func (s *AssetTransferServiceImpl) WithSourceStager(stager acquisition.SourceStager) *AssetTransferServiceImpl {
	s.sourceStager = stager
	return s
}

func (s *AssetTransferServiceImpl) Download(ctx context.Context, assetID string) (io.ReadCloser, string, error) {
	rec, err := s.resolve(ctx, assetID)
	if err != nil {
		return nil, "", err
	}
	if rec == nil {
		return nil, "", fmt.Errorf("asset %s not found", assetID)
	}

	filename := strings.TrimSpace(rec.Filename)
	if filename == "" {
		if rec.LocalPath != "" {
			filename = filepath.Base(rec.LocalPath)
		} else {
			filename = rec.AssetID
		}
	}

	if rec.LocalPath != "" {
		f, err := os.Open(rec.LocalPath)
		if err != nil {
			return nil, "", fmt.Errorf("open local asset %s: %w", rec.LocalPath, err)
		}
		return f, filename, nil
	}

	for _, rawURL := range []string{rec.DownloadLink, rec.DriveLink} {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			continue
		}
		if filename == rec.AssetID {
			filename = ""
		}
		return s.fetch(ctx, rawURL, filename)
	}

	return nil, "", fmt.Errorf("asset %s has no downloadable source", assetID)
}

func (s *AssetTransferServiceImpl) InitiateUpload(ctx context.Context, assetID string) (*UploadResponse, error) {
	if s.workerOutputs == nil {
		return nil, fmt.Errorf("worker-output store not configured")
	}
	if err := s.workerOutputs.MarkUploading(ctx, assetID); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(s.uploadRoot, assetID), 0o755); err != nil {
		return nil, err
	}
	return &UploadResponse{
		UploadID: assetID,
		URL:      "/internal/v1/worker-assets/uploads/" + assetID + "/content",
	}, nil
}

func (s *AssetTransferServiceImpl) Upload(ctx context.Context, assetID, filename string, content io.Reader) error {
	if s.workerOutputs == nil {
		return fmt.Errorf("worker-output store not configured")
	}
	if strings.TrimSpace(assetID) == "" {
		return fmt.Errorf("asset id is required")
	}
	if content == nil {
		return fmt.Errorf("upload content is required")
	}
	if strings.TrimSpace(filename) == "" {
		filename = assetID
	}
	dir := filepath.Join(s.uploadRoot, assetID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dir, filepath.Base(filename))
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	return s.workerOutputs.MarkUploaded(ctx, assetID, dst)
}

func (s *AssetTransferServiceImpl) FinalizeUpload(ctx context.Context, assetID string) error {
	if s.workerOutputs == nil {
		return fmt.Errorf("worker-output store not configured")
	}
	return s.workerOutputs.MarkReady(ctx, assetID)
}

// resolve finds the canonical source for an asset.
//
// Precedence is the architecture's, not convenience: PostgreSQL is the media
// SSOT (ARCHITECTURE.md), so the canonical media lookup runs FIRST. The SQLite
// asset index is a worker-output projection that can lag a committed media row,
// and the image/voiceover lookups are further projections; answering from the
// index first could serve stale data for an asset the SSOT already owns.
//
// Every port follows the (nil, nil) = absent contract, so an infrastructure
// error is RETURNED instead of being mistaken for "not found". The previous
// implementation discarded every lookup error and returned (nil, nil), which
// made an unreachable database indistinguishable from a missing asset.
func (s *AssetTransferServiceImpl) resolve(ctx context.Context, assetID string) (*resolvedAsset, error) {
	if s.querySvc != nil {
		details, err := s.querySvc.Get(ctx, assetID)
		switch {
		case err != nil && !errors.Is(err, asset.ErrNotFound):
			return nil, fmt.Errorf("resolve asset %s: canonical media lookup: %w", assetID, err)
		case err == nil && details != nil && details.Asset != nil:
			return convertMediaAsset(details), nil
		}
	}

	if s.workerOutputs != nil {
		rec, err := s.workerOutputs.GetByID(ctx, assetID)
		if err != nil {
			return nil, fmt.Errorf("resolve asset %s: worker-output lookup: %w", assetID, err)
		}
		if rec != nil {
			return convertWorkerOutput(rec, assetID), nil
		}
	}

	if s.images != nil {
		img, err := s.images.GetByID(ctx, assetID)
		if err != nil {
			return nil, fmt.Errorf("resolve asset %s: image lookup: %w", assetID, err)
		}
		if img != nil {
			return &resolvedAsset{
				AssetID:      assetID,
				Filename:     filepath.Base(img.PathRel),
				LocalPath:    img.PathRel,
				DownloadLink: img.SourceURL,
			}, nil
		}
	}

	if s.voiceovers != nil {
		rec, err := s.voiceovers.GetByID(ctx, assetID)
		if err != nil {
			return nil, fmt.Errorf("resolve asset %s: voiceover lookup: %w", assetID, err)
		}
		if rec != nil {
			return &resolvedAsset{
				AssetID:      assetID,
				Filename:     rec.Filename,
				LocalPath:    rec.LocalPath,
				DriveLink:    rec.DriveLink,
				DownloadLink: rec.DownloadLink,
			}, nil
		}
	}

	return nil, nil
}

// fetch streams a remote URL into an io.ReadCloser.
//
// PR-SOURCESTAGER-CONSOLIDATE (July 2026): the inline
// `http.NewRequest + s.httpClient.Do + StatusCode` path is retired.
// The download now routes through the canonical acquisition.SourceStager
// port (Prepare) so:
//   - status-code checks no longer leak into the processor,
//   - the staged LocalPath is deterministic from SourceRef.URL
//     (two requests for the same URL dedupe naturally on disk),
//   - the SHA256 is computed during the staging write so
//     callers do not pay a second read pass for dedup checks.
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil stager fails closed with a
// typed error rather than silently falling back to inline http.
// Cleanup of the staged file happens deterministically when the
// returned ReadCloser is closed (via stagedSourceReadCloser.Close).
func (s *AssetTransferServiceImpl) fetch(ctx context.Context, rawURL, filename string) (io.ReadCloser, string, error) {
	if s.sourceStager == nil {
		return nil, "", fmt.Errorf("jobs/assets.fetch: source stager is nil (PR-SOURCESTAGER-CONSOLIDATE: composition root must wire acquisition.SourceStager)")
	}
	sourceURL := normalizeDownloadURL(rawURL)
	req := acquisition.PrepareRequest{
		Source:         acquisition.SourceRef{URL: sourceURL},
		CallerRef:      "jobs-assets-fetch",
		IdempotencyKey: acquisition.DeriveIdempotencyKey(acquisition.SourceRef{URL: sourceURL}),
	}
	prepared, err := s.sourceStager.Prepare(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("jobs/assets.fetch: stage source %q: %w", rawURL, err)
	}
	f, openErr := os.Open(prepared.LocalPath)
	if openErr != nil {
		// Best-effort release of the staged file; do not leak it.
		_ = releaseStaging(s.sourceStager, prepared.CleanupToken)
		return nil, "", fmt.Errorf("jobs/assets.fetch: open staged source %q: %w", prepared.LocalPath, openErr)
	}
	return &stagedSourceReadCloser{
		ReadCloser: f,
		prepared:   prepared,
		stager:     s.sourceStager,
		log:        s.log,
		sourceURL:  rawURL,
	}, filename, nil
}

// stagedSourceReadCloser wraps a *os.File opened from a staged source
// so the staged file is deterministically cleaned up when the
// caller closes the ReadCloser. This preserves the streaming
// io.ReadCloser contract for callers (e.g. Download) while
// transparently routing the URL download through the canonical
// acquisition.SourceStager port.
//
// godlike/07: cleanup failures are logged but do NOT fail Close()
// because the caller has already finished reading the body and a
// stale staging file is a non-fatal operational concern (next
// Prepare call dedupes via deterministic LocalPath).
type stagedSourceReadCloser struct {
	io.ReadCloser
	prepared  *acquisition.PrepareContext
	stager    acquisition.SourceStager
	log       *zap.Logger
	sourceURL string
}

func (r *stagedSourceReadCloser) Close() error {
	closeErr := r.ReadCloser.Close()
	// Use a fresh, BOUNDED context for release: a cancelled request context
	// must not prevent staging release, but detaching it without a deadline
	// (context.Background()) lets a hung adapter keep the goroutine and the
	// temp file alive forever. The release stays best-effort.
	releaseErr := releaseStaging(r.stager, r.prepared.CleanupToken)
	if releaseErr != nil && r.log != nil {
		r.log.Warn("stagedSourceReadCloser: release failed (best-effort)",
			zap.String("local_path", r.prepared.LocalPath),
			zap.String("source_url", r.sourceURL),
			zap.Error(releaseErr))
	}
	if closeErr != nil {
		return closeErr
	}
	return releaseErr
}

// stagingReleaseTimeout bounds the detached staging cleanup. Long enough for a
// local unlink or a registry delete, short enough that a broken adapter cannot
// pin a goroutine (and the staged bytes) indefinitely.
const stagingReleaseTimeout = 30 * time.Second

func releaseStaging(stager acquisition.SourceStager, cleanupToken string) error {
	if stager == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), stagingReleaseTimeout)
	defer cancel()
	return stager.Release(ctx, cleanupToken)
}

func convertWorkerOutput(rec *WorkerOutputRecord, fallbackID string) *resolvedAsset {
	filename := filepath.Base(rec.LocalPath)
	if filename == "." || filename == string(filepath.Separator) || filename == "" {
		filename = fallbackID
	}
	return &resolvedAsset{
		AssetID:      fallbackID,
		Filename:     filename,
		LocalPath:    rec.LocalPath,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
	}
}

func convertMediaAsset(details *asset.Details) *resolvedAsset {
	assetItem := details.Asset
	filename := assetItem.Filename
	localPath := ""
	driveLink := ""
	downloadLink := ""
	for _, loc := range details.Locations {
		switch loc.LocationKind {
		case asset.LocationKindLocal:
			localPath = loc.URI
		case asset.LocationKindDrive:
			driveLink = loc.AccessURL
			downloadLink = loc.DownloadURL
		}
	}
	if filename == "" {
		filename = filepath.Base(localPath)
	}
	if filename == "" {
		filename = assetItem.ID
	}
	dl := downloadLink
	if dl == "" {
		dl = assetItem.ExternalURL()
	}
	return &resolvedAsset{
		AssetID:      assetItem.ID,
		Filename:     filename,
		LocalPath:    localPath,
		DriveLink:    driveLink,
		DownloadLink: dl,
	}
}

func normalizeDownloadURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if strings.Contains(rawURL, "drive.google.com") && !strings.Contains(rawURL, "/uc?") {
		// kernel/driveurl, not platform/drive: the capability needs the URL
		// parse, not the Drive client.
		if fileID := driveurl.FileIDFromLink(rawURL); fileID != "" {
			return "https://drive.google.com/uc?export=download&id=" + fileID
		}
	}
	return rawURL
}
