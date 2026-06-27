package voiceover

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
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

// SanitizeBasename validates and sanitizes a voiceover basename.
// Does NOT add an extension — callers should append .mp3 themselves.
// Rejects path separators (path traversal).
func SanitizeBasename(name string) (string, error) {
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("invalid filename: path traversal detected")
	}
	return filepath.Base(textutil.SanitizeFilename(name)), nil
}

// SanitizeFilename validates a filename against path traversal, adds
// .mp3 if missing, and returns a safe output path rooted at outputDir.
func SanitizeFilename(outputDir, filename string) (string, error) {
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
