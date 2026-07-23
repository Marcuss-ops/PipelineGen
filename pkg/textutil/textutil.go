// Package textutil provides common text processing utilities used across the codebase.
package textutil

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var genericStopWords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "and": {}, "or": {}, "but": {}, "for": {}, "with": {},
	"from": {}, "into": {}, "onto": {}, "about": {}, "above": {}, "across": {}, "after": {},
	"against": {}, "along": {}, "among": {}, "around": {}, "at": {}, "before": {}, "behind": {},
	"below": {}, "beneath": {}, "between": {}, "beyond": {}, "by": {}, "down": {}, "during": {},
	"except": {}, "inside": {}, "instead": {}, "near": {}, "off": {}, "on": {}, "over": {},
	"through": {}, "to": {}, "toward": {}, "towards": {}, "under": {}, "until": {}, "upon": {},
	"within": {}, "without": {}, "is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {},
	"being": {}, "have": {}, "has": {}, "had": {}, "do": {}, "does": {}, "did": {}, "will": {},
	"would": {}, "could": {}, "should": {}, "may": {}, "might": {}, "must": {}, "shall": {},
	"can": {}, "cannot": {}, "not": {}, "no": {}, "this": {}, "that": {}, "these": {}, "those": {},
	"what": {}, "which": {}, "who": {}, "whom": {}, "whose": {}, "where": {}, "when": {}, "why": {},
	"how": {}, "all": {}, "any": {}, "both": {}, "each": {}, "few": {}, "more": {}, "most": {},
	"other": {}, "some": {}, "such": {}, "only": {}, "own": {}, "same": {}, "so": {}, "than": {},
	"too": {}, "very": {}, "just": {}, "now": {}, "then": {}, "here": {}, "there": {}, "if": {},
	"as": {}, "of": {}, "in": {}, "it": {}, "i": {},
	"il": {}, "lo": {}, "la": {}, "gli": {}, "le": {}, "un": {}, "uno": {}, "una": {},
	"dei": {}, "degli": {}, "della": {}, "delle": {}, "dello": {}, "di": {}, "da": {},
	"con": {}, "su": {}, "per": {}, "tra": {}, "fra": {}, "al": {}, "allo": {}, "alla": {},
	"ai": {}, "agli": {}, "alle": {}, "dal": {}, "dallo": {}, "dalla": {}, "dai": {}, "dagli": {},
	"dalle": {}, "nel": {}, "nello": {}, "nella": {}, "nei": {}, "negli": {}, "nelle": {}, "col": {},
	"coi": {}, "sul": {}, "sulla": {}, "sui": {}, "sulle": {}, "e": {}, "che": {}, "ma": {}, "se": {},
	"anche": {}, "più": {}, "meno": {}, "quando": {}, "dove": {}, "perché": {}, "chi": {}, "cosa": {},
	"quale": {}, "quali": {}, "tutto": {}, "tutti": {}, "ogni": {}, "qualche": {}, "molto": {},
	"poco": {}, "troppo": {}, "abbastanza": {},
}

// Slugify converts a string to a lowercase slug with hyphens.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	b := make([]rune, 0, len(s))
	prevDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b = append(b, r)
			prevDash = false
		} else {
			if !prevDash {
				b = append(b, '-')
				prevDash = true
			}
		}
	}
	return strings.Trim(string(b), "-")
}

// SlugifyWithMax is like Slugify but truncates to maxLen runes.
func SlugifyWithMax(s string, maxLen int) string {
	s = Slugify(s)
	if maxLen > 0 {
		r := []rune(s)
		if len(r) > maxLen {
			s = string(r[:maxLen])
			s = strings.TrimRight(s, "-")
		}
	}
	return s
}

// SafeName replaces filesystem-unsafe characters with spaces and returns a trimmed result.
func SafeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}
	name = strings.NewReplacer(
		"/", " ", "\\", " ", ":", " ", "*", " ", "?", " ",
		"\"", " ", "<", " ", ">", " ", "|", " ", "@", " ",
		"#", " ", "$", " ", "%", " ", "^", " ", "&", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "{", " ",
		"}", " ", "'", " ", "`", " ", "~", " ", "!", " ",
		";", " ", ",", " ", "-", " ", "_", " ", ".", " ",
	).Replace(name)
	result := strings.Join(strings.Fields(name), " ")
	if result == "" {
		return "untitled"
	}
	return result
}

// SanitizeFilename removes potentially dangerous characters from a filename.
func SanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")
	name = strings.ReplaceAll(name, "\x00", "")
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-' || c == ' ') {
			name = name[:i] + name[i+1:]
			i--
		}
	}
	name = strings.TrimSpace(name)
	if len(name) > 255 {
		name = name[:255]
	}
	if name == "" {
		name = "unnamed"
	}
	return name
}

// Truncate returns a truncated string with '...' if it exceeds length n.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

// CountWords returns the number of words in a text.
func CountWords(text string) int {
	return len(strings.Fields(strings.TrimSpace(text)))
}

// FirstNonEmpty returns the first non-empty (after trim) string.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ContainsCI reports whether substr is within s, case-insensitively.
func ContainsCI(s, substr string) bool {
	if substr == "" {
		return false
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// SplitCSV splits a comma-separated string into a trimmed slice.
func SplitCSV(text string) []string {
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ",")
	var result []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

// NormalizeStringSlice normalizes a slice of strings (trim, lowercase, filter empty).
func NormalizeStringSlice(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		tag = strings.ToLower(tag)
		out = append(out, tag)
	}
	return out
}

// Tokenize splits text into tokens using unicode-aware word boundaries.
func Tokenize(text string) []string {
	text = strings.ToLower(text)
	return strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// ── VTT Subtitle Helpers ────────────────────────────────────────────────

// ParseVTTTimestamp parses a VTT/SSA timestamp string to seconds as float64.
func ParseVTTTimestamp(ts string) float64 {
	ts = strings.TrimSpace(ts)
	parts := strings.Split(ts, ":")
	if len(parts) == 3 {
		var h, m, s float64
		fmt.Sscanf(parts[0], "%f", &h)
		fmt.Sscanf(parts[1], "%f", &m)
		fmt.Sscanf(parts[2], "%f", &s)
		return h*3600 + m*60 + s
	} else if len(parts) == 2 {
		var m, s float64
		fmt.Sscanf(parts[0], "%f", &m)
		fmt.Sscanf(parts[1], "%f", &s)
		return m*60 + s
	}
	return 0
}

// FormatSecondsToTimestamp converts seconds to HH:MM:SS format.
func FormatSecondsToTimestamp(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// CleanSubtitleText removes HTML/VTT tags from subtitle text.
func CleanSubtitleText(text string) string {
	text = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	return text
}

// ParseTimestamp parses a timestamp string to seconds.
func ParseTimestamp(ts string) (int, error) {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return 0, fmt.Errorf("empty timestamp")
	}
	parts := strings.Split(ts, ":")
	switch len(parts) {
	case 3:
		var h, m, s int
		if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
			return 0, err
		}
		if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
			return 0, err
		}
		if _, err := fmt.Sscanf(parts[2], "%d", &s); err != nil {
			return 0, err
		}
		return h*3600 + m*60 + s, nil
	case 2:
		var m, s int
		if _, err := fmt.Sscanf(parts[0], "%d", &m); err != nil {
			return 0, err
		}
		if _, err := fmt.Sscanf(parts[1], "%d", &s); err != nil {
			return 0, err
		}
		return m*60 + s, nil
	case 1:
		var seconds int
		if _, err := fmt.Sscanf(ts, "%d", &seconds); err != nil {
			return 0, err
		}
		return seconds, nil
	default:
		return 0, fmt.Errorf("invalid timestamp format: %s", ts)
	}
}

// ── Markdown / Voiceover Cleaning ───────────────────────────────────────

var (
	voReHeadingMarker    = regexp.MustCompile(`(?m)^#+\s+`)
	voReTrailingHash     = regexp.MustCompile(`\s+#+\s+`)
	voReHorizontalRule   = regexp.MustCompile(`(?m)^[\-\_\*\=]{3,}\s*$`)
	voReBoldMarker       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	voReItalicMarker     = regexp.MustCompile(`(?:^|\s)\*([^*\s][^*]*?)\*(?:\s|$|[.,;!?])`)
	voReBracketArtifact  = regexp.MustCompile(`\[[^\]]*\]`)
	voReBlockquoteMarker = regexp.MustCompile(`(?m)^>+\s*`)
	voReChapterLabel     = regexp.MustCompile(`(?mi)^(?:Table\s+of\s+Contents|Item|Chapter|Parte|Capitolo|Chapitre|Capítulo|Kapitel)\s+\d+[\.:]?\s*`)
	voReMultipleNewlines = regexp.MustCompile(`\n{3,}`)
	voReMultipleSpaces   = regexp.MustCompile(` {2,}`)
)

