package youtube

import (
	"regexp"
	"sort"
	"strings"

	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
)

// ── Rich metadata normalization ────────────────────────────────────────────

func fallbackClipRichMetadata(title, transcript, description string) *clipRichMetadata {
	summary := deriveFallbackClipSummary(transcript, description)
	cleanTitle := deriveFallbackClipTitle(title, transcript, description)
	shortTitle := deriveFallbackShortTitle(cleanTitle)
	topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, tags, hook := deriveFallbackSemanticFields(title, transcript, description, cleanTitle)
	embeddingText := buildEmbeddingText(cleanTitle, summary, hook, topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, transcript)
	return &clipRichMetadata{
		ClipSummary:     summary,
		Topics:          topics,
		Speakers:        speakers,
		MentionedPeople: mentionedPeople,
		SourceTags:      sourceTags,
		ClipTags:        clipTags,
		SearchKeywords:  searchKeywords,
		People:          mentionedPeople,
		Hook:            hook,
		CleanTitle:      cleanTitle,
		ShortTitle:      shortTitle,
		EmbeddingText:   embeddingText,
		Tags:            tags,
	}
}

func normalizeClipRichMetadata(meta *clipRichMetadata, title, transcript, description string) *clipRichMetadata {
	if meta == nil {
		return fallbackClipRichMetadata(title, transcript, description)
	}

	meta.ClipSummary = strings.TrimSpace(meta.ClipSummary)
	meta.CleanTitle = strings.TrimSpace(meta.CleanTitle)
	meta.ShortTitle = strings.TrimSpace(meta.ShortTitle)
	meta.Hook = strings.TrimSpace(meta.Hook)
	meta.Topics = normalizeClipTagList(meta.Topics)
	meta.Speakers = normalizeClipTagList(meta.Speakers)
	meta.MentionedPeople = normalizeClipTagList(meta.MentionedPeople)
	meta.SourceTags = normalizeClipTagList(meta.SourceTags)
	meta.ClipTags = normalizeClipTagList(meta.ClipTags)
	meta.SearchKeywords = normalizeClipTagList(meta.SearchKeywords)
	meta.People = normalizeClipTagList(meta.People)
	meta.Tags = normalizeClipTagList(meta.Tags)
	meta.CleanTranscript = cleanClipTranscript(meta.CleanTranscript)
	meta.EmbeddingText = strings.TrimSpace(meta.EmbeddingText)
	meta.QualityScore = sliceutil.ClampFloat64(meta.QualityScore, 0, 1)
	if len(meta.MentionedPeople) == 0 && len(meta.People) > 0 {
		meta.MentionedPeople = append([]string(nil), meta.People...)
	}
	meta.People = mergeTagLists(meta.Speakers, meta.MentionedPeople, meta.People)

	if meta.CleanTitle == "" {
		meta.CleanTitle = deriveFallbackClipTitle(title, transcript, description)
	}
	if meta.ShortTitle == "" {
		meta.ShortTitle = deriveFallbackShortTitle(meta.CleanTitle)
	}
	if meta.ClipSummary == "" {
		meta.ClipSummary = deriveFallbackClipSummary(transcript, description)
	}

	fallbackTopics, fallbackSpeakers, fallbackMentionedPeople, fallbackSourceTags, fallbackClipTags, fallbackSearchKeywords, _, fallbackHook := deriveFallbackSemanticFields(title, transcript, description, meta.CleanTitle)
	if len(meta.Topics) == 0 {
		meta.Topics = fallbackTopics
	}
	if len(meta.Speakers) == 0 {
		meta.Speakers = fallbackSpeakers
	}
	if len(meta.MentionedPeople) == 0 {
		meta.MentionedPeople = fallbackMentionedPeople
	}
	if len(meta.People) == 0 {
		meta.People = append([]string(nil), meta.MentionedPeople...)
	}
	if len(meta.SourceTags) == 0 {
		meta.SourceTags = fallbackSourceTags
	}
	if len(meta.ClipTags) == 0 {
		meta.ClipTags = fallbackClipTags
	}
	if len(meta.SearchKeywords) == 0 {
		meta.SearchKeywords = fallbackSearchKeywords
	}
	if len(meta.Tags) == 0 {
		meta.Tags = mergeTagLists(meta.SourceTags, meta.ClipTags, meta.SearchKeywords, meta.Topics, meta.Speakers, meta.MentionedPeople)
	}
	if meta.EmbeddingText == "" {
		meta.EmbeddingText = buildEmbeddingText(meta.CleanTitle, meta.ClipSummary, meta.Hook, meta.Topics, meta.Speakers, meta.MentionedPeople, meta.SourceTags, meta.ClipTags, meta.SearchKeywords, transcript)
	}
	if meta.Hook == "" {
		meta.Hook = fallbackHook
	}
	return meta
}

