package script

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	imgservice "velox/go-master/internal/media/images"
	"velox/go-master/internal/media/models"
)

type imagePlanItem struct {
	Subject string
	URL     string
	Path    string
}

func buildImagePlanningSection(ctx context.Context, req ScriptDocsRequest, timeline *TimelinePlan, plan *VisualPlan, importantPhrases, importantWords []string, imgService *imgservice.Service) ScriptSection {
	subjects := plan.GlobalImageSubjects(5)
	subjects = append(subjects, extractStandaloneImageTerms(importantPhrases, importantWords)...)
	subjects = uniqueNonEmpty(subjects)
	if len(subjects) > 7 {
		subjects = subjects[:7]
	}

	zap.L().Info("Image planning starting from visual plan",
		zap.Strings("subjects", subjects),
		zap.String("topic", req.Topic),
	)

	if len(subjects) == 0 {
		return ScriptSection{
			Title: "📸 Entità con Immagine",
			Body:  "Nessun soggetto identificato per le immagini.",
		}
	}

	var items []imagePlanItem
	for _, subject := range subjects {
		if imgService != nil {
			prompts := buildImagePromptCandidates(subject, req.Topic, timeline)
			tags := buildImageTags(subject, req.Topic, timeline)
			asset, err := resolveImageAsset(ctx, imgService, subject, req.Topic, req.Language, prompts, tags, "subject")
			if err != nil {
				continue
			}
			if asset == nil {
				continue
			}

			items = append(items, imagePlanItem{
				Subject: subject,
				URL:     resolveImageDisplayURL(asset),
				Path:    asset.PathRel,
			})
		}
	}

	if len(items) == 0 {
		return ScriptSection{
			Title: "📸 Entità con Immagine",
			Body:  "Ricerca completata: nessuna immagine trovata online.",
		}
	}

	return ScriptSection{
		Title: "📸 Entità con Immagine",
		Body:  renderImagePlans(items),
	}
}

func buildImportantWordImageMap(ctx context.Context, req ScriptDocsRequest, timeline *TimelinePlan, importantPhrases, importantWords []string, imgService *imgservice.Service) map[string]string {
	if imgService == nil || len(importantWords) == 0 {
		return nil
	}

	selected := extractStandaloneImageTerms(importantPhrases, importantWords)
	if len(selected) == 0 {
		selected = importantWords
	}
	selected = uniqueNonEmpty(selected)
	if len(selected) > 4 {
		selected = selected[:4]
	}

	result := make(map[string]string, len(selected))
	for _, word := range selected {
		prompts := buildImportantWordPrompts(word, req.Topic, timeline)
		tags := buildImportantWordTags(word, req.Topic, timeline)
		asset, err := resolveImageAsset(ctx, imgService, word, req.Topic, req.Language, prompts, tags, "important word")
		if err != nil || asset == nil {
			continue
		}
		if link := resolveImageDisplayURL(asset); link != "" {
			result[strings.ToLower(strings.TrimSpace(word))] = link
		}
	}

	return result
}

func buildImagePromptCandidates(subject, topic string, timeline *TimelinePlan) []string {
	candidates := make([]string, 0, 8)
	subject = strings.TrimSpace(subject)
	topic = strings.TrimSpace(topic)

	if subject != "" && topic != "" {
		candidates = append(candidates, fmt.Sprintf("cinematic documentary image of %s related to %s", subject, topic))
	}
	if subject != "" {
		candidates = append(candidates, fmt.Sprintf("cinematic still of %s", subject))
	}
	if topic != "" {
		candidates = append(candidates, fmt.Sprintf("cinematic documentary image of %s", topic))
	}

	if timeline != nil {
		needle := strings.ToLower(subject)
		for _, seg := range timeline.Segments {
			matchSubject := strings.ToLower(seg.Subject)
			matchVisual := strings.ToLower(seg.VisualSubject)
			if subject != "" && (strings.Contains(matchSubject, needle) || strings.Contains(matchVisual, needle)) {
				candidates = append(candidates, seg.VisualPrompts...)
				if strings.TrimSpace(seg.VisualCaption) != "" {
					candidates = append(candidates, fmt.Sprintf("%s, cinematic documentary frame", seg.VisualCaption))
				}
				candidates = append(candidates, seg.SearchSuggestions...)
			}
		}
	}

	candidates = append(candidates, topic)
	return uniqueNonEmpty(candidates)
}

