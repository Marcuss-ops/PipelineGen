// Package processor — media download / process / Drive upload
// orchestrator. PR 7 (codex/asset-manifest-cutover, June 2026):
// per-asset metadata writes no longer go through a private merge
// method under a global shared sync.Mutex; instead the Processor
// calls into internal/application/assets/manifest.Service which owns
// the per-path lock + atomic merge-by-AssetID semantics.
//
// Pre-cutover symbols removed in this file:
//   - the package-level shared mutex (removed)
//   - the (p *Processor) private metadata merge method (removed)
//
// Pre-cutover imports cleaned up:
//   - encoding/json (no longer needed; manifest owns marshaling)
//   - sync (no longer needed; manifest owns locking)
//   - os (no longer needed for direct WriteFile; manifest writes atomic)
package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/manifest"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Processor orchestrates download via yt-dlp or HTTP, optional ffmpeg
// normalization, perceptual deduplication, file hashing, and Drive upload.
// It implements the canonical core/asset.Processor contract directly.
//
// PR 7: per-asset metadata is delegated to manifest.Service.
// manifestSvc may be nil for Drive-less fixtures (where the
// step-4 cascade is best-effort); in production it is always wired.
type Processor struct {
	dl            YTDLP
	httpDL        HTTPDownloader
	ffmpeg        VideoProcessor
	log           *zap.Logger
	dataDir       string
	tempDir       string
	videoCfg      ffmpeg.NormalizeOptions
	scraperURL    string
	embeddingURL  string
	registry      artifacts.Registry
	driveUploader *drive.Uploader
	manifestSvc   manifest.Service
}

var _ asset.Processor = (*Processor)(nil)

// ProcessorConfig holds the constructor dependencies for Processor.
type ProcessorConfig struct {
	DataDir            string
	TempDir            string
	VideoCfg           ffmpeg.NormalizeOptions
	ScraperServerURL   string // Artlist persistent scraper server (e.g. http://localhost:9123)
	EmbeddingServerURL string // Python embedding/phash server (e.g. http://127.0.0.1:8001)
}

// NewProcessor creates a new media processor with the given dependencies.
//
// PR 7: manifestSvc added as a required parameter. Production wiring
// (composition root) passes the canonical *manifest.Service instance.
// Pass nil to opt out of metadata-side-effects (Drive-less test
// fixtures).
func NewProcessor(
	dl YTDLP,
	httpDL HTTPDownloader,
	ff VideoProcessor,
	log *zap.Logger,
	cfg ProcessorConfig,
	registry artifacts.Registry,
	driveUploader *drive.Uploader,
	manifestSvc manifest.Service,
) *Processor {
	scraperURL := cfg.ScraperServerURL
	if scraperURL == "" {
		scraperURL = "http://127.0.0.1:9123"
	}
	embeddingURL := cfg.EmbeddingServerURL
	if embeddingURL == "" {
		embeddingURL = "http://127.0.0.1:8001"
	}
	return &Processor{
		dl:            dl,
		httpDL:        httpDL,
		ffmpeg:        ff,
		log:           log,
		dataDir:       cfg.DataDir,
		tempDir:       cfg.TempDir,
		videoCfg:      cfg.VideoCfg,
		scraperURL:    scraperURL,
		embeddingURL:  embeddingURL,
		registry:      registry,
		driveUploader: driveUploader,
		manifestSvc:   manifestSvc,
	}
}

