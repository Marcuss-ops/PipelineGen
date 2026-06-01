package semantic

import (
	"sort"
	"strings"
	"time"
)

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

func ShouldUseLLMFallback(confidence, threshold float64) bool {
	return threshold > 0 && confidence > 0 && confidence < threshold
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
	styleList := []string{}
	if strings.TrimSpace(style) != "" {
		styleList = append(styleList, strings.TrimSpace(style))
	}
	return &Payload{
		AssetType:           AssetTypeForMediaType(mediaType),
		SemanticTier:        "generated_light",
		Source:              "generated",
		MediaType:           strings.TrimSpace(mediaType),
		Generator:           strings.TrimSpace(generator),
		PromptOriginal:      prompt,
		SemanticDescription: prompt,
		SearchText:          NormalizeSearchText(prompt, subject, strings.Join(tags, " "), style),
		Subjects:            []string{subject},
		Tags:                tags,
		Style:               styleList,
		Confidence:          0.5,
		EmbeddingStatus:     "pending",
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
	}
}

func AttachAssetInfo(meta *Payload, info map[string]any) {
	if meta == nil || info == nil {
		return
	}
	if meta.Assets == nil {
		meta.Assets = []map[string]any{}
	}
	meta.Assets = append(meta.Assets, info)
}

func ApplySemanticTaggerDefaults(meta *Payload, assetID, assetType, mediaType, generator string) *Payload {
	if meta == nil {
		return nil
	}
	if strings.TrimSpace(meta.AssetID) == "" {
		meta.AssetID = strings.TrimSpace(assetID)
	}
	if strings.TrimSpace(meta.AssetType) == "" {
		meta.AssetType = strings.TrimSpace(assetType)
	}
	if strings.TrimSpace(meta.MediaType) == "" {
		meta.MediaType = strings.TrimSpace(mediaType)
	}
	if strings.TrimSpace(meta.Generator) == "" {
		meta.Generator = strings.TrimSpace(generator)
	}
	return meta
}
