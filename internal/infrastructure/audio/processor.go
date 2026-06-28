package audioasset

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	audio "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
)

type Processor struct {
	pythonScriptsDir  string
	driveUploader     *drive.Uploader
	assetDestResolver asset.Resolver
	log               *zap.Logger
}

func NewProcessor(
	pythonScriptsDir string,
	driveUploader *drive.Uploader,
	assetDestResolver asset.Resolver,
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

	// 1. Generate TTS via Python script.
	// Defense-in-depth: validate filename against path traversal.
	// filepath.Base strips any directory components; if the result
	// differs from the input, the filename contained path separators.
	safeName := filepath.Base(input.Filename)
	if safeName != input.Filename {
		return nil, fmt.Errorf("invalid filename: path traversal detected")
	}
	if filepath.Ext(safeName) == "" {
		safeName += ".mp3"
	}
	outputPath := filepath.Join(input.OutputDir, safeName)

	scriptPath := filepath.Join(p.pythonScriptsDir, "bridges", "tts_edge.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("tts script not found: %s", scriptPath)
	}

	args := []string{
		scriptPath,
		"--lang", input.Language,
		"--out", outputPath,
	}

	// --voice passthrough: override the auto-detected voice profile.
	if input.Voice != "" {
		args = append(args, "--voice", input.Voice)
	}

	// Text delivery: use stdin when requested or when text is long
	// (> 32 KB — avoids OS argument-length limits and process-table
	// visibility). stdin also prevents shell interpolation of
	// special characters.
	useStdin := input.UseStdin || len(input.Text) > 32*1024
	if !useStdin {
		args = append(args, "--text", input.Text)
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	if useStdin {
		cmd.Stdin = bytes.NewReader([]byte(input.Text))
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("TTS generation failed: %w, output: %s", err, string(output))
	}

	// Parse the JSON response from tts_edge.py to extract the real
	// voice name (e.g. "en-US-RogerNeural") and detect failures
	// the script reports as JSON but exits 0 for (empty-file).
	type ttsResponse struct {
		OK    bool   `json:"ok"`
		Voice string `json:"voice"`
		Error string `json:"error"`
		Path  string `json:"path"`
	}
	var ttsOut ttsResponse
	if jsonErr := json.Unmarshal(bytes.TrimSpace(output), &ttsOut); jsonErr != nil {
		p.log.Warn("TTS script returned non-JSON output",
			zap.String("output", string(bytes.TrimSpace(output))),
			zap.Error(jsonErr))
	} else {
		result.Voice = ttsOut.Voice
		if !ttsOut.OK {
			return nil, fmt.Errorf("TTS generation failed: %s", ttsOut.Error)
		}
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("TTS output file not found: %s", outputPath)
	}

	result.LocalPath = outputPath
	result.Status = "generated"

	p.log.Info("TTS generated", zap.String("path", outputPath))

	// 2. Optional silence removal

	// PR-VO-A1 deleted the historical MP3-as-JSON re-read here; the canonical
	// voice is captured from tts_edge.py stdout in section 1 above.
	if input.RemoveSilence {
		cleanedPath := filepath.Join(input.OutputDir, "cleaned_"+safeName)
		err := audio.RemoveSilence(ctx, "", outputPath, cleanedPath)
		if err != nil {
			p.log.Warn("silence removal failed", zap.Error(err))
		} else {
			result.CleanedPath = cleanedPath
			result.LocalPath = cleanedPath
			result.Status = "cleaned"
		}
	}

	// 4. Compute hash
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
