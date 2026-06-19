package scripts

import (
	"strings"
	"unicode"
)

// ValidationScore holds a single post-generation quality score.
type ValidationScore struct {
	Check   string `json:"check"`             // e.g. "word_count_ok", "no_markdown"
	Passed  bool   `json:"passed"`            // true = OK, false = needs attention
	Value   int    `json:"value,omitempty"`   // numeric value (word count, repetition count)
	Message string `json:"message,omitempty"` // human-readable detail
}

// ValidateResult holds the full set of post-generation checks.
type ValidateResult struct {
	Scores  []ValidationScore `json:"scores"`
	AllPass bool              `json:"all_pass"`
}

// ValidateScript runs all post-generation checks against the script and plan.
// It never blocks on an LLM call — all checks are rule-based / statistical.
// Returns a ValidateResult with per-check scores.
func ValidateScript(script string, plan *ScriptGenerationPlan) *ValidateResult {
	if script == "" {
		return &ValidateResult{AllPass: false}
	}

	scores := []ValidationScore{
		checkWordCount(script, plan),
		checkNoMarkdown(script),
		checkNoStageDirections(script),
		checkRepetition(script),
		checkHookStrength(script),
		checkCTAPresent(script),
	}

	allPass := true
	for _, s := range scores {
		if !s.Passed {
			allPass = false
		}
	}

	return &ValidateResult{Scores: scores, AllPass: allPass}
}

// ── Individual checks ──────────────────────────────────────────────────────

// checkWordCount verifies the script length is within tolerance.
func checkWordCount(script string, plan *ScriptGenerationPlan) ValidationScore {
	words := len(strings.Fields(strings.TrimSpace(script)))
	if words == 0 {
		return ValidationScore{
			Check: "word_count_ok", Passed: false, Value: 0,
			Message: "script is empty",
		}
	}

	target := plan.TargetWords
	if target <= 0 && plan.Duration > 0 {
		target = CalculateTargetWords(plan.Duration, plan.DurationMin)
	}
	if target <= 0 {
		// No target configured — just report the count
		return ValidationScore{
			Check: "word_count_ok", Passed: true, Value: words,
			Message: "no target configured",
		}
	}

	minWords, maxWords := WordCountBounds(target)
	passed := words >= minWords && words <= maxWords
	msg := ""
	if !passed {
		if words < minWords {
			msg = "too short"
		} else {
			msg = "too long"
		}
	}

	return ValidationScore{
		Check: "word_count_ok", Passed: passed,
		Value: words, Message: msg,
	}
}

// checkNoMarkdown verifies the script does not contain markdown formatting.
func checkNoMarkdown(script string) ValidationScore {
	mdPatterns := []string{"```", "**", "__", "# ", "## ", "### "}
	for _, p := range mdPatterns {
		if strings.Contains(script, p) {
			return ValidationScore{
				Check: "no_markdown", Passed: false, Message: "contains markdown formatting",
			}
		}
	}
	return ValidationScore{Check: "no_markdown", Passed: true}
}

// checkNoStageDirections verifies the script does not contain [stage directions]
// or [timestamp markers]. Bracketed text is a strong indicator of LLM
// artifacts that should be stripped before voiceover / Doc output.
func checkNoStageDirections(script string) ValidationScore {
	hasOpenBracket := strings.Contains(script, "[")
	hasCloseBracket := strings.Contains(script, "]")
	if hasOpenBracket && hasCloseBracket {
		return ValidationScore{
			Check: "no_stage_directions", Passed: false,
			Message: "contains bracketed text (stage directions / timestamps)",
		}
	}
	return ValidationScore{Check: "no_stage_directions", Passed: true}
}

