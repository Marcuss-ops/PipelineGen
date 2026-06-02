package handlers

import (
	"context"
	"sync"

	"velox/go-master/internal/ml/ollama"
)

// BuildMetadataLanguages builds the list of languages for metadata generation.
// Always includes English first, then adds base language if different, then additional languages.
func BuildMetadataLanguages(baseLanguage string, additionalLanguages []string) []string {
	languages := []string{"en"} // Always include English for YouTube
	languageSet := map[string]bool{"en": true}

	// Add base language if not English
	if baseLanguage != "" && baseLanguage != "en" && !languageSet[baseLanguage] {
		languages = append(languages, baseLanguage)
		languageSet[baseLanguage] = true
	}

	// Add additional requested languages
	for _, lang := range additionalLanguages {
		if !languageSet[lang] {
			languages = append(languages, lang)
			languageSet[lang] = true
		}
	}

	return languages
}

// GenerateVideoMetadata generates YouTube metadata (title, description, tags) for multiple languages in parallel.
// Always generates English metadata first, then translates to other languages.
func GenerateVideoMetadata(ctx context.Context, generator *ollama.Generator, title string, languages []string) []VideoMetadata {
	var mu sync.Mutex
	metadata := make([]VideoMetadata, 0, len(languages))
	var wg sync.WaitGroup

	for _, lang := range languages {
		wg.Add(1)
		go func(lang string) {
			defer wg.Done()

			meta := VideoMetadata{Language: lang}

			// Translate title to target language
			titleTranslated, _ := generator.TranslateText(ctx, title, lang)
			if titleTranslated != "" {
				meta.Title = titleTranslated
			} else {
				meta.Title = title // fallback to original
			}

			// Generate description and tags in English, or translate if not English
			if lang == "en" {
				if desc, tags, err := generator.GenerateVideoMetadata(ctx, title); err == nil {
					meta.Description = desc
					meta.Tags = tags
				}
			} else {
				// Translate English metadata to target language
				if desc, tags, err := generator.GenerateVideoMetadata(ctx, title); err == nil {
					descTranslated, _ := generator.TranslateText(ctx, desc, lang)
					if descTranslated != "" {
						meta.Description = descTranslated
					} else {
						meta.Description = desc
					}
					// Translate tags
					var translatedTags []string
					for _, tag := range tags {
						if t, err := generator.TranslateText(ctx, tag, lang); err == nil && t != "" {
							translatedTags = append(translatedTags, t)
						} else {
							translatedTags = append(translatedTags, tag) // fallback to original
						}
					}
					meta.Tags = translatedTags
				}
			}

			mu.Lock()
			metadata = append(metadata, meta)
			mu.Unlock()
		}(lang)
	}
	wg.Wait()

	return metadata
}