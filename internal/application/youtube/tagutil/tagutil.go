// Package tagutil provides YouTube-specific tag normalization, text cleaning,
// phrase extraction, and fallback metadata derivation used across the YouTube
// capability sub-packages (metadata, search, extraction, segments, enrichment).
//
// This is NOT a generic "common" package — every function here is specific to
// YouTube clip processing. The package is a leaf: it imports only stdlib,
// pkg/sliceutil, pkg/textutil, and youtube/types.
package tagutil

import (
	"crypto/md5"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	similarity "github.com/Marcuss-ops/PipelineGen/pkg/similarity"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Tag normalization ──────────────────────────────────────────────────────

// NormalizeClipTagList normalizes, filters generic tags, and deduplicates a
// tag list in first-seen order.
func NormalizeClipTagList(tags []string) []string {
	return sliceutil.NormalizeAndDedupe(tags, NormalizeClipTag, IsGenericClipTag)
}

// MergeTagLists normalizes, filters generic tags, and merges multiple tag
// lists into a single deduplicated slice.
func MergeTagLists(lists ...[]string) []string {
	return sliceutil.MergeNormalizedListsVariadic(NormalizeClipTag, IsGenericClipTag, lists...)
}

// NormalizeClipTag normalizes a single tag: lowercases, replaces separators,
// collapses whitespace.
func NormalizeClipTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag = strings.ReplaceAll(tag, "_", " ")
	tag = strings.ReplaceAll(tag, "-", " ")
	tag = strings.Join(strings.Fields(tag), " ")
	return tag
}

// ContainsNormalized checks whether a normalized target string appears in a
// list after normalization.
func ContainsNormalized(list []string, target string) bool {
	target = NormalizeClipTag(target)
	for _, item := range list {
		if NormalizeClipTag(item) == target {
			return true
		}
	}
	return false
}

// IsGenericClipTag returns true for tags that are too generic to be useful
// (e.g. "video", "podcast", "youtube").
func IsGenericClipTag(tag string) bool {
	switch tag {
	case "", "video", "clip", "clips", "youtube", "yt", "podcast", "interview", "comedy", "talk show", "stand up", "stand up comedy", "comedian",
		"https", "http", "www", "com", "nbsp", "code", "watch", "listen", "subscribe", "channel", "official", "new",
		"tour", "dates", "go", "check", "find", "submit", "merch", "music", "producer", "facebook", "instagram", "twitter",
		"spotify", "live", "wiltern", "theater", "los angeles":
		return true
	}
	genericFragments := []string{
		"podcast", "interview", "comedy", "stand up", "talk show",
		"youtube", "clip", "live", "official video", "shorts", "highlights",
	}
	for _, frag := range genericFragments {
		if tag == frag || strings.Contains(tag, frag) {
			return true
		}
	}
	return false
}

// IsGenericPersonPhrase returns true for phrases that look like person names
// but are actually generic (show names, venues, sponsors).
func IsGenericPersonPhrase(tag string) bool {
	switch tag {
	case "this past weekend", "this past weekend w theo von", "wiltern theater", "los angeles",
		"tour dates", "new merch", "celsius", "perplexity", "prize picks", "moonpay",
		"tecovas", "liquid iv", "blue chew", "paramount plus", "spotify":
		return true
	}
	return false
}

// ── People/topic extraction ────────────────────────────────────────────────

