package fullimages

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/media/models"
)

const (
	// imageGenerationTimeout limits how long a single image can take.
	imageGenerationTimeout = 3 * time.Minute

	// Default image resolution used by both generation tiers.
	imageWidth  = 1024
	imageHeight = 1024
)

// buildSectionPrompts creates candidate image prompts from section content.
// The first non-empty prompt is the primary; remaining are fallbacks.
func buildSectionPrompts(sec Section, topic string) []string {
	var prompts []string

	// 1. Use section title as the primary prompt subject
	if sec.Title != "" {
		prompts = append(prompts,
			fmt.Sprintf("cinematic documentary image of %s", sec.Title),
			fmt.Sprintf("professional stock photo of %s", sec.Title),
		)
	}

	// 2. Add topic context if present and different from title
	if topic != "" && !strings.EqualFold(topic, sec.Title) {
		prompts = append(prompts,
			fmt.Sprintf("cinematic documentary image of %s, %s theme", sec.Title, topic),
			fmt.Sprintf("high quality photography of %s related to %s", sec.Title, topic),
		)
	}

	// 3. Use the first ~100 chars of text as a contextual prompt
	if text := strings.TrimSpace(sec.Text); text != "" {
		if len(text) > 100 {
			text = text[:100]
		}
		prompts = append(prompts, text)
	}

	// 4. Pure topic as last resort
	if topic != "" {
		prompts = append(prompts, fmt.Sprintf("documentary image about %s", topic))
	}

	return prompts
}

// pickBestPrompt returns the first non-empty prompt, or constructs a default.
func pickBestPrompt(prompts []string, subject, topic string) string {
	for _, p := range prompts {
		if strings.TrimSpace(p) != "" {
			return p
		}
	}
	if subject != "" {
		return fmt.Sprintf("A cinematic image of %s", subject)
	}
	return fmt.Sprintf("A documentary image about %s", topic)
}

// resolveDisplayURL extracts the display URL from an ImageAsset.
func resolveDisplayURL(asset *models.ImageAsset) string {
	if asset == nil {
		return ""
	}
	if asset.PathRel != "" {
		return "/assets/" + asset.PathRel
	}
	if asset.SourceURL != "" {
		return asset.SourceURL
	}
	return ""
}

// resolveImagePath searches the images directory for a file whose name starts
// with the given hash. This is needed because the ingest pipeline may not
// reliably populate PathRel on the returned asset.
func resolveImagePath(imagesDir, hash string) string {
	if imagesDir == "" || hash == "" {
		return ""
	}
	entries, err := os.ReadDir(imagesDir)
	if err != nil {
		return ""
	}
	// Walk one level deep — images are stored in {imagesDir}/{slug}/{hash}.ext
	for _, dir := range entries {
		if !dir.IsDir() {
			continue
		}
		subEntries, err := os.ReadDir(filepath.Join(imagesDir, dir.Name()))
		if err != nil {
			continue
		}
		for _, f := range subEntries {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			// Strip extension and compare
			if ext := filepath.Ext(name); ext != "" {
				name = name[:len(name)-len(ext)]
			}
			if name == hash {
				return filepath.Join(imagesDir, dir.Name(), f.Name())
			}
		}
	}
	return ""
}
