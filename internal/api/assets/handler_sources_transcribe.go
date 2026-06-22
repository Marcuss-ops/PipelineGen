package assets

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	executil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// transcriptResult holds the parsed JSON output from transcribe_detect_lang.py.
type transcriptResult struct {
	Language             string  `json:"language"`
	Probability          float64 `json:"probability"`
	TranscriptFull       string  `json:"transcript_full"`
	TranscriptPreview    string  `json:"transcript_preview"`
	TranscriptLength     int     `json:"transcript_length"`
	NumSegments          int     `json:"num_segments"`
	TranscriptionTimeSec float64 `json:"transcription_time_seconds"`
	Error                string  `json:"error"`
}

// transcribeAudio runs Whisper transcription on a local video file and returns
// the transcript text + detected language. Uses the tiny model for speed (~1-2s
// for short clips). Non-fatal: logs warnings on failure but never blocks the
// registration flow.
func (h *Handler) transcribeAudio(ctx context.Context, localPath string, log *zap.Logger) (transcript string, language string) {
	if localPath == "" {
		return "", ""
	}

	// Quick sanity: skip non-video files or files that don't exist
	if _, err := os.Stat(localPath); err != nil {
		log.Debug("transcribe: file not found, skipping", zap.String("path", localPath), zap.Error(err))
		return "", ""
	}

	pythonBin := "python3"
	scriptPath := filepath.Join(h.cfg.Paths.PythonScriptsDir, "tools", "transcribe_detect_lang.py")

	// Check that the script exists before trying
	if _, err := os.Stat(scriptPath); err != nil {
		log.Debug("transcribe: script not found, skipping", zap.String("path", scriptPath), zap.Error(err))
		return "", ""
	}

	// Build command: python3 scripts/tools/transcribe_detect_lang.py --transcribe --model tiny --json-only <file>
	execResult, err := executil.RunSimple(ctx, pythonBin, scriptPath,
		"--transcribe", "--model", "tiny", "--json-only", localPath,
	)
	if err != nil {
		log.Warn("transcription failed for clip (non-fatal)",
			zap.String("path", localPath),
			zap.Error(err),
		)
		return "", ""
	}

	var tsResult transcriptResult
	if err := json.Unmarshal([]byte(execResult.Output), &tsResult); err != nil {
		log.Warn("failed to parse transcription JSON",
			zap.String("path", localPath),
			zap.Error(err),
		)
		return "", ""
	}

	if tsResult.Error != "" {
		log.Warn("transcription error from whisper",
			zap.String("path", localPath),
			zap.String("error", tsResult.Error),
		)
		return "", ""
	}

	transcript = strings.TrimSpace(tsResult.TranscriptFull)
	language = strings.TrimSpace(tsResult.Language)

	log.Info("clip transcribed",
		zap.String("path", localPath),
		zap.String("language", language),
		zap.Float64("probability", tsResult.Probability),
		zap.Int("transcript_len", tsResult.TranscriptLength),
		zap.Float64("time_sec", tsResult.TranscriptionTimeSec),
	)

	return transcript, language
}

// saveTranscriptAndStage writes the transcript text to:
//  1. A .txt file next to the video (for the Python embedding server's /index_transcript)
//  2. The youtube-clips staging directory (for the bulk upload embedding path)
//
// Both paths are needed because different indexing code paths look in different places.
// Non-fatal: logs warnings on failure.
func (h *Handler) saveTranscriptAndStage(localPath string, transcript string, group string, log *zap.Logger) {
	if transcript == "" {
		return
	}

	// 1. Write .txt next to the video file (e.g. /tmp/clip.mp4 → /tmp/clip.txt)
	baseNoExt := strings.TrimSuffix(localPath, filepath.Ext(localPath))
	txtPath := baseNoExt + ".txt"
	if err := os.WriteFile(txtPath, []byte(transcript), 0644); err != nil {
		log.Warn("failed to write transcript .txt next to video",
			zap.String("txt_path", txtPath),
			zap.Error(err),
		)
	} else {
		log.Debug("transcript .txt saved next to video",
			zap.String("txt_path", txtPath),
		)
	}

	// 2. Stage in youtube-clips for the embedding server's /index_transcript endpoint
	// The indexer looks for {base}.txt in the youtube-clips directory.
	stageRoot := h.cfg.Storage.YoutubeClipsPath()
	if stageRoot == "" {
		stageRoot = filepath.Join(h.cfg.Storage.DataDir, "youtube-clips")
	}

	// Use group as subdirectory to avoid naming collisions
	subBucket := strings.TrimSpace(group)
	if subBucket == "" || subBucket == "." {
		subBucket = "_manual"
	}
	// Sanitise: only alphanumeric, dash, underscore
	subBucket = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, subBucket)

	stageDir := filepath.Join(stageRoot, subBucket)
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		log.Warn("failed to create transcript staging directory",
			zap.String("dir", stageDir),
			zap.Error(err),
		)
		return
	}

	stageFile := filepath.Base(baseNoExt) + ".txt"
	stagePath := filepath.Join(stageDir, stageFile)
	if err := os.WriteFile(stagePath, []byte(transcript), 0644); err != nil {
		log.Warn("failed to stage transcript for embedding server",
			zap.String("stage_path", stagePath),
			zap.Error(err),
		)
	} else {
		log.Debug("transcript staged for embedding server",
			zap.String("stage_path", stagePath),
		)
	}
}