// ── Tag normalization ──────────────────────────────────────────────────────

// normalizeClipTagList normalizes, filters generic tags, and deduplicates a
// tag list in first-seen order. Delegates to the generic sliceutil helper
// with the youtube-specific normalize/skip functions.
func normalizeClipTagList(tags []string) []string {
	return sliceutil.NormalizeAndDedupe(tags, normalizeClipTag, isGenericClipTag)
}

// mergeTagLists normalizes, filters generic tags, and merges multiple tag
// lists into a single deduplicated slice. Delegates to the generic
// sliceutil helper with the youtube-specific normalize/skip functions.
func mergeTagLists(lists ...[]string) []string {
	return sliceutil.MergeNormalizedListsVariadic(normalizeClipTag, isGenericClipTag, lists...)
}

func normalizeClipTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag = strings.ReplaceAll(tag, "_", " ")
	tag = strings.ReplaceAll(tag, "-", " ")
	tag = strings.Join(strings.Fields(tag), " ")
	return tag
}

func containsNormalized(list []string, target string) bool {
	target = normalizeClipTag(target)
	for _, item := range list {
		if normalizeClipTag(item) == target {
			return true
		}
	}
	return false
}

func isGenericClipTag(tag string) bool {
	switch tag {
	case "", "video", "clip", "clips", "youtube", "yt", "podcast", "interview", "comedy", "talk show", "stand up", "stand up comedy", "comedian",
		"https", "http", "www", "com", "nbsp", "code", "watch", "listen", "subscribe", "channel", "official", "new",
		"tour", "dates", "go", "check", "find", "submit", "merch", "music", "producer", "facebook", "instagram", "twitter",
		"spotify", "live", "wiltern", "theater", "los angeles":
		return true
	}
	genericFragments := []string{
		"podcast",
		"interview",
		"comedy",
		"stand up",
		"talk show",
		"youtube",
		"clip",
		"live",
		"official video",
		"shorts",
		"highlights",
	}
	for _, frag := range genericFragments {
		if tag == frag || strings.Contains(tag, frag) {
			return true
		}
	}
	return false
}

func isGenericPersonPhrase(tag string) bool {
	switch tag {
	case "this past weekend", "this past weekend w theo von", "wiltern theater", "los angeles", "tour dates", "new merch", "celsius", "perplexity", "prize picks", "moonpay", "tecovas", "liquid iv", "blue chew", "paramount plus", "spotify":
		return true
	}
	return false
}

// ── People/topic extraction ────────────────────────────────────────────────

func extractPeopleTags(parts ...string) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, text := range parts {
		for _, phrase := range extractCapitalizedPhrases(text) {
			norm := normalizeClipTag(phrase)
			if norm == "" || isGenericClipTag(norm) || isGenericPersonPhrase(norm) {
				continue
			}
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			out = append(out, norm)
		}
	}
	return out
}

func extractCapitalizedPhrases(text string) []string {
	if text == "" {
		return nil
	}
	// Match proper-noun phrases with at least two words without hardcoding specific names.
	re := regexp.MustCompile(`\b[A-Z][a-z]+(?:\s+[A-Z][a-z]+){1,2}\b`)
	matches := re.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		norm := normalizeClipTag(m)
		if norm == "" || isGenericClipTag(norm) {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, m)
	}
	return out
}

func extractTopicTags(text string) []string {
	phrases := extractConceptTags(text, 6)
	return normalizeClipTagList(phrases)
}