// ExtractPeopleTags extracts likely person names from text segments using
// capitalized-phrase heuristics.
func ExtractPeopleTags(parts ...string) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, text := range parts {
		for _, phrase := range ExtractCapitalizedPhrases(text) {
			norm := NormalizeClipTag(phrase)
			if norm == "" || IsGenericClipTag(norm) || IsGenericPersonPhrase(norm) {
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

// ExtractCapitalizedPhrases matches proper-noun phrases (2-3 capitalized words).
func ExtractCapitalizedPhrases(text string) []string {
	if text == "" {
		return nil
	}
	re := regexp.MustCompile(`\b[A-Z][a-z]+(?:\s+[A-Z][a-z]+){1,2}\b`)
	matches := re.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		norm := NormalizeClipTag(m)
		if norm == "" || IsGenericClipTag(norm) {
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

// ExtractTopicTags extracts topic-like phrases from text.
func ExtractTopicTags(text string) []string {
	phrases := ExtractConceptTags(text, 6)
	return NormalizeClipTagList(phrases)
}

// ExtractConceptTags extracts up to max frequent, non-stopword terms.
func ExtractConceptTags(text string, max int) []string {
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
			if IsGenericClipTag(w) {
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

// ── Transcript / description cleaning ──────────────────────────────────────

// CleanClipTranscript strips subtitle artifacts (HTML entities, bracketed
// cues) from a transcript and returns cleaned prose.
func CleanClipTranscript(transcript string) string {
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

// CleanYouTubeDescription strips link lines, HTML artifacts, and sponsor
// boilerplate from a YouTube description. Returns compact prose.
func CleanYouTubeDescription(desc string) string {
	if desc == "" {
		return ""
	}
	desc = strings.ReplaceAll(desc, "&gt;&gt;", "")
	desc = strings.ReplaceAll(desc, "&gt;", "")
	desc = strings.ReplaceAll(desc, "&nbsp;", " ")
	lines := strings.Split(desc, "\n")
	var kept []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "timestamp") || strings.Contains(lower, "chapter") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, " ")
}

// CleanClipName strips ugly artifacts from segment names produced by subtitle
// extraction or Ollama analysis.
func CleanClipName(name string) string {
	name = strings.ReplaceAll(name, "&gt;&gt;", "")
	name = strings.ReplaceAll(name, "&gt;", "")
	name = strings.ReplaceAll(name, "&nbsp;", " ")
	name = strings.ReplaceAll(name, "gt gt", "")
	name = strings.ReplaceAll(name, "[music]", "")
	name = strings.ReplaceAll(name, "[Music]", "")
	name = strings.ReplaceAll(name, "[MUSIC]", "")
	name = strings.ReplaceAll(name, "[Applause]", "")
	name = strings.ReplaceAll(name, "[__]", "")
	name = strings.ReplaceAll(name, "[ __ ]", "")
	name = strings.Join(strings.Fields(name), " ")
	name = strings.TrimSpace(name)
	const maxClipNameRunes = 80
	runes := []rune(name)
	if len(runes) > maxClipNameRunes {
		name = string(runes[:maxClipNameRunes])
		name = strings.TrimRight(name, "-_ ")
	}
	if name == "" {
		name = "clip"
	}
	return name
}

// CompactYouTubeDescription keeps the first few non-sponsor, non-link lines
// of a YouTube description up to a 500-character budget.
func CompactYouTubeDescription(desc string) string {
	desc = CleanYouTubeDescription(desc)
	if desc == "" {
		return ""
	}
	parts := strings.Split(desc, "\n")
	var kept []string
	limitChars := 500
	stopMarkers := []string{
		"sponsored by", "tour dates", "new merch", "submit your", "hit the hotline",
		"video hotline", "find theo", "producer:", "watch on spotify",
	}
	for _, part := range parts {
		line := strings.TrimSpace(part)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		stop := false
		for _, marker := range stopMarkers {
			if strings.Contains(lower, marker) {
				stop = true
				break
			}
		}
		if stop {
			break
		}
		if strings.Contains(line, "http://") || strings.Contains(line, "https://") || strings.Contains(line, "www.") {
			continue
		}
		kept = append(kept, line)
		if len(strings.Join(kept, " ")) >= limitChars || len(kept) >= 3 {
			break
		}
	}
	return strings.Join(kept, " ")
}

// ExtractKeyPhrases extracts up to maxPhrases meaningful noun phrases from a
// description string using simple heuristics.
func ExtractKeyPhrases(desc string, maxPhrases int) []string {
	if desc == "" || maxPhrases <= 0 {
		return nil
	}
	desc = CleanYouTubeDescription(desc)
	parts := strings.Split(desc, "\n")
	out := make([]string, 0, maxPhrases)
	seen := make(map[string]struct{})
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || IsGenericClipTag(part) {
			continue
		}
		words := strings.Fields(part)
		if len(words) < 3 || len(words) > 20 {
			continue
		}
		norm := NormalizeClipTag(part)
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, part)
		if len(out) >= maxPhrases {
			break
		}
	}
	return out
}

// ── Semantic field derivation ──────────────────────────────────────────────

// DeriveFallbackSemanticFields extracts all semantic fields from clip text
// using pure heuristics (no LLM). Returns topics, speakers, mentionedPeople,
// sourceTags, clipTags, searchKeywords, tags, hook.
func DeriveFallbackSemanticFields(title, transcript, description, cleanTitle string) (topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, tags []string, hook string) {
	cleanTranscript := CleanClipTranscript(transcript)
	combined := strings.Join([]string{title, cleanTranscript, cleanTitle}, "\n")
	combined = CleanYouTubeDescription(combined)
	if combined == "" {
		return nil, nil, nil, nil, nil, nil, nil, ""
	}
	speakers = DeriveFallbackSpeakers(title, transcript, description, cleanTitle)
	mentionedPeople = ExtractPeopleTags(title, transcript, description, cleanTitle)
	sourceTags = DeriveFallbackSourceTags(title, description, speakers)
	clipTags = ExtractTopicTags(combined)
	searchKeywords = DeriveFallbackSearchKeywords(cleanTranscript, cleanTitle, title)
	topics = MergeTagLists(clipTags, searchKeywords)
	hook = ExtractFallbackHook(transcript, description)
	tags = MergeTagLists(sourceTags, clipTags, searchKeywords, topics, speakers, mentionedPeople)
	return topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, tags, hook
}

// DeriveFallbackSpeakers extracts up to 3 speaker names from capitalized phrases.
func DeriveFallbackSpeakers(title, transcript, description, cleanTitle string) []string {
	out := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, phrase := range ExtractCapitalizedPhrases(strings.Join([]string{title, transcript, description, cleanTitle}, "\n")) {
		norm := NormalizeClipTag(phrase)
		if norm == "" || IsGenericPersonPhrase(norm) {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// DeriveFallbackSourceTags extracts source/channel tags from title and description.
func DeriveFallbackSourceTags(title, description string, speakers []string) []string {
	candidates := []string{title, description}
	out := make([]string, 0, 6)
	seen := make(map[string]struct{})
	for _, text := range candidates {
		for _, phrase := range ExtractCapitalizedPhrases(text) {
			norm := NormalizeClipTag(phrase)
			if norm == "" || IsGenericClipTag(norm) {
				continue
			}
			if ContainsNormalized(speakers, norm) {
				continue
			}
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			out = append(out, norm)
		}
	}
	if textutil.ContainsCI(title, "this past weekend") {
		if _, ok := seen["this past weekend"]; !ok {
			out = append(out, "this past weekend")
			seen["this past weekend"] = struct{}{}
		}
		if _, ok := seen["tpw"]; !ok {
			out = append(out, "tpw")
		}
	}
	return out
}

// DeriveFallbackSearchKeywords extracts searchable keyword phrases.
func DeriveFallbackSearchKeywords(cleanTranscript, cleanTitle, title string) []string {
	combined := strings.Join([]string{cleanTranscript, cleanTitle, title}, "\n")
	combined = CleanYouTubeDescription(combined)
	keyPhrases := ExtractKeyPhrases(combined, 6)
	if len(keyPhrases) == 0 {
		keyPhrases = ExtractConceptTags(combined, 6)
	}
	return NormalizeClipTagList(keyPhrases)
}

// BuildEmbeddingText constructs a structured embedding text block from
// clip metadata fields.
func BuildEmbeddingText(cleanTitle, clipSummary, hook string, topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords []string, _ string) string {
	parts := make([]string, 0, 8)
	if cleanTitle != "" {
		parts = append(parts, "Title: "+cleanTitle)
	}
	if clipSummary != "" {
		parts = append(parts, "Summary: "+clipSummary)
	}
	if hook != "" {
		parts = append(parts, "Hook: "+hook)
	}
	if len(topics) > 0 {
		parts = append(parts, "Topics: "+strings.Join(topics, ", "))
	}
	if len(speakers) > 0 {
		parts = append(parts, "Speakers: "+strings.Join(speakers, ", "))
	}
	if len(mentionedPeople) > 0 {
		parts = append(parts, "Mentioned people: "+strings.Join(mentionedPeople, ", "))
	}
	if len(sourceTags) > 0 {
		parts = append(parts, "Source tags: "+strings.Join(sourceTags, ", "))
	}
	if len(clipTags) > 0 {
		parts = append(parts, "Clip tags: "+strings.Join(clipTags, ", "))
	}
	if len(searchKeywords) > 0 {
		parts = append(parts, "Search keywords: "+strings.Join(searchKeywords, ", "))
	}
	return strings.Join(parts, "\n")
}

// DeriveFallbackClipSummary returns a 2-sentence summary from transcript or description.
func DeriveFallbackClipSummary(transcript, description string) string {
	text := transcript
	if text == "" {
		text = description
	}
	text = CleanYouTubeDescription(text)
	if text == "" {
		return ""
	}
	parts := strings.Split(text, "\n")
	var sentences []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || IsGenericClipTag(part) {
			continue
		}
		sentences = append(sentences, part)
		if len(sentences) >= 2 {
			break
		}
	}
	return strings.Join(sentences, " ")
}

// DeriveFallbackClipTitle derives a concise clip title from available text.
func DeriveFallbackClipTitle(title, transcript, description string) string {
	candidates := []string{transcript, description, title}
	for _, c := range candidates {
		c = CleanYouTubeDescription(c)
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		parts := strings.Fields(c)
		if len(parts) > 0 {
			limit := 10
			if len(parts) < limit {
				limit = len(parts)
			}
			joined := strings.Join(parts[:limit], " ")
			joined = strings.TrimSpace(joined)
			if joined != "" {
				return strings.Title(joined)
			}
		}
	}
	if title != "" {
		return title
	}
	return "Clip"
}

// DeriveFallbackShortTitle returns a shortened version of the clean title.
func DeriveFallbackShortTitle(cleanTitle string) string {
	words := strings.Fields(cleanTitle)
	if len(words) == 0 {
		return ""
	}
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.Join(words, " ")
}

// ExtractFallbackHook returns the strongest opening line from transcript or description.
func ExtractFallbackHook(transcript, description string) string {
	if transcript != "" {
		transcript = CleanYouTubeDescription(transcript)
		for _, line := range strings.Split(transcript, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				if len(line) > 140 {
					line = line[:140]
				}
				return line
			}
		}
	}
	if description != "" {
		description = CleanYouTubeDescription(description)
		for _, line := range strings.Split(description, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				if len(line) > 140 {
					line = line[:140]
				}
				return line
			}
		}
	}
	return ""
}

// DeriveSearchVisibility maps a quality score to a visibility tier.
func DeriveSearchVisibility(qualityScore float64) string {
	switch {
	case qualityScore >= 0.80:
		return "high"
	case qualityScore >= 0.45:
		return "normal"
	case qualityScore >= 0.30:
		return "low"
	default:
		return "poor"
	}
}

// ── Rich metadata normalization ────────────────────────────────────────────

// clipRichMetadata is a zero-copy alias to types.ClipRichMetadata.
type clipRichMetadata = types.ClipRichMetadata

// FallbackClipRichMetadata builds a clipRichMetadata from text heuristics.
func FallbackClipRichMetadata(title, transcript, description string) *clipRichMetadata {
	summary := DeriveFallbackClipSummary(transcript, description)
	cleanTitle := DeriveFallbackClipTitle(title, transcript, description)
	shortTitle := DeriveFallbackShortTitle(cleanTitle)
	topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, tags, hook := DeriveFallbackSemanticFields(title, transcript, description, cleanTitle)
	embeddingText := BuildEmbeddingText(cleanTitle, summary, hook, topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, transcript)
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

// NormalizeClipRichMetadata normalizes and fills gaps in a clipRichMetadata.
func NormalizeClipRichMetadata(meta *clipRichMetadata, title, transcript, description string) *clipRichMetadata {
	if meta == nil {
		return FallbackClipRichMetadata(title, transcript, description)
	}
	meta.ClipSummary = strings.TrimSpace(meta.ClipSummary)
	meta.CleanTitle = strings.TrimSpace(meta.CleanTitle)
	meta.ShortTitle = strings.TrimSpace(meta.ShortTitle)
	meta.Hook = strings.TrimSpace(meta.Hook)
	meta.Topics = NormalizeClipTagList(meta.Topics)
	meta.Speakers = NormalizeClipTagList(meta.Speakers)
	meta.MentionedPeople = NormalizeClipTagList(meta.MentionedPeople)
	meta.SourceTags = NormalizeClipTagList(meta.SourceTags)
	meta.ClipTags = NormalizeClipTagList(meta.ClipTags)
	meta.SearchKeywords = NormalizeClipTagList(meta.SearchKeywords)
	meta.People = NormalizeClipTagList(meta.People)
	meta.Tags = NormalizeClipTagList(meta.Tags)
	meta.CleanTranscript = CleanClipTranscript(meta.CleanTranscript)
	meta.EmbeddingText = strings.TrimSpace(meta.EmbeddingText)
	meta.QualityScore = sliceutil.ClampFloat64(meta.QualityScore, 0, 1)
	if len(meta.MentionedPeople) == 0 && len(meta.People) > 0 {
		meta.MentionedPeople = append([]string(nil), meta.People...)
	}
	meta.People = MergeTagLists(meta.Speakers, meta.MentionedPeople, meta.People)
	if meta.CleanTitle == "" {
		meta.CleanTitle = DeriveFallbackClipTitle(title, transcript, description)
	}
	if meta.ShortTitle == "" {
		meta.ShortTitle = DeriveFallbackShortTitle(meta.CleanTitle)
	}
	if meta.ClipSummary == "" {
		meta.ClipSummary = DeriveFallbackClipSummary(transcript, description)
	}
	fallbackTopics, fallbackSpeakers, fallbackMentionedPeople, fallbackSourceTags, fallbackClipTags, fallbackSearchKeywords, _, fallbackHook := DeriveFallbackSemanticFields(title, transcript, description, meta.CleanTitle)
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
		meta.Tags = MergeTagLists(meta.SourceTags, meta.ClipTags, meta.SearchKeywords, meta.Topics, meta.Speakers, meta.MentionedPeople)
	}
	if meta.EmbeddingText == "" {
		meta.EmbeddingText = BuildEmbeddingText(meta.CleanTitle, meta.ClipSummary, meta.Hook, meta.Topics, meta.Speakers, meta.MentionedPeople, meta.SourceTags, meta.ClipTags, meta.SearchKeywords, transcript)
	}
	if meta.Hook == "" {
		meta.Hook = fallbackHook
	}
	return meta
}

// MergeYouTubeClipTags combines existing tags, YouTube tags, and clip metadata fields.
func MergeYouTubeClipTags(existingTags, ytTags []string, clipMetadata *clipRichMetadata) []string {
	combined := make([]string, 0, len(existingTags)+len(ytTags))
	combined = append(combined, existingTags...)
	combined = append(combined, ytTags...)
	if clipMetadata != nil {
		combined = append(combined, clipMetadata.SourceTags...)
		combined = append(combined, clipMetadata.ClipTags...)
		combined = append(combined, clipMetadata.SearchKeywords...)
		combined = append(combined, clipMetadata.Topics...)
		combined = append(combined, clipMetadata.Speakers...)
		combined = append(combined, clipMetadata.MentionedPeople...)
		if clipMetadata.CleanTitle != "" {
			combined = append(combined, clipMetadata.CleanTitle)
		}
	}
	return NormalizeClipTagList(combined)
}

// ── URL / download utilities ──────────────────────────────────────────────

// CanonicalYouTubeURL normalizes a YouTube URL to the standard watch format.
func CanonicalYouTubeURL(inputURL, videoID string) string {
	if videoID == "" {
		return ""
	}
	parsed, err := url.Parse(inputURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}
	if strings.Contains(host, "youtube.com") || host == "youtu.be" {
		return "https://www.youtube.com/watch?v=" + videoID
	}
	return ""
}

// ValidateDownloadURL validates that a URL is from an allowed host.
func ValidateDownloadURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("URL is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := []string{
		"youtube.com", "www.youtube.com", "youtu.be",
		"m.youtube.com",
	}
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			return nil
		}
	}
	return fmt.Errorf("URL host %q is not in the allowed list", host)
}

// FallbackMD5String computes an MD5 hex digest of a string.
func FallbackMD5String(data string) string {
	h := md5.Sum([]byte(data))
	return fmt.Sprintf("%x", h)
}

// FallbackMD5File computes an MD5 hex digest of a file's contents.
func FallbackMD5File(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := md5.Sum(data)
	return fmt.Sprintf("%x", h)
}

// ── Semantic text normalization ───────────────────────────────────────

// NormalizeSemanticText lowercases, strips punctuation/HTML, filters short
// words and generic tokens, and returns a cleaned token string.
func NormalizeSemanticText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.NewReplacer(
		"&gt;", " ", "&nbsp;", " ", "https://", " ", "http://", " ",
		",", " ", ".", " ", "!", " ", "?", " ", ";", " ", ":", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "-", " ", "_", " ",
		"\"", " ", "'", " ", "/", " ", "\\", " ", "|", " ", "#", " ",
	).Replace(text)
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	filtered := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 3 || IsGenericToken(w) {
			continue
		}
		filtered = append(filtered, w)
	}
	return strings.Join(filtered, " ")
}

