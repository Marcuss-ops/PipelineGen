package mediaasset

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

var driveMetaMu sync.Mutex

// Processor orchestrates download via yt-dlp or HTTP, optional ffmpeg
// normalization, perceptual deduplication, file hashing, and Drive upload.
// It implements the canonical core/processor.Processor contract directly.
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
}

var _ processor.Processor = (*Processor)(nil)

// ProcessorConfig holds the constructor dependencies for Processor.
type ProcessorConfig struct {
	DataDir            string
	TempDir            string
	VideoCfg           ffmpeg.NormalizeOptions
	ScraperServerURL   string // Artlist persistent scraper server (e.g. http://localhost:9123)
	EmbeddingServerURL string // Python embedding/phash server (e.g. http://127.0.0.1:8001)
}

// NewProcessor creates a new media processor with the given dependencies.
func NewProcessor(
	dl YTDLP,
	httpDL HTTPDownloader,
	ff VideoProcessor,
	log *zap.Logger,
	cfg ProcessorConfig,
	registry artifacts.Registry,
	driveUploader *drive.Uploader,
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
	}
}

// Process orchestrates the full pipeline: download, process, hash, and upload.
// It validates inputs, downloads the asset, optionally normalizes via ffmpeg,
// checks for perceptual duplicates, computes the file hash, and returns metadata.
func (p *Processor) Process(ctx context.Context, input *processor.ProcessInput) (*processor.ProcessResult, error) {
	if input == nil {
		err := fmt.Errorf("processor.ProcessInput is required")
		return &processor.ProcessResult{Status: "failed", Error: err.Error()}, err
	}

	result := &processor.ProcessResult{
		ID:     input.ID,
		Status: "failed",
	}

	// Validate required inputs.
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

	// Setup paths.
	tmpDir, saveDir := p.setupDirectories(input)
	finalFilename := textutil.SafeName(input.Name) + " " + input.ID + ".mp4"
	processedPath := OutputPath(saveDir, finalFilename)

	// Step 1: Download (use path without extension so yt-dlp can add %(ext)s correctly).
	rawPath := TmpPath(tmpDir, fmt.Sprintf("raw_%s", input.ID))
	actualRawPath, err := p.downloadStep(ctx, input, rawPath)
	if err != nil {
		result.Error = fmt.Sprintf("download failed: %v", err)
		return result, err
	}

	// Step 2: Process/Normalize.
	processedPath, err = p.processStep(ctx, input, actualRawPath, processedPath)
	if err != nil {
		_ = os.Remove(actualRawPath)
		result.Error = fmt.Sprintf("process failed: %v", err)
		return result, err
	}

	// Perceptual deduplication.
	duplicateID, _ := p.checkPHashDeduplication(ctx, input.ID, processedPath)
	if duplicateID != "" {
		p.log.Info("perceptual duplicate found", zap.String("id", input.ID), zap.String("duplicate_of", duplicateID))
		result.DuplicateOf = duplicateID
		if existing, err := p.registry.GetMedia(ctx, duplicateID); err == nil && existing != nil {
			result.DriveLink = existing.DriveLink
			result.DriveFileID = existing.DriveFileID
			result.DownloadLink = existing.DownloadLink
			result.Status = "duplicate"

			// Clean up local files to avoid duplicate storage.
			_ = os.Remove(actualRawPath)
			_ = os.Remove(processedPath)

			p.log.Info("Reusing Drive details from duplicate asset", zap.String("id", input.ID), zap.String("duplicate_of", duplicateID))
			return result, nil
		}
	}

	// Step 3: Hash.
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

	// Step 4: Upload to Google Drive (if configured).
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

		sourceVal := "artlist"
		if s, ok := input.Metadata["source"].(string); ok {
			sourceVal = s
		}

		metaData := map[string]any{
			"clip_id":       input.ID,
			"name":          input.Name,
			"source":        sourceVal,
			"term":          input.Term,
			"filename":      result.Filename,
			"file_hash":     result.FileHash,
			"duration_sec":  input.Duration,
			"created_at":    time.Now().UTC().Format(time.RFC3339),
			"drive_file_id": result.DriveFileID,
			"drive_link":    result.DriveLink,
			"download_link": result.DownloadLink,
			"clip_page_url": input.ClipPageURL,
			"source_url":    input.SourceURL,
			"duplicate_of":  result.DuplicateOf,
		}
		// Merge any extra metadata provided in the input.
		for k, v := range input.Metadata {
			if _, exists := metaData[k]; !exists {
				metaData[k] = v
			}
		}

		// Maintain a single metadata.json locally under lock to avoid concurrency races.
		driveMetaMu.Lock()
		localMetaPath := filepath.Join(filepath.Dir(processedPath), "metadata.json")
		var localExisting []map[string]any
		if data, err := os.ReadFile(localMetaPath); err == nil {
			_ = json.Unmarshal(data, &localExisting)
		}
		foundLocal := false
		for i, entry := range localExisting {
			if id, ok := entry["clip_id"].(string); ok && id == input.ID {
				localExisting[i] = metaData
				foundLocal = true
				break
			}
		}
		if !foundLocal {
			localExisting = append(localExisting, metaData)
		}
		if data, err := json.MarshalIndent(localExisting, "", "  "); err == nil {
			if writeErr := os.WriteFile(localMetaPath, data, 0o644); writeErr != nil {
				p.log.Warn("failed to write metadata JSON locally", zap.String("id", input.ID), zap.Error(writeErr))
			}
		}

		// Maintain and upload a single cumulative metadata.json to Google Drive.
		p.updateCumulativeMetadataJSON(ctx, input.FolderID, input.ID, metaData)
		driveMetaMu.Unlock()
	}

	// Cleanup raw file after processing.
	_ = os.Remove(actualRawPath)

	result.Status = "processed"
	return result, nil
}

