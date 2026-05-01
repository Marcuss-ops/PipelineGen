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

// sanitizeFilename ensures the filename is safe and within the output directory
func sanitizeFilename(outputDir, filename string) (string, error) {
	// Only keep the base filename to prevent path traversal
	cleanName := filepath.Base(filename)

	// Validate .mp3 extension
	if !strings.HasSuffix(cleanName, ".mp3") {
		cleanName += ".mp3"
	}

	// Additional slugify: remove any path separators that might have passed through
	cleanName = strings.ReplaceAll(cleanName, "/", "")
	cleanName = strings.ReplaceAll(cleanName, "\\", "")

	outputPath := filepath.Join(outputDir, cleanName)

	// Verify the result is within outputDir
	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return "", err
	}
	absOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absOutputDir, absOutputPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid filename: path traversal detected")
	}

	return outputPath, nil
}

func (s *Service) Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("text is empty")
	}

	outputPath, err := sanitizeFilename(s.outputDir, filename)
	if err != nil {
		return nil, fmt.Errorf("invalid filename: %w", err)
	}
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