// Process orchestrates the full pipeline: download, process, hash, and upload.
// It validates inputs, downloads the asset, optionally normalizes via ffmpeg,
// checks for perceptual duplicates, computes the file hash, and returns metadata.
//
// PR 7: step 4's "Maintain a single metadata.json locally + on Drive"
// block is REMOVED; replaced with a single manifest.Service.UpsertLocal
// + UpsertRemote call. The package-level shared mutex is gone.
func (p *Processor) Process(ctx context.Context, input *asset.ProcessInput) (*asset.ProcessResult, error) {
	if input == nil {
		err := fmt.Errorf("asset.ProcessInput is required")
		return &asset.ProcessResult{Status: "failed", Error: err.Error()}, err
	}

	result := &asset.ProcessResult{
		ID:     input.ID,
		Status: "failed",
	}

	if input.ID == "" {
		return result, fmt.Errorf("ProcessInput.ID is required")
	}
	if input.Name == "" {
		return result, fmt.Errorf("ProcessInput.Name is required")
	}
	if input.SourceURL == "" {
		return result, fmt.Errorf("ProcessInput.SourceURL is required")
	}
	if p.dl == nil {
		return result, fmt.Errorf("Processor.dl (YTDLP) is nil - cannot download")
	}

	tmpDir, saveDir := p.setupDirectories(input)
	finalFilename := textutil.SafeName(input.Name) + " " + input.ID + ".mp4"
	processedPath := OutputPath(saveDir, finalFilename)

	rawPath := TmpPath(tmpDir, fmt.Sprintf("raw_%s", input.ID))
	actualRawPath, err := p.downloadStep(ctx, input, rawPath)
	if err != nil {
		result.Error = fmt.Sprintf("download failed: %v", err)
		return result, err
	}

	processedPath, err = p.processStep(ctx, input, actualRawPath, processedPath)
	if err != nil {
		_ = os.Remove(actualRawPath)
		result.Error = fmt.Sprintf("process failed: %v", err)
		return result, err
	}

	duplicateID, _ := p.checkPHashDeduplication(ctx, input.ID, processedPath)
	if duplicateID != "" {
		p.log.Info("perceptual duplicate found", zap.String("id", input.ID), zap.String("duplicate_of", duplicateID))
		result.DuplicateOf = duplicateID
		if existing, err := p.registry.GetMedia(ctx, duplicateID); err == nil && existing != nil {
			result.DriveLink = existing.DriveLink
			result.DriveFileID = existing.DriveFileID
			result.DownloadLink = existing.DownloadLink
			result.Status = "duplicate"
			_ = os.Remove(actualRawPath)
			_ = os.Remove(processedPath)
			p.log.Info("Reusing Drive details from duplicate asset", zap.String("id", input.ID), zap.String("duplicate_of", duplicateID))
			return result, nil
		}
	}

	fileHash, err := p.hashStep(ctx, processedPath)
	if err != nil {
		_ = os.Remove(actualRawPath)
		_ = os.Remove(processedPath)
		result.Error = fmt.Sprintf("hash failed: %v", err)
		return result, err
	}
	result.FileHash = fileHash
	result.LocalPath = processedPath
	result.Filename = filepath.Base(processedPath)

	if p.driveUploader != nil && input.FolderID != "" {
		uploadResult, uploadErr := p.driveUploader.UploadFile(ctx, processedPath, input.FolderID, result.Filename)
		if uploadErr != nil {
			p.log.Warn("Drive upload failed (continuing with local only)",
				zap.String("id", input.ID),
				zap.Error(uploadErr),
			)
		} else {
			result.DriveLink = uploadResult.WebViewLink
			result.DriveFileID = uploadResult.FileID
			result.DownloadLink = uploadResult.DownloadLink
			p.log.Info("File uploaded to Drive",
				zap.String("id", input.ID),
				zap.String("file_id", uploadResult.FileID),
				zap.String("folder_id", input.FolderID),
			)
		}

		// PR 7 cutover: per-asset metadata write is delegated to
		// the canonical manifest.Service. The pre-cutover "marshal +
		// in-place replace" + global shared-lock dance is gone.
		if p.manifestSvc != nil {
			sourceVal := "artlist"
			if s, ok := input.Metadata["source"].(string); ok {
				sourceVal = s
			}
			extras := map[string]any{
				"term":          input.Term,
				"filename":      result.Filename,
				"duration_sec":  input.Duration,
				"created_at":    time.Now().UTC().Format(time.RFC3339),
				"download_link": result.DownloadLink,
				"clip_page_url": input.ClipPageURL,
				"source_url":    input.SourceURL,
				"duplicate_of":  result.DuplicateOf,
			}
			entry := manifest.AssetToEntry(&asset.Asset{
				ID:        input.ID,
				Name:      input.Name,
				Filename:  result.Filename,
				Source:    asset.Source(sourceVal),
				Tags:      []string{},
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
				Duration:  time.Duration(input.Duration) * time.Second,
			}, sourceVal, input.Term, extras)
			entry.LocalPath = processedPath
			entry.DriveFileID = result.DriveFileID
			entry.DriveLink = result.DriveLink
			entry.FileHash = result.FileHash

			// 1) Local manifest (atomic temp+fsync+rename).
			if dirErr := p.manifestSvc.UpsertLocal(ctx, filepath.Dir(processedPath), entry); dirErr != nil {
				p.log.Warn("manifest: local upsert failed",
					zap.String("id", input.ID), zap.Error(dirErr))
			}
			// 2) Drive manifest (per-folder locked replace).
			if remErr := p.manifestSvc.UpsertRemote(ctx, input.FolderID, entry); remErr != nil {
				p.log.Warn("manifest: remote upsert failed",
					zap.String("id", input.ID), zap.Error(remErr))
			}
		}
	}

	_ = os.Remove(actualRawPath)
	result.Status = "processed"
	return result, nil
}

// setupDirectories creates temp and save directories, returning their paths.
func (p *Processor) setupDirectories(input *asset.ProcessInput) (tmpDir, saveDir string) {
	tmpDir = filepath.Join(p.dataDir, p.tempDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		p.log.Error("failed to create temp directory", zap.String("dir", tmpDir), zap.Error(err))
		tmpDir = os.TempDir()
	}

	saveDir = input.OutputDir
	if saveDir == "" {
		saveDir = filepath.Join(p.dataDir, "mediaassets", textutil.SafeName(input.Term))
	}
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		p.log.Error("failed to create save directory", zap.String("dir", saveDir), zap.Error(err))
		saveDir = tmpDir
	}

	return tmpDir, saveDir
}
