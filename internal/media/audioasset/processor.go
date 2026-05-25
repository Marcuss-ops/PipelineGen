package audioasset

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"go.uber.org/zap"

	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/pkg/hashutil"
	"velox/go-master/internal/pkg/media/audio"
	"velox/go-master/internal/upload/drive"
)

type Processor struct {
	pythonScriptsDir  string
	driveUploader     *drive.Uploader
	assetDestResolver destination.Resolver
	log               *zap.Logger
}

func NewProcessor(
	pythonScriptsDir string,
	driveUploader *drive.Uploader,
	assetDestResolver destination.Resolver,
	log *zap.Logger,
) *Processor {
	return &Processor{
		pythonScriptsDir:  pythonScriptsDir,
		driveUploader:     driveUploader,
		assetDestResolver: assetDestResolver,
		log:               log,
	}
}

func (p *Processor) Generate(ctx context.Context, input *AudioInput) (*AudioResult, error) {
	result := &AudioResult{}

	// 1. Generate TTS via Python script
	outputPath := filepath.Join(input.OutputDir, input.Filename)

	scriptPath := filepath.Join(p.pythonScriptsDir, "tts_edge.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("tts script not found: %s", scriptPath)
	}

	args := []string{
		scriptPath,
		"--text", input.Text,
		"--lang", input.Language,
		"--out", outputPath,
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("TTS generation failed: %w, output: %s", err, string(output))
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("TTS output file not found: %s", outputPath)
	}

	result.LocalPath = outputPath
	result.Status = "generated"

	p.log.Info("TTS generated", zap.String("path", outputPath))

	// 2. Optional silence removal
	if input.RemoveSilence {
		cleanedPath := filepath.Join(input.OutputDir, "cleaned_"+input.Filename)
		err := audio.RemoveSilence(ctx, "", outputPath, cleanedPath)
		if err != nil {
			p.log.Warn("silence removal failed", zap.Error(err))
		} else {
			result.CleanedPath = cleanedPath
			result.LocalPath = cleanedPath
			result.Status = "cleaned"
		}
	}

	// 3. Compute hash
	if result.LocalPath != "" {
		hash, err := hashutil.HashFile(result.LocalPath, md5.New())
		if err != nil {
			p.log.Warn("hash computation failed", zap.Error(err))
		} else {
			result.FileHash = hash
		}
	}

	// 4. Upload to Drive if destination is provided
	if input.Destination != nil && p.driveUploader != nil {
		resolved, err := p.assetDestResolver.Resolve(ctx, input.Destination)
		if err != nil {
			p.log.Warn("destination resolution failed", zap.Error(err))
		} else if resolved.FolderID != "" {
			driveLink, fileID, err := p.uploadToDrive(ctx, result.LocalPath, resolved.FolderID, filepath.Base(result.LocalPath))
			if err != nil {
				p.log.Warn("drive upload failed", zap.Error(err))
			} else {
				result.DriveLink = driveLink
				result.DriveFileID = fileID
				result.Status = "uploaded"
			}
		}
	}

	if result.Status == "" {
		result.Status = "processed"
	}

	return result, nil
}

func (p *Processor) uploadToDrive(ctx context.Context, filePath, folderID, filename string) (string, string, error) {
	if p.driveUploader == nil {
		return "", "", fmt.Errorf("drive uploader not configured")
	}

	result, err := p.driveUploader.UploadFile(ctx, filePath, folderID, filename)
	if err != nil {
		return "", "", fmt.Errorf("drive upload failed: %w", err)
	}

	p.log.Info("audio file uploaded to drive",
		zap.String("file_id", result.FileID),
	)

	return result.WebViewLink, result.FileID, nil
}