func extractConceptTags(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" || max <= 0 {
		return nil
	}
	stopwords := map[string]struct{}{
		"the": {}, "and": {}, "for": {}, "with": {}, "that": {}, "this": {}, "from": {}, "you": {}, "your": {}, "are": {},
		"was": {}, "were": {}, "has": {}, "have": {}, "had": {}, "his": {}, "her": {}, "him": {}, "she": {}, "they": {},
		"them": {}, "their": {}, "there": {}, "here": {}, "what": {}, "when": {}, "where": {}, "why": {}, "how": {},
		"who": {}, "into": {}, "onto": {}, "like": {}, "just": {}, "really": {}, "very": {}, "could": {},
		"would": {}, "should": {}, "about": {}, "after": {}, "before": {}, "because": {}, "then": {}, "than": {},
		"also": {}, "been": {}, "being": {}, "our": {}, "out": {}, "over": {}, "under": {}, "some": {},
		"more": {}, "most": {}, "much": {}, "many": {}, "way": {}, "one": {}, "two": {}, "three": {}, "all": {},
		"not": {}, "can": {}, "will": {}, "able": {}, "if": {}, "or": {}, "so": {}, "um": {}, "uh": {},
		"https": {}, "http": {}, "www": {}, "com": {}, "nbsp": {}, "code": {}, "watch": {}, "listen": {}, "subscribe": {},
		"channel": {}, "official": {}, "new": {}, "tour": {}, "dates": {}, "go": {}, "check": {}, "find": {}, "submit": {},
		"merch": {}, "music": {}, "producer": {}, "facebook": {}, "instagram": {}, "twitter": {}, "spotify": {}, "live": {},
		"wiltern": {}, "theater": {}, "los": {}, "angeles": {}, "video": {}, "videos": {}, "clip": {}, "clips": {},
	}

	freq := make(map[string]int)
	order := make([]string, 0)
	wordRe := regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9']+`)
	for _, raw := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\t' || r == '.' || r == ',' || r == ';' || r == ':' || r == '!' || r == '?' || r == '/' || r == '|' || r == '(' || r == ')'
	}) {
		words := wordRe.FindAllString(strings.ToLower(raw), -1)
		for _, w := range words {
			if len(w) < 6 {
				continue
			}
			if _, ok := stopwords[w]; ok {
				continue
			}
			if isGenericClipTag(w) {
				continue
			}
			if _, ok := freq[w]; !ok {
				order = append(order, w)
			}
			freq[w]++
		}
	}

	type kv struct {
		word  string
		score int
	}
	ranked := make([]kv, 0, len(freq))
	for _, w := range order {
		ranked = append(ranked, kv{word: w, score: freq[w]})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return len(ranked[i].word) > len(ranked[j].word)
		}
		return ranked[i].score > ranked[j].score
	})

	out := make([]string, 0, max)
	seen := make(map[string]struct{})
	for _, item := range ranked {
		if _, ok := seen[item.word]; ok {
			continue
		}
		seen[item.word] = struct{}{}
		out = append(out, item.word)
		if len(out) >= max {
			break
		}
	}
	return out
}

func cleanClipTranscript(transcript string) string {
	if transcript == "" {
		return ""
	}
	lines := strings.Split(transcript, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.ReplaceAll(line, "&gt;&gt;", "")
		line = strings.ReplaceAll(line, "&gt;", "")
		line = strings.ReplaceAll(line, "&nbsp;", " ")
		line = strings.ReplaceAll(line, "gt gt", "")
		line = strings.ReplaceAll(line, "[laughter]", "")
		line = strings.ReplaceAll(line, "[laughs]", "")
		line = strings.ReplaceAll(line, "[applause]", "")
		line = strings.ReplaceAll(line, "[cheering]", "")
		line = strings.ReplaceAll(line, "[music]", "")
		line = strings.ReplaceAll(line, "[__]", "")
		line = strings.ReplaceAll(line, "[ __ ]", "")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	result := strings.Join(cleaned, " ")
	result = strings.ReplaceAll(result, "  ", " ")
	result = strings.Join(strings.Fields(result), " ")
	return strings.TrimSpace(result)
}
