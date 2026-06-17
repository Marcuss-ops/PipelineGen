package voiceover

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/pkg/hashutil"
	"velox/go-master/pkg/textutil"
)

func (s *Service) buildFilename(req *BatchRequest, language, textHash string) string {
	slug := textutil.SlugifyWithMax(req.Text, 30)
	template := req.FilenameTemplate
	if template == "" {
		template = "{slug}_{lang}.mp3"
	}

	filename := strings.ReplaceAll(template, "{slug}", slug)
	filename = strings.ReplaceAll(filename, "{lang}", language)
	filename = strings.ReplaceAll(filename, "{hash}", textHash[:8])
	filename = strings.ReplaceAll(filename, "{time}", time.Now().Format("150405"))

	return filename
}

func buildVoiceoverID(textHash, language, folderID string) string {
	data := fmt.Sprintf("%s:%s:%s", textHash, language, folderID)
	return "vo_" + hashutil.SHA256Bytes([]byte(data))[:16]
}

func sanitizeFilename(outputDir, filename string) (string, error) {
	if filepath.Ext(filename) == "" {
		filename += ".mp3"
	}

	// Prevent path traversal: reject if filename contains path separators
	if strings.ContainsAny(filename, "/\\") {
		return "", fmt.Errorf("invalid filename: path traversal detected")
	}

	// Sanitize the filename portion
	cleanName := textutil.SanitizeFilename(filename)
	cleanName = filepath.Base(cleanName)

	finalPath := filepath.Join(outputDir, cleanName)

	// If outputDir is set, verify the final path is inside outputDir
	if outputDir != "" {
		if !strings.HasPrefix(finalPath, outputDir+string(filepath.Separator)) && finalPath != outputDir {
			return "", fmt.Errorf("invalid filename: path traversal detected")
		}
	}

	return finalPath, nil
}
