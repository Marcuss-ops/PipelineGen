package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

type VoiceoverResult struct {
	OK    bool   `json:"ok"`
	Voice string `json:"voice"`
	Path  string `json:"path"`
	Error string `json:"error,omitempty"`
}

type Service struct {
	pythonScriptsDir string
	outputDir        string
	log              *zap.Logger
}

func NewService(pythonScriptsDir, outputDir string, log *zap.Logger) *Service {
	return &Service{
		pythonScriptsDir: pythonScriptsDir,
		outputDir:        outputDir,
		log:              log,
	}
}

func (s *Service) Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("text is empty")
	}

	outputPath := filepath.Join(s.outputDir, filename)
	scriptPath := filepath.Join(s.pythonScriptsDir, "tts_edge.py")

	s.log.Info("Generating voiceover", 
		zap.String("language", language), 
		zap.String("output", outputPath))

	cmd := exec.CommandContext(ctx, "python3", scriptPath, 
		"--text", text, 
		"--lang", language, 
		"--out", outputPath)

	out, err := cmd.CombinedOutput()
	if err != nil {
		s.log.Error("Voiceover generation failed", 
			zap.Error(err), 
			zap.String("output", string(out)))
		return nil, fmt.Errorf("voiceover script failed: %w (output: %s)", err, string(out))
	}

	var result VoiceoverResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse voiceover result: %w", err)
	}

	if !result.OK {
		return nil, fmt.Errorf("voiceover error: %s", result.Error)
	}

	return &result, nil
}