// IsGenericToken returns true for common English stopwords, filler words,
// and YouTube boilerplate tokens that should be excluded from semantic analysis.
func IsGenericToken(token string) bool {
	switch token {
	case "the", "and", "for", "with", "that", "this", "from", "you", "your", "are", "was", "were", "has", "have", "had",
		"his", "her", "him", "she", "they", "them", "their", "there", "here", "what", "when", "where", "why", "how",
		"who", "into", "onto", "like", "just", "really", "very", "could", "would", "should", "about", "after",
		"before", "because", "then", "than", "also", "been", "being", "our", "out", "over", "under", "some", "more",
		"most", "much", "many", "way", "one", "two", "three", "all", "not", "can", "will", "able", "if", "or", "so",
		"um", "uh", "https", "http", "www", "com", "nbsp", "code", "watch", "listen", "subscribe", "channel", "official",
		"new", "tour", "dates", "go", "check", "find", "submit", "merch", "music", "producer", "facebook", "instagram",
		"twitter", "spotify", "live", "video", "videos", "clip", "clips":
		return true
	}
	return false
}

// ── Transient download error detection ──────────────────────────────

// IsTransientDownloadError returns true if the error is likely transient
// and worth retrying (e.g. timeout, connection reset, HTTP 429/5xx).
// Permanent errors (video unavailable, private, invalid URL, etc.) return false.
func IsTransientDownloadError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	permanentPatterns := []string{
		"video unavailable", "private video", "sign in to confirm",
		"confirm your age", "requested format is not available",
		"invalid url", "unable to extract", "no video formats", "video is live",
	}
	for _, p := range permanentPatterns {
		if strings.Contains(errStr, p) {
			return false
		}
	}

	transientPatterns := []string{
		"timeout", "connection reset", "connection refused",
		"temporary failure", "fragment download failed",
		"no route to host", "network is unreachable",
		"i/o timeout", "broken pipe",
	}
	for _, p := range transientPatterns {
		if strings.Contains(errStr, p) {
			return true
		}
	}

	if strings.Contains(errStr, "http 429") || strings.Contains(errStr, "http 5") {
		return true
	}

	return false
}

