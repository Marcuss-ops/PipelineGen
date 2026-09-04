package generation

import (
	"fmt"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func buildGeneratedImageSlug(promptUsed string) string {
	slug := textutil.Slugify(promptUsed)
	if len(slug) > 50 {
		slug = slug[:50]
	}
	return slug
}

func buildGeneratedImageFilename(promptUsed, format string) string {
	filename := promptUsed
	if len(filename) > 80 {
		filename = filename[:80]
	}
	return textutil.Slugify(filename) + "." + format
}

func buildGeneratedImageDescription(promptUsed string) string {
	return fmt.Sprintf("AI generated image via Chrome/Playwright for prompt: %s", promptUsed)
}

func resolveGeneratedImageSource(provider string) string {
	if provider == "" {
		return "google-slides"
	}
	return provider
}
