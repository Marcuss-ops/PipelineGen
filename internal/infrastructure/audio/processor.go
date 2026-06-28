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

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	audio "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
)

// Processor — PR-VO-B1 (June 2026): the previous direct Drive
// coupling has been removed. Processor writes ONLY to the local
// filesystem; the Drive upload belongs to Lifecycle (which already
// owns Step 2 of ProcessAsset in internal/application/assets/lifecycle/
// service.go). voiceover.go now calls NewProcessor with only
// (pythonScriptsDir, log) and the audioasset package no longer
// imports infrastructure/drive or domain/asset.
type Processor struct {
	pythonScriptsDir string
	log              *zap.Logger
}

// NewProcessor constructs a Processor. The previous driveUploader and
// assetDestResolver arguments are intentionally gone — Processor
// handles local FS only (TTS generation + optional FFmpeg silence
// removal + MD5 hash). Drive upload is owned by Lifecycle.
func NewProcessor(
	pythonScriptsDir string,
	log *zap.Logger,
) *Processor {
	return &Processor{
		pythonScriptsDir: pythonScriptsDir,
		log:              log,
	}
}

// Generate runs TTS over the configured Python bridge + (optionally)
// FFmpeg silence removal. Local FS only; no Drive interaction.
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

	// 3. Compute hash
	if result.LocalPath != "" {
		hash, err := hashutil.HashFile(result.LocalPath, md5.New())
		if err != nil {
			p.log.Warn("hash computation failed", zap.Error(err))
		} else {
			result.FileHash = hash
		}
	}

	// 4. Drive upload is the Lifecycle's responsibility, not the
	// Processor's. PR-VO-B1 (June 2026) removed the inline upload
	// code path entirely. voiceover.processLanguage hands the
	// local file off to lifecycle.ProcessAsset (Step 2 in
	// internal/application/assets/lifecycle/service.go performs the
	// Drive upload with the folder ID + filename). AudioResult
	// keeps DriveLink/DriveFileID fields as zero-value (Lifecycle
	// fills them after Generate returns).
	//
	// result.Status is set to "generated" at line above and
	// optionally overridden to "cleaned" by the silence-removal
	// block — it's never empty here. The previous defensive
	// fallback `if result.Status == "" { Status = "processed" }`
	// was deleted in PR-VO-B1 because it is unreachable after the
	// Drive upload removal (the upload path was the only place that
	// could reset Status without restoring it).
	return result, nil
}
