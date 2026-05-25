package voiceover

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"velox/go-master/internal/pkg/hashutil"
)

var slugReplacer = regexp.MustCompile(`[^a-z0-9]+`)

func toSlug(text string, maxLen int) string {
	text = strings.ToLower(text)
	text = strings.ReplaceAll(text, " ", "-")
	text = slugReplacer.ReplaceAllString(text, "-")
	text = strings.Trim(text, "-")
	if len(text) > maxLen {
		text = text[:maxLen]
	}
	text = strings.TrimRight(text, "-")
	return text
}

func (s *Service) buildFilename(req *BatchRequest, language, textHash string) string {
	slug := toSlug(req.Text, 30)
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

	// Get clean base name
	cleanName := filepath.Base(filename)
	if cleanName != filename {
		return "", fmt.Errorf("invalid filename: path traversal detected")
	}

	finalPath := filepath.Join(outputDir, cleanName)

	// If outputDir is set, verify the final path is inside outputDir
	if outputDir != "" {
		if !strings.HasPrefix(finalPath, outputDir+string(filepath.Separator)) && finalPath != outputDir {
			return "", fmt.Errorf("invalid filename: path traversal detected")
		}
	}

	return finalPath, nil
}

func textToHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", h)
}
