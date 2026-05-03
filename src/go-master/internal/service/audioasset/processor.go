package audioasset

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/service/assetdestination"
	"velox/go-master/pkg/hashutil"
	"velox/go-master/pkg/media/audio"
)

type Processor struct {
	pythonScriptsDir string
	driveClient      *driveapi.Service
	assetDestResolver *assetdestination.Resolver
	log               *zap.Logger
}

func NewProcessor(
	pythonScriptsDir string,
	driveClient *driveapi.Service,
	assetDestResolver *assetdestination.Resolver,
	log *zap.Logger,
) *Processor {
	return &Processor{
		pythonScriptsDir: pythonScriptsDir,
		driveClient:      driveClient,
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
		"--language", input.Language,
		"--output", outputPath,
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
	if input.Destination != nil && p.driveClient != nil {
		resolved, err := p.assetDestResolver.Resolve(ctx, input.Destination)
		if err != nil {
			p.log.Warn("destination resolution failed", zap.Error(err))
		} else if resolved.FolderID != "" {
			driveLink, err := p.uploadToDrive(ctx, result.LocalPath, resolved.FolderID, filepath.Base(result.LocalPath))
			if err != nil {
				p.log.Warn("drive upload failed", zap.Error(err))
			} else {
				result.DriveLink = driveLink
				result.Status = "uploaded"
			}
		}
	}

	if result.Status == "" {
		result.Status = "processed"
	}

	return result, nil
}

func (p *Processor) uploadToDrive(ctx context.Context, filePath, folderID, filename string) (string, error) {
	if p.driveClient == nil {
		return "", fmt.Errorf("drive client not configured")
	}

	file := &driveapi.File{
		Name:    filename,
		Parents: []string{folderID},
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	start := time.Now()
	created, err := p.driveClient.Files.Create(file).
		Media(f).
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("drive upload failed: %w", err)
	}

	p.log.Info("audio file uploaded to drive",
		zap.String("file_id", created.Id),
		zap.Duration("duration", time.Since(start)),
	)

	return created.WebViewLink, nil
}