// setupDirectories creates temp and save directories, returning their paths.
func (p *Processor) setupDirectories(input *processor.ProcessInput) (tmpDir, saveDir string) {
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

// updateCumulativeMetadataJSON maintains a single metadata.json per folder on Google Drive.
func (p *Processor) updateCumulativeMetadataJSON(ctx context.Context, folderID, clipID string, newEntry map[string]any) {
	const metaFilename = "metadata.json"

	var existing []map[string]any
	query := fmt.Sprintf("'%s' in parents and trashed = false and name = '%s'", folderID, metaFilename)
	list, err := p.driveUploader.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		p.log.Warn("failed to list metadata.json", zap.Error(err))
	} else if len(list.Files) > 0 {
		existingFileID := list.Files[0].Id
		body, _, dlErr := p.driveUploader.DownloadFile(ctx, existingFileID)
		if dlErr == nil && body != nil {
			defer body.Close()
			var raw []map[string]any
			if decErr := json.NewDecoder(body).Decode(&raw); decErr == nil {
				existing = raw
			}
		}
		if err := p.driveUploader.TrashFile(ctx, existingFileID); err != nil {
			p.log.Warn("failed to trash old metadata.json", zap.Error(err))
		}
	}

	found := false
	for i, entry := range existing {
		if id, ok := entry["clip_id"].(string); ok && id == clipID {
			existing[i] = newEntry
			found = true
			break
		}
	}
	if !found {
		existing = append(existing, newEntry)
	}

	jsonBytes, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		p.log.Warn("failed to marshal cumulative metadata json", zap.Error(err))
		return
	}
	metaTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("meta_%s_%d.json", clipID, time.Now().UnixNano()))
	if err := os.WriteFile(metaTempPath, jsonBytes, 0o644); err != nil {
		p.log.Warn("failed to write metadata json temp file", zap.Error(err))
		return
	}
	defer os.Remove(metaTempPath)

	if _, err := p.driveUploader.UploadFile(ctx, metaTempPath, folderID, metaFilename); err != nil {
		p.log.Warn("failed to upload metadata.json to Drive", zap.Error(err))
	} else {
		p.log.Info("uploaded cumulative metadata.json to Drive", zap.Int("entries", len(existing)))
	}
}