func buildImageTags(subject, topic string, timeline *TimelinePlan) []string {
	tags := []string{subject, topic}
	if timeline != nil {
		for _, seg := range timeline.Segments {
			tags = append(tags, seg.Subject, seg.VisualSubject)
		}
	}
	return uniqueNonEmpty(tags)
}

func buildImportantWordPrompts(word, topic string, timeline *TimelinePlan) []string {
	candidates := make([]string, 0, 6)
	word = strings.TrimSpace(word)
	topic = strings.TrimSpace(topic)

	if word != "" && topic != "" {
		candidates = append(candidates, fmt.Sprintf("cinematic documentary image of %s related to %s", word, topic))
	}
	if word != "" {
		candidates = append(candidates, fmt.Sprintf("cinematic still of %s", word))
	}
	if topic != "" {
		candidates = append(candidates, fmt.Sprintf("cinematic documentary image of %s", topic))
	}
	if timeline != nil {
		for _, seg := range timeline.Segments {
			if strings.Contains(strings.ToLower(seg.NarrativeText), strings.ToLower(word)) {
				candidates = append(candidates, seg.VisualPrompts...)
				if strings.TrimSpace(seg.VisualCaption) != "" {
					candidates = append(candidates, seg.VisualCaption)
				}
			}
		}
	}
	return uniqueNonEmpty(candidates)
}

func buildImportantWordTags(word, topic string, timeline *TimelinePlan) []string {
	tags := []string{word, topic}
	if timeline != nil {
		for _, seg := range timeline.Segments {
			tags = append(tags, seg.Subject, seg.VisualSubject)
			tags = append(tags, seg.Keywords...)
		}
	}
	return uniqueNonEmpty(tags)
}

func resolveImageAsset(ctx context.Context, imgService *imgservice.Service, subject, topic, language string, prompts, tags []string, label string) (*models.ImageAsset, error) {
	asset, err := imgService.GenerateSmartImage(ctx, subject, topic, prompts, tags, 1024, 1024, "")
	if err == nil && asset != nil {
		return asset, nil
	}

	logFields := []zap.Field{zap.String("subject", subject)}
	if strings.TrimSpace(label) != "" {
		logFields = append(logFields, zap.String("label", label))
	}
	zap.L().Warn("AI image generation failed, falling back to web search", append(logFields, zap.Error(err))...)

	slug := Slugify(subject)
	query := buildImageSearchQuery(subject, topic)
	asset, err = imgService.SearchAndDownload(slug, subject, query, language, tags)
	if err != nil {
		zap.L().Warn("web image search failed", append(logFields, zap.Error(err))...)
		return nil, err
	}
	return asset, nil
}

func buildImageSearchQuery(subject, topic string) string {
	subject = strings.TrimSpace(subject)
	topic = strings.TrimSpace(topic)
	query := subject
	if query == "" {
		query = topic
	}
	if query != "" && len(strings.Fields(subject)) < 2 && topic != "" && !strings.Contains(strings.ToLower(topic), strings.ToLower(subject)) {
		query = subject + " " + topic
	}
	return query
}

func extractStandaloneImageTerms(importantPhrases, importantWords []string) []string {
	phraseText := strings.ToLower(strings.Join(importantPhrases, " "))
	terms := make([]string, 0, len(importantWords))

	for _, word := range importantWords {
		normalized := strings.TrimSpace(word)
		if normalized == "" {
			continue
		}
		lower := strings.ToLower(normalized)
		if phraseText != "" && strings.Contains(phraseText, lower) {
			continue
		}
		terms = append(terms, normalized)
	}

	return terms
}

func resolveImageDisplayURL(asset *models.ImageAsset) string {
	if asset == nil {
		return ""
	}
	if strings.TrimSpace(asset.PathRel) != "" {
		return "/assets/" + strings.TrimPrefix(strings.ReplaceAll(asset.PathRel, "\\", "/"), "/")
	}
	if strings.TrimSpace(asset.SourceURL) != "" {
		return asset.SourceURL
	}
	return ""
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		result = append(result, value)
	}
	return result
}

func renderImagePlans(items []imagePlanItem) string {
	var b strings.Builder
	for i, item := range items {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("🖼️ \"%s\": %s", item.Subject, item.URL))
	}
	return b.String()
}

// Local slugify
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
