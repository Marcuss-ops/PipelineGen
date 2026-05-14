package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"velox/go-master/pkg/executil"

	"go.uber.org/zap"
)

type GenerateResult struct {
	OK    bool   `json:"ok"`
	Voice string `json:"voice,omitempty"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Service) generateAudio(ctx context.Context, text, language, filename string) (string, *GenerateResult, error) {
	outputPath, err := s.sanitizeFilename(s.outputDir, filename)
	if err != nil {
		return "", nil, err
	}

	scriptPath := filepath.Join(s.pythonScriptsDir, "tts_edge.py")
	s.log.Info("Running TTS script", zap.String("script_path", scriptPath))

	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		s.log.Error("Script does not exist", zap.String("path", scriptPath))
	}

	result, err := executil.Run(ctx, "python3", []string{
		scriptPath,
		"--text", text,
		"--lang", language,
		"--out", outputPath,
	}, executil.Options{
		Timeout:        10 * time.Minute,
		CombinedOutput: true,
	})

	if err != nil {
		return "", nil, fmt.Errorf("voiceover script failed: %w", err)
	}

	var genResult GenerateResult
	if err := json.Unmarshal([]byte(result.Output), &genResult); err != nil {
		return "", nil, fmt.Errorf("failed to parse tts_edge output: %w", err)
	}

	if !genResult.OK {
		return "", nil, fmt.Errorf("voiceover error: %s", genResult.Error)
	}

	return genResult.Path, &genResult, nil
}