// checkRepetition detects repeated 4-grams (sequences of 4 words) that
// appear more than once in the script. Repeated 4-grams are a strong
// signal of templated or copy-pasted content.
//
// The threshold scales with script length to avoid false positives on
// long-form single-topic content where natural phrases repeat.
func checkRepetition(script string) ValidationScore {
	words := strings.Fields(strings.TrimSpace(script))
	if len(words) < 8 {
		return ValidationScore{Check: "no_repetition", Passed: true}
	}

	seen := make(map[string]int)
	for i := 0; i < len(words)-3; i++ {
		ngram := strings.Join(words[i:i+4], " ")
		seen[ngram]++
	}

	repeated := 0
	for _, count := range seen {
		if count > 1 {
			repeated++
		}
	}

	threshold := repetitionThreshold(len(words))
	passed := repeated <= threshold
	msg := ""
	if !passed {
		msg = "repeated 4-grams detected"
	}

	return ValidationScore{
		Check: "no_repetition", Passed: passed,
		Value: repeated, Message: msg,
	}
}

// repetitionThreshold returns the maximum number of unique repeated 4-grams
// allowed for a script of the given word count. Scales linearly: 1 per 100
// words, with a minimum of 3.
func repetitionThreshold(wordCount int) int {
	const base = 3
	if wordCount <= 200 {
		return base
	}
	scaled := wordCount / 100
	if scaled < base {
		return base
	}
	return scaled
}

// genericOpeners lists weak opening phrases that should be flagged.
var genericOpeners = []string{
	"in today's world", "in today world",
	"since the beginning of time", "since the dawn of time",
	"have you ever wondered", "there are many",
	"the world is full of", "let's face it",
	"in this video", "welcome to",
}

// checkHookStrength verifies the first sentence is not a generic opener.
func checkHookStrength(script string) ValidationScore {
	first := strings.TrimSpace(script)
	// Take the first sentence or first 120 chars
	if idx := strings.IndexAny(first, ".!?"); idx != -1 {
		first = first[:idx+1]
	}
	if len(first) > 120 {
		first = first[:120]
	}
	firstLower := strings.ToLower(first)

	for _, opener := range genericOpeners {
		if strings.Contains(firstLower, opener) {
			return ValidationScore{
				Check: "hook_strength", Passed: false,
				Message: "starts with a generic opener: \"" + opener + "\"",
			}
		}
	}
	return ValidationScore{Check: "hook_strength", Passed: true}
}

// ctaKeywords lists call-to-action phrases commonly found in strong closings.
var ctaKeywords = []string{
	"subscribe", "follow", "share",
	"let us know", "comment below",
	"next video", "watch next",
	"join", "sign up",
}

// checkCTAPresent verifies the last 2 sentences contain a call-to-action.
func checkCTAPresent(script string) ValidationScore {
	trimmed := strings.TrimSpace(script)
	if len(trimmed) < 50 {
		return ValidationScore{Check: "cta_present", Passed: false, Message: "script too short"}
	}

	// Take the last ~200 characters as the closing
	closing := trimmed
	if len(closing) > 200 {
		closing = closing[len(closing)-200:]
	}
	closingLower := strings.ToLower(closing)

	// Also check the last sentence specifically
	lastSentence := ""
	if idx := strings.LastIndexAny(trimmed, ".!?"); idx != -1 && idx < len(trimmed)-1 {
		lastSentence = strings.TrimSpace(trimmed[idx+1:])
	}
	lastLower := strings.ToLower(lastSentence)

	for _, kw := range ctaKeywords {
		if strings.Contains(closingLower, kw) {
			// Ensure it's not part of a larger word
			idx := strings.Index(closingLower, kw)
			end := idx + len(kw)
			beforeOK := idx == 0 || !isWordChar(rune(closingLower[idx-1]))
			afterOK := end >= len(closingLower) || !isWordChar(rune(closingLower[end]))
			if beforeOK && afterOK {
				return ValidationScore{Check: "cta_present", Passed: true}
			}
		}
	}

	// Check last sentence specifically
	for _, kw := range ctaKeywords {
		if strings.Contains(lastLower, kw) {
			return ValidationScore{Check: "cta_present", Passed: true}
		}
	}

	return ValidationScore{Check: "cta_present", Passed: false, Message: "no call-to-action in closing"}
}

// isWordChar returns true for letters and digits — characters that form
// part of a word. Used by checkCTAPresent to avoid matching keywords that
// are substrings of longer words (e.g. "share" matched inside "bushare").
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
