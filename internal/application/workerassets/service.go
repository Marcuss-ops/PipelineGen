package workerassets

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/images"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/database/drive"
)

type Service struct {
	assetIndex    *assetindex.Service
	querySvc      *assets.Service
	imagesRepo    *images.Repository
	voiceoverRepo *voiceovers.Repository
	uploadRoot    string
	httpClient    *http.Client
	log           *zap.Logger
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

func NewService(assetIndex *assetindex.Service, querySvc *assets.Service, imagesRepo *images.Repository, voiceoverRepo *voiceovers.Repository, log *zap.Logger) *Service {
	return NewServiceWithUploadRoot(assetIndex, querySvc, imagesRepo, voiceoverRepo, "", log)
}

func NewServiceWithUploadRoot(assetIndex *assetindex.Service, querySvc *assets.Service, imagesRepo *images.Repository, voiceoverRepo *voiceovers.Repository, uploadRoot string, log *zap.Logger) *Service {
	if strings.TrimSpace(uploadRoot) == "" {
		uploadRoot = filepath.Join(os.TempDir(), "pipelinegen", "worker-uploads")
	}
	return &Service{
		assetIndex:    assetIndex,
		querySvc:      querySvc,
		imagesRepo:    imagesRepo,
		voiceoverRepo: voiceoverRepo,
		uploadRoot:    uploadRoot,
		httpClient:    &http.Client{Timeout: 2 * time.Minute},
		log:           log,
	}
}

func (s *Service) Download(ctx context.Context, assetID string) (io.ReadCloser, string, error) {
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

func (s *Service) InitiateUpload(ctx context.Context, assetID string) (*UploadResponse, error) {
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

func (s *Service) Upload(ctx context.Context, assetID, filename string, content io.Reader) error {
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

func (s *Service) FinalizeUpload(ctx context.Context, assetID string) error {
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

func (s *Service) resolve(ctx context.Context, assetID string) (*resolvedAsset, error) {
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

func (s *Service) fetch(ctx context.Context, rawURL, filename string) (io.ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizeDownloadURL(rawURL), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, "", fmt.Errorf("remote download failed: %s", resp.Status)
	}
	if filename == "" {
		filename = filenameFromResponse(resp, rawURL)
	}
	return resp.Body, filename, nil
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

func convertMediaAsset(details *assets.Details) *resolvedAsset {
	assetItem := details.Asset
	filename := assetItem.Filename
	localPath := ""
	driveLink := ""
	downloadLink := ""
	for _, loc := range details.Locations {
		if loc.LocationKind == assets.LocationKindLocal {
			localPath = loc.URI
		} else if loc.LocationKind == assets.LocationKindDrive {
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
	return &resolvedAsset{
		AssetID:      assetItem.ID,
		Filename:     filename,
		LocalPath:    localPath,
		DriveLink:    driveLink,
		DownloadLink: firstNonEmpty(downloadLink, assetItem.ExternalURL()),
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

func filenameFromResponse(resp *http.Response, fallbackURL string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if idx := strings.Index(strings.ToLower(cd), "filename="); idx >= 0 {
			name := strings.Trim(cd[idx+len("filename="):], "\"'; ")
			if name != "" {
				return name
			}
		}
	}
	if fallbackURL != "" {
		if idx := strings.LastIndex(fallbackURL, "/"); idx >= 0 && idx < len(fallbackURL)-1 {
			return fallbackURL[idx+1:]
		}
	}
	return "asset.bin"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
