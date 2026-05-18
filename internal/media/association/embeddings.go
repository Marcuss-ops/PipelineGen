package association

import (
	"context"
	"encoding/json"
	"fmt"
	"go.uber.org/zap"
	"os/exec"
	"path/filepath"
)

// GenerateEmbedding calls the Python script to generate a semantic embedding for the given text.
func (s *Service) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}

	scriptPath := filepath.Join(s.scriptsDir, "generate_embedding.py")

	cmd := exec.CommandContext(ctx, "python3", scriptPath, "--text", text)
	output, err := cmd.CombinedOutput()
	if err != nil {
		zap.L().Error("Embedding script failed", zap.Error(err), zap.String("output", string(output)))
		return nil, fmt.Errorf("embedding generation failed: %w", err)
	}

	var embedding []float32
	if err := json.Unmarshal(output, &embedding); err != nil {
		return nil, fmt.Errorf("failed to parse embedding JSON: %w (output: %s)", err, string(output))
	}

	return embedding, nil
}