// CleanForVoiceover strips markdown formatting artifacts and structural labels.
func CleanForVoiceover(text string) string {
	text = voReHeadingMarker.ReplaceAllString(text, "")
	text = voReTrailingHash.ReplaceAllString(text, " ")
	text = voReHorizontalRule.ReplaceAllString(text, "")
	text = voReBoldMarker.ReplaceAllString(text, "$1")
	text = voReItalicMarker.ReplaceAllString(text, "$1")
	text = voReBracketArtifact.ReplaceAllString(text, "")
	text = voReBlockquoteMarker.ReplaceAllString(text, "")
	text = voReChapterLabel.ReplaceAllString(text, "")
	text = voReMultipleNewlines.ReplaceAllString(text, "\n\n")
	text = voReMultipleSpaces.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

// ── Script Sentence Splitting ───────────────────────────────────────────

var scriptSentenceRe = regexp.MustCompile(`(?m)([^.!?]+[.!?]+|[^.!?]+$)`)

// SplitScriptSentences splits script text into sentences for scene generation.
func SplitScriptSentences(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	parts := scriptSentenceRe.FindAllString(text, -1)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "\u2022-* \t")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// BuildSceneQuery builds the canonical query used for image matching and clip recommendation.
func BuildSceneQuery(sentence, topic, style, language string) string {
	parts := []string{strings.TrimSpace(sentence)}
	if t := strings.TrimSpace(topic); t != "" {
		parts = append(parts, t)
	}
	if s := strings.TrimSpace(style); s != "" {
		parts = append(parts, s)
	}
	if l := strings.TrimSpace(language); l != "" {
		parts = append(parts, l)
	}
	return strings.Join(parts, " | ")
}

// ExtractJSONArray attempts to find and extract the first JSON array from a string.
func ExtractJSONArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}

// Float64To32 converts a slice of float64 to float32.
func Float64To32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

// IsStopWord checks if a term is a common stop word. It uses a compact
// built-in set so text processing stays dependency-free and does not
// require any external lexicon registry.
func IsStopWord(term string) bool {
	_, ok := genericStopWords[strings.ToLower(term)]
	return ok
}

// IsStopWordForLanguage is kept for call sites that still pass a
// language tag. The current implementation intentionally ignores the
// language and uses the generic stop-word set.
func IsStopWordForLanguage(term string, language string) bool {
	_ = language
	return IsStopWord(term)
}

// ── Script text stripping ───────────────────────────────────────────────

// StripNarrationMarkerRe matches narration markers like [NARRATION] or [NARRATORE].
var StripNarrationMarkerRe = regexp.MustCompile(`(?i)\[(?:narration|narratore|narrador|narrateur|erzähler|narracja|narratione)\]`)

// StripClipMarkerRe matches clip markers like [CLIP:...] or [VIDEO:...].
var StripClipMarkerRe = regexp.MustCompile(`(?i)\[(?:clip|video|film|media):[^\]]+\]`)

// TokenizeWithStopWords removes stop words from tokenization.
func TokenizeWithStopWords(text string) []string {
	tokens := Tokenize(text)
	result := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if len(tok) >= 3 && !IsStopWord(tok) {
			result = append(result, tok)
		}
	}
	return result
}

// UniqueStringsVar returns a deduplicated slice preserving first-occurrence order.
func UniqueStringsVar(items ...string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

// LangFullName returns the full language name for a language code.
func LangFullName(code string) string {
	names := map[string]string{
		"it": "Italian", "es": "Spanish", "fr": "French", "de": "German",
		"pt": "Portuguese", "nl": "Dutch", "pl": "Polish", "ru": "Russian",
		"ja": "Japanese", "zh": "Chinese", "ko": "Korean", "ar": "Arabic",
		"hi": "Hindi", "tr": "Turkish", "sv": "Swedish", "da": "Danish",
		"fi": "Finnish", "no": "Norwegian", "cs": "Czech", "ro": "Romanian",
		"hu": "Hungarian", "el": "Greek", "he": "Hebrew", "th": "Thai",
		"vi": "Vietnamese", "id": "Indonesian", "ms": "Malay",
	}
	if name, ok := names[code]; ok {
		return name
	}
	return code
}
