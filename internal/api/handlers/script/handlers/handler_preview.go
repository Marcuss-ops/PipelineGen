package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"velox/go-master/internal/api/handlers/script"
)

func (h *ScriptDocsHandler) savePreview(title, content string) (string, error) {
	dir := h.dataDir
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	scriptsDir := filepath.Join(dir, "scripts")
	// Ensure the scripts directory exists
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create scripts directory: %w", err)
	}

	path := script.BuildPreviewPath(scriptsDir, title)
	if err := script.WritePreview(path, title, content); err != nil {
		return "", err
	}
	return path, nil
}

func narrativeOnly(content string) string {
	marker := "## 🎙️ Narrator"
	if idx := strings.Index(content, marker); idx != -1 {
		part := content[idx+len(marker):]
		if nextIdx := strings.Index(part, "## 🎬 Timeline"); nextIdx != -1 {
			return strings.TrimSpace(part[:nextIdx])
		}
		return strings.TrimSpace(part)
	}
	return content
}
