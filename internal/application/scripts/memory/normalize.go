package memory

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	// stopWords for token extraction (English + Italian common words)
	stopWords = map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "from": true, "is": true, "was": true, "are": true,
		"be": true, "been": true, "being": true, "have": true, "has": true, "had": true,
		"do": true, "does": true, "did": true, "will": true, "would": true, "could": true,
		"should": true, "may": true, "might": true, "shall": true, "can": true,
		"this": true, "that": true, "these": true, "those": true, "it": true, "its": true,
		"not": true, "no": true, "nor": true, "so": true, "if": true, "then": true,
		"than": true, "too": true, "very": true, "just": true, "about": true,
		// Italian
		"il": true, "la": true, "gli": true, "le": true, "un": true, "una": true,
		"di": true, "da": true, "con": true, "su": true, "per": true,
		"tra": true, "fra": true, "che": true, "non": true, "si": true, "è": true,
		"sono": true, "ha": true, "come": true, "anche": true, "più": true, "già": true,
		"del": true, "dello": true, "della": true, "dei": true, "degli": true, "delle": true,
		"al": true, "allo": true, "alla": true, "ai": true, "agli": true, "alle": true,
		"nel": true, "nello": true, "nella": true, "nei": true, "negli": true, "nelle": true,
	}

	// nonAlpha strips everything except letters/numbers/spaces
	nonAlpha = regexp.MustCompile(`[^a-z0-9\s]`)
)

// NormalizeInput produces a lowercase, whitespace-collapsed, punctuation-stripped version of the input.
func NormalizeInput(channelID, title, prompt string) string {
	raw := strings.ToLower(channelID + " " + title + " " + prompt)
	// Remove URLs
	raw = regexp.MustCompile(`https?://\S+`).ReplaceAllString(raw, "")
	// Strip punctuation
	raw = nonAlpha.ReplaceAllString(raw, " ")
	// Collapse whitespace
	raw = strings.Join(strings.Fields(raw), " ")
	return strings.TrimSpace(raw)
}

// HashInput computes a SHA-256 hex digest from channel_id + mode + normalized input.
func HashInput(channelID, mode, normalized string) string {
	data := channelID + "|" + mode + "|" + normalized
	h := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", h)
}

// BuildTopicKey extracts a short topic identifier from title + prompt.
// Produces a slug like "caitlin-clark-wnba-impact".
func BuildTopicKey(title, prompt string) string {
	raw := strings.ToLower(title + " " + prompt)
	raw = nonAlpha.ReplaceAllString(raw, " ")
	tokens := ExtractSearchTokens(raw)
	if len(tokens) > 6 {
		tokens = tokens[:6]
	}
	return strings.Join(tokens, "-")
}

// ExtractSearchTokens returns deduplicated, meaningful tokens from text.
// Tokens shorter than 3 chars or in the stop-word list are filtered out.
func ExtractSearchTokens(text string) []string {
	raw := strings.ToLower(text)
	raw = nonAlpha.ReplaceAllString(raw, " ")
	seen := map[string]bool{}
	var tokens []string
	for _, tok := range strings.Fields(raw) {
		if len(tok) < 3 {
			continue
		}
		if stopWords[tok] {
			continue
		}
		// Simple stemming: trim trailing s/v/i/le for dedup (very rough)
		stemmed := simpleStem(tok)
		if seen[stemmed] {
			continue
		}
		seen[stemmed] = true
		tokens = append(tokens, tok)
	}
	return tokens
}

// simpleStem does a conservative suffix trim to improve dedup.
// Only trims well-known suffixes; never strips arbitrary trailing characters.
func simpleStem(word string) string {
	if len(word) <= 4 {
		return word
	}
	for _, suffix := range []string{"ing", "tion", "sion", "ment", "ness", "ized", "ised", "able", "ful", "less", "ous", "ive"} {
		if strings.HasSuffix(word, suffix) && len(word)-len(suffix) >= 3 {
			return word[:len(word)-len(suffix)]
		}
	}
	return word
}

// ChunkScript splits a script text into paragraph-level chunks for drive.
func ChunkScript(text string, maxChunkLen int) []string {
	if maxChunkLen <= 0 {
		maxChunkLen = 500
	}
	paragraphs := strings.Split(text, "\n\n")
	var chunks []string
	var current strings.Builder
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if current.Len() > 0 && current.Len()+len(p)+2 > maxChunkLen {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(p)
	}
	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}
	return chunks
}

// NormalizeSearchText produces a lowercase, stripped text suitable for LIKE matching.
func NormalizeSearchText(text string) string {
	raw := strings.ToLower(text)
	// Keep only alphanumeric and spaces
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// TitleMatchScore computes a simple token-overlap score between two texts (0.0–1.0).
func TitleMatchScore(a, b string) float64 {
	tokensA := map[string]bool{}
	for _, t := range ExtractSearchTokens(a) {
		tokensA[t] = true
	}
	tokensB := map[string]bool{}
	for _, t := range ExtractSearchTokens(b) {
		tokensB[t] = true
	}
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0
	}
	var overlap int
	for t := range tokensA {
		if tokensB[t] {
			overlap++
		}
	}
	// Jaccard-like but weighted by smaller set
	denom := len(tokensA)
	if len(tokensB) < denom {
		denom = len(tokensB)
	}
	return float64(overlap) / float64(denom)
}
