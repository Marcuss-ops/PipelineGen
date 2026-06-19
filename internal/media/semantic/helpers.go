package semantic

import (
	"fmt"
	"sort"
	"strings"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

var generatedNoiseWords = map[string]struct{}{
	"ai": {}, "generated": {}, "image": {}, "video": {}, "via": {}, "prompt": {},
	"for": {}, "flux": {}, "google-flow": {}, "google": {}, "flow": {},
	"nvidia": {}, "stabilityai": {}, "sdxl": {}, "turbo": {}, "standard": {},
	"quality": {}, "hd": {}, "and": {}, "the": {}, "of": {}, "to": {},
	"in": {}, "on": {}, "a": {}, "an": {}, "with": {}, "by": {},
	"de": {}, "del": {}, "della": {}, "di": {}, "e": {}, "ed": {}, "la": {},
	"le": {}, "gli": {}, "il": {}, "lo": {}, "un": {}, "una": {}, "uno": {},
	"da": {}, "nel": {}, "nella": {}, "nei": {}, "nelle": {}, "su": {},
}

func CleanGeneratedPrompt(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	if idx := strings.Index(lower, "for prompt:"); idx >= 0 {
		text = strings.TrimSpace(text[idx+len("for prompt:"):])
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	return strings.Join(strings.Fields(text), " ")
}

func ExtractSubjectAndTags(prompt string) (subject string, tags []string) {
	prompt = CleanGeneratedPrompt(prompt)
	if prompt == "" {
		return "unknown", nil
	}
	parts := strings.Split(prompt, ",")
	subject = strings.TrimSpace(parts[0])
	if len(subject) > 60 {
		subject = subject[:60]
	}
	seen := make(map[string]bool)
	for _, part := range parts {
		t := strings.TrimSpace(part)
		if t == "" {
			continue
		}
		lower := strings.ToLower(t)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		tags = append(tags, t)
	}
	return subject, tags
}

func UniqueAppend(base []string, items ...string) []string {
	seen := make(map[string]bool, len(base))
	for _, v := range base {
		seen[strings.ToLower(strings.TrimSpace(v))] = true
	}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item))
		if key == "" || seen[key] {
			continue
		}
		base = append(base, item)
		seen[key] = true
	}
	return base
}

func CompactSemanticPhrase(text string, maxWords, maxChars int) string {
	text = CleanGeneratedPrompt(text)
	if text == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		".", " ",
		",", " ",
		";", " ",
		":", " ",
		"/", " ",
		"\\", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
		"`", " ",
		"'", " ",
		"\"", " ",
	)
	cleaned := replacer.Replace(text)
	fields := strings.Fields(cleaned)
	if len(fields) == 0 {
		return ""
	}
	words := make([]string, 0, maxWords)
	for _, field := range fields {
		token := strings.Trim(field, "-_")
		norm := strings.ToLower(strings.TrimSpace(token))
		if len(norm) < 3 {
			continue
		}
		if _, ok := generatedNoiseWords[norm]; ok {
			continue
		}
		words = append(words, token)
		if len(words) >= maxWords {
			break
		}
	}
	if len(words) == 0 {
		limit := maxWords
		if len(fields) < limit {
			limit = len(fields)
		}
		for i := 0; i < limit; i++ {
			token := strings.Trim(fields[i], "-_")
			if token == "" {
				continue
			}
			words = append(words, token)
		}
	}
	phrase := strings.Join(words, " ")
	if len(phrase) > maxChars {
		phrase = phrase[:maxChars]
	}
	return strings.TrimSpace(strings.Trim(phrase, ",;:-"))
}

