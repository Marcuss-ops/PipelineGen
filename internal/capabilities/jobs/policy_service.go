package jobs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"

	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

type AssetTransferServiceImpl struct {
	assetIndex    *assetindex.Service
	querySvc      *asset.Service
	imagesRepo    *imagesrepo.ImagesRepository
	voiceoverRepo *sqliteassets.VoiceoversRepository
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

func NewAssetTransferService(assetIndex *assetindex.Service, querySvc *asset.Service, imagesRepo *imagesrepo.ImagesRepository, voiceoverRepo *sqliteassets.VoiceoversRepository, log *zap.Logger) *AssetTransferServiceImpl {
	return NewAssetTransferServiceWithUploadRoot(assetIndex, querySvc, imagesRepo, voiceoverRepo, "", log)
}

func NewAssetTransferServiceWithUploadRoot(assetIndex *assetindex.Service, querySvc *asset.Service, imagesRepo *imagesrepo.ImagesRepository, voiceoverRepo *sqliteassets.VoiceoversRepository, uploadRoot string, log *zap.Logger) *AssetTransferServiceImpl {
	if strings.TrimSpace(uploadRoot) == "" {
		uploadRoot = filepath.Join(os.TempDir(), "pipelinegen", "worker-uploads")
	}
	return &AssetTransferServiceImpl{
		assetIndex:    assetIndex,
		querySvc:      querySvc,
		imagesRepo:    imagesRepo,
		voiceoverRepo: voiceoverRepo,
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
	if s.assetIndex == nil {
		return nil, fmt.Errorf("asset index service not configured")
	}
	rec, err := s.assetIndex.GetByID(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		rec = &assetindex.AssetRecord{
			AssetID:   assetID,
			AssetType: "worker-output",
			Source:    "worker",
			SourceID:  assetID,
		}
	}
	rec.Status = "uploading"
	if rec.AssetType == "" {
		rec.AssetType = "worker-output"
	}
	if rec.Source == "" {
		rec.Source = "worker"
	}
	if rec.SourceID == "" {
		rec.SourceID = assetID
	}
	if err := s.assetIndex.Upsert(ctx, rec); err != nil {
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
	if s.assetIndex == nil {
		return fmt.Errorf("asset index service not configured")
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

	rec, err := s.assetIndex.GetByID(ctx, assetID)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = &assetindex.AssetRecord{
			AssetID:   assetID,
			AssetType: "worker-output",
			Source:    "worker",
			SourceID:  assetID,
		}
	}
	if rec.AssetType == "" {
		rec.AssetType = "worker-output"
	}
	if rec.Source == "" {
		rec.Source = "worker"
	}
	if rec.SourceID == "" {
		rec.SourceID = assetID
	}
	rec.LocalPath = dst
	rec.Status = "uploaded"
	return s.assetIndex.Upsert(ctx, rec)
}

func (s *AssetTransferServiceImpl) FinalizeUpload(ctx context.Context, assetID string) error {
	if s.assetIndex == nil {
		return fmt.Errorf("asset index service not configured")
	}
	rec, err := s.assetIndex.GetByID(ctx, assetID)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = &assetindex.AssetRecord{
			AssetID:   assetID,
			AssetType: "worker-output",
			Source:    "worker",
			SourceID:  assetID,
		}
	}
	if rec.AssetType == "" {
		rec.AssetType = "worker-output"
	}
	if rec.Source == "" {
		rec.Source = "worker"
	}
	if rec.SourceID == "" {
		rec.SourceID = assetID
	}
	rec.Status = "ready"
	return s.assetIndex.Upsert(ctx, rec)
}

func (s *AssetTransferServiceImpl) resolve(ctx context.Context, assetID string) (*resolvedAsset, error) {
	if s.assetIndex != nil {
		if rec, err := s.assetIndex.GetByID(ctx, assetID); err != nil {
			if s.log != nil {
				s.log.Warn("asset_index lookup failed", zap.String("asset_id", assetID), zap.Error(err))
			}
		} else if rec != nil {
			return convertAssetRecord(rec.AssetID, rec.LocalPath, rec.DriveLink, rec.DownloadLink, assetID), nil
		}
	}

	if s.querySvc != nil {
		if details, err := s.querySvc.Get(ctx, assetID); err == nil && details != nil && details.Asset != nil {
			return convertMediaAsset(details), nil
		}
	}

	if s.imagesRepo != nil {
		if img, err := s.imagesRepo.GetByID(ctx, assetID); err == nil && img != nil {
			return &resolvedAsset{
				AssetID:      assetID,
				Filename:     filepath.Base(img.PathRel),
				LocalPath:    img.PathRel,
				DownloadLink: img.SourceURL,
			}, nil
		}
	}

	if s.voiceoverRepo != nil {
		if rec, err := s.voiceoverRepo.GetByID(ctx, assetID); err == nil && rec != nil {
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
		_ = s.sourceStager.Release(context.Background(), prepared.CleanupToken)
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
	// Use a fresh context for release so a cancelled request context
	// does not prevent staging release. The release is best-effort.
	releaseErr := r.stager.Release(context.Background(), r.prepared.CleanupToken)
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

func convertAssetRecord(assetID, localPath, driveLink, downloadLink, fallbackID string) *resolvedAsset {
	filename := filepath.Base(localPath)
	if filename == "." || filename == string(filepath.Separator) || filename == "" {
		filename = fallbackID
	}
	return &resolvedAsset{
		AssetID:      assetID,
		Filename:     filename,
		LocalPath:    localPath,
		DriveLink:    driveLink,
		DownloadLink: downloadLink,
	}
}

func convertMediaAsset(details *asset.Details) *resolvedAsset {
	assetItem := details.Asset
	filename := assetItem.Filename
	localPath := ""
	driveLink := ""
	downloadLink := ""
	for _, loc := range details.Locations {
		if loc.LocationKind == asset.LocationKindLocal {
			localPath = loc.URI
		} else if loc.LocationKind == asset.LocationKindDrive {
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
		if fileID := driveutil.FileIDFromLink(rawURL); fileID != "" {
			return "https://drive.google.com/uc?export=download&id=" + fileID
		}
	}
	return rawURL
}