// ── Token-set / Jaccard helpers ───────────────────────────────────────

// TokenSetForText builds a token set from raw text by lowercasing, cleaning,
// splitting, and filtering short/generic tokens.
func TokenSetForText(text string) map[string]struct{} {
	text = strings.ToLower(text)
	text = CleanYouTubeDescription(text)
	text = CleanClipTranscript(text)
	replacer := strings.NewReplacer(
		",", " ", ".", " ", "!", " ", "?", " ", ";", " ", ":", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "-", " ", "_", " ",
		"\"", " ", "'", " ", "/", " ", "\\", " ",
		"&", " ", "|", " ", "#", " ",
	)
	text = replacer.Replace(text)
	set := make(map[string]struct{})
	for _, word := range strings.Fields(text) {
		word = strings.TrimSpace(word)
		if len(word) < 3 {
			continue
		}
		if IsGenericToken(word) {
			continue
		}
		set[word] = struct{}{}
	}
	return set
}

// TokenSetFromStrings aggregates token sets from multiple string slices.
func TokenSetFromStrings(values ...[]string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, list := range values {
		for _, item := range list {
			for tok := range TokenSetForText(item) {
				set[tok] = struct{}{}
			}
		}
	}
	return set
}

// TextJaccardScore returns the Jaccard similarity of two texts after tokenization.
func TextJaccardScore(a, b string) float64 {
	return similarity.Jaccard(TokenSetForText(a), TokenSetForText(b))
}

// SliceJaccardScore returns the Jaccard similarity of two string slices
// after tokenization.
func SliceJaccardScore(a, b []string) float64 {
	return similarity.Jaccard(TokenSetFromStrings(a), TokenSetFromStrings(b))
}

// MergeStringSlices merges multiple string slices, normalizing each item
// via NormalizeSemanticText and deduplicating.
func MergeStringSlices(values ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, list := range values {
		for _, item := range list {
			norm := NormalizeSemanticText(item)
			if norm == "" {
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