func CompactSemanticList(values []string, maxItems, maxWords, maxChars int) []string {
	out := make([]string, 0, maxItems)
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		phrase := CompactSemanticPhrase(value, maxWords, maxChars)
		if phrase == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(phrase))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, phrase)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func CompactGeneratedPayload(meta *Payload) *Payload {
	if meta == nil {
		return nil
	}

	subjects := CompactSemanticList(meta.Subjects, 3, 6, 60)
	if len(subjects) == 0 {
		subjectCandidates := append([]string{}, meta.ConceptTags...)
		subjectCandidates = append(subjectCandidates, meta.VisualObjects...)
		subjects = CompactSemanticList(subjectCandidates, 3, 6, 60)
	}
	if len(subjects) == 0 {
		subjects = CompactSemanticList(meta.Tags, 3, 6, 60)
	}
	if len(subjects) == 0 {
		fallback := CompactSemanticPhrase(meta.PromptOriginal, 6, 60)
		if fallback == "" {
			fallback = "unknown"
		}
		subjects = []string{fallback}
	}
	meta.Subjects = subjects
	meta.SubjectSlugs = make([]string, 0, len(subjects))
	for _, subject := range subjects {
		slug := strings.ToLower(strings.TrimSpace(subject))
		slug = strings.ReplaceAll(slug, "_", "-")
		slug = strings.ReplaceAll(slug, " ", "-")
		slug = strings.Trim(slug, "-")
		if slug != "" {
			meta.SubjectSlugs = append(meta.SubjectSlugs, slug)
		}
	}

	tagSources := append([]string{}, meta.Tags...)
	tagSources = append(tagSources, meta.ConceptTags...)
	tagSources = append(tagSources, meta.VisualObjects...)
	tagSources = append(tagSources, meta.EmotionalTone...)
	tagSources = append(tagSources, meta.Categories...)
	tagSources = append(tagSources, meta.Style...)
	meta.Tags = CompactSemanticList(tagSources, 12, 4, 48)

	parts := []string{}
	parts = append(parts, subjects...)
	parts = append(parts, meta.Tags...)
	parts = append(parts, meta.Categories...)
	parts = append(parts, meta.Mood...)
	parts = append(parts, meta.Style...)
	parts = append(parts, meta.ConceptTags...)
	parts = append(parts, meta.VisualObjects...)
	parts = append(parts, meta.EmotionalTone...)
	meta.SearchText = NormalizeSearchText(parts...)

	if strings.TrimSpace(meta.SemanticDescription) == "" || strings.TrimSpace(meta.SemanticDescription) == strings.TrimSpace(meta.PromptOriginal) || len(meta.SemanticDescription) > 240 {
		mediaLabel := meta.MediaType
		switch strings.ToLower(strings.TrimSpace(meta.MediaType)) {
		case "image":
			mediaLabel = "image"
		case "video":
			mediaLabel = "video"
		case "audio":
			mediaLabel = "sound effect"
		case "voiceover":
			mediaLabel = "voiceover"
		default:
			if mediaLabel == "" {
				mediaLabel = "asset"
			}
		}
		styleStr := ""
		if len(meta.Style) > 0 && strings.TrimSpace(meta.Style[0]) != "" {
			styleStr = fmt.Sprintf(" in %s style", strings.TrimSpace(meta.Style[0]))
		}
		meta.SemanticDescription = fmt.Sprintf("A generated %s depicting %s%s.", mediaLabel, strings.Join(subjects, ", "), styleStr)
	}

	return meta
}

func NormalizeSearchText(parts ...string) string {
	seen := make(map[string]bool)
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		for _, token := range strings.Fields(strings.ToLower(strings.TrimSpace(part))) {
			if token == "" || seen[token] {
				continue
			}
			seen[token] = true
			values = append(values, token)
		}
	}
	sort.Strings(values)
	return strings.Join(values, " ")
}

func AssetTypeForMediaType(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image":
		return "image"
	case "video":
		return "video"
	case "audio":
		return "audio"
	case "voiceover":
		return "voiceover"
	case "clip":
		return "clip"
	default:
		if mediaType == "" {
			return "asset"
		}
		return strings.ToLower(strings.TrimSpace(mediaType))
	}
}

func NewFallbackPayload(mediaType, prompt, style, generator string) *Payload {
	subject, tags := ExtractSubjectAndTags(prompt)
	if subject == "" {
		subject = CompactSemanticPhrase(prompt, 6, 60)
	}
	if subject == "" {
		subject = "unknown"
	}
	styleList := []string{}
	if strings.TrimSpace(style) != "" {
		styleList = append(styleList, strings.TrimSpace(style))
	}
	p := &Payload{
		AssetType:           AssetTypeForMediaType(mediaType),
		SemanticTier:        "generated_light",
		Source:              "generated",
		MediaType:           strings.TrimSpace(mediaType),
		Generator:           strings.TrimSpace(generator),
		PromptOriginal:      prompt,
		SemanticDescription: prompt,
		SearchText:          NormalizeSearchText(subject, strings.Join(tags, " "), style),
		Subjects:            []string{subject},
		Tags:                tags,
		Style:               styleList,
		EmbeddingStatus:     "pending",
		CreatedAt:           timeutil.FormatRFC3339(time.Now()),
	}
	return CompactGeneratedPayload(p)
}
