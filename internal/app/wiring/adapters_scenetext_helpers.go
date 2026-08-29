package wiring

import (
	"strconv"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func buildEditorialPromptFromGenReq(req scriptgen.GenerateRequest) string {
	return buildEditorialPrompt(req.Source.Topic, req.Source.SourceText, req.Title, req.Source.Query, req.ScriptParams.TargetWords, req.ScriptParams.MinWords, firstNonEmpty(req.Style, req.ScriptParams.Style), req.ScriptParams.Guidelines, string(req.SourceLanguage), req.ScriptParams.PromptVersion)
}

func buildEditorialPrompt(topic, sourceText, title, query string, targetWords, minWords int, style, guidelines, language, promptVersion string) string {
	sourceText, _ = scriptpkg.ParseArtlistDirectives(sourceText)
	var parts []string
	if topic != "" {
		parts = append(parts, "Topic: "+topic)
	}
	if sourceText != "" {
		parts = append(parts, "Source text:\n"+sourceText)
	}
	if guidelines != "" {
		parts = append(parts, "Guidelines:\n"+guidelines)
	}
	if title != "" {
		parts = append(parts, "Title: "+title)
	}
	if query != "" {
		parts = append(parts, "Search query: "+query)
	}
	if targetWords > 0 {
		parts = append(parts, "Target words: "+strconv.Itoa(targetWords))
	}
	if minWords > 0 {
		parts = append(parts, "Min words: "+strconv.Itoa(minWords))
	}
	// Style is the caller's positive writing directive (e.g. "Scrivi un
	// documentario storico preciso…"). The plan_builder editorial prompt
	// already carries it; this wiring path dropped it, so the single-shot
	// research run had no positive task inside the OVERRIDING WRITING
	// INSTRUCTIONS block (whose footer says "Do NOT write a video script")
	// and the model answered with a near-empty summary.
	if style != "" {
		parts = append(parts, "Style: "+style)
	}
	if language != "" {
		parts = append(parts, "Language: "+language)
	}
	if promptVersion != "" {
		parts = append(parts, "Prompt version: "+promptVersion)
	}
	parts = append(parts, "Do not include raw URLs, hyperlinks, or source citations in the prose output.")
	// Entity-mention contract (godlike/06 no-fake-availability): the
	// controlled entity test names one person, one place and one concept in
	// the source text; the model must state each verbatim, by name, instead
	// of degrading them into pronouns or generic descriptors. Without this
	// the downstream entity extractor can never recover the PERSON the
	// certification is trying to prove.
	parts = append(parts, "You MUST explicitly mention, verbatim and by name, the central person, the place and the main concept named in the source text. State each named entity at least once in the narration; never replace a named entity with only a pronoun or a generic descriptor such as \"an imposing figure\", \"the athlete\" or \"his presence\".")
	return strings.Join(parts, "\n\n")
}

func genSourceToSourceSpec(src scriptgen.Source) scriptpkg.SourceSpec {
	return scriptpkg.SourceSpec{
		Type:               scriptpkg.SourceType(src.Type),
		Topic:              src.Topic,
		SourceText:         src.SourceText,
		ArtlistKeywords:    copyStrings(src.ArtlistKeywords),
		Guidelines:         src.Guidelines,
		ClipIDs:            copyStrings(src.ClipIDs),
		IntroClipIDs:       copyStrings(src.IntroClipIDs),
		NumClips:           src.NumClips,
		Query:              src.Query,
		MaxClips:           src.MaxClips,
		MinCoverage:        src.MinCoverage,
		MinQualityScore:    cloneFloat64(src.MinQualityScore),
		MinTranscriptWords: cloneInt(src.MinTranscriptWords),
		TranscriptPolicy:   src.TranscriptPolicy,
		OrderingStrategy:   src.OrderingStrategy,
		GroundingPolicy:    src.GroundingPolicy,
		FallbackPolicy:     src.FallbackPolicy,
		ForceRefresh:       src.ForceRefresh,
		Search:             src.Search,
		AllowTextOnly:      src.AllowTextOnly,
		SourceFilter:       src.SourceFilter,
		MediaTypeFilter:    src.MediaTypeFilter,
		CachePolicy:        src.CachePolicy,
		Research:           src.Research,
	}
}

func genRequestToResolutionContext(req scriptgen.GenerateRequest) scriptpkg.SourceResolutionContext {
	return scriptpkg.SourceResolutionContext{
		Title:             req.Title,
		Language:          string(req.SourceLanguage),
		TargetWords:       req.ScriptParams.TargetWords,
		SegmentWords:      req.ScriptParams.SegmentWords,
		Segments:          scriptpkg.CloneScriptSegments(req.ScriptParams.Segments),
		NumClips:          req.Source.NumClips,
		RequireDriveLink:  true,
		RequireLocalMedia: false,
	}
}

func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func cloneFloat64(src *float64) *float64 {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func cloneInt(src *int) *int {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx >= 0 {
		return s[:idx]
	}
	return s
}
