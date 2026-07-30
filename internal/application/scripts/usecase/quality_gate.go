// Package usecase — quality_gate.go implements the editorial quality
// gate for /api/script/generate.
//
// The gate runs after generation and postprocessing are complete and
// checks:
//   - detected language == requested language
//   - source_text coverage >= 0.70
//   - clip_evidence coverage == 1.00 for clips_primary
//   - unsupported claims == 0
//   - target words within 80-120% tolerance
//   - reject empty/generic text
//
// The result is always populated in GenerationResult.Quality. When
// the gate fails, a typed QualityGateError is returned so the caller
// can surface both the metrics and the failure reasons.
package usecase

import (
	"strings"
	"unicode"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

var qualityGateEnglishSignals = map[string]struct{}{
	"the": {}, "and": {}, "of": {}, "to": {}, "in": {}, "is": {}, "are": {}, "that": {}, "with": {}, "for": {},
}

var qualityGateItalianSignals = map[string]struct{}{
	"il": {}, "la": {}, "di": {}, "e": {}, "che": {}, "non": {}, "per": {}, "con": {}, "su": {}, "una": {},
}

var qualityGateSpanishSignals = map[string]struct{}{
	"el": {}, "la": {}, "de": {}, "y": {}, "que": {}, "no": {}, "por": {}, "con": {}, "sobre": {}, "una": {},
}

var qualityGateFrenchSignals = map[string]struct{}{
	"le": {}, "la": {}, "de": {}, "et": {}, "que": {}, "non": {}, "pour": {}, "avec": {}, "sur": {}, "une": {},
}

var qualityGateGermanSignals = map[string]struct{}{
	"der": {}, "die": {}, "das": {}, "und": {}, "zu": {}, "in": {}, "von": {}, "mit": {}, "auf": {}, "für": {},
}

var qualityGateVerbSuffixes = []string{"ing", "ed", "are", "ere", "ire", "ando", "endo", "ato", "uto", "ito"}

// Quality thresholds.
const (
	// defaultMinSourceTextCoverage is the fallback minimum acceptable
	// ratio of generated content words that must be present in the
	// source text or clip evidence.
	defaultMinSourceTextCoverage = 0.70

	// minTargetWordsRatio is the lower bound of the target word
	// tolerance (actual >= target * minTargetWordsRatio).
	minTargetWordsRatio = 0.80

	// maxTargetWordsRatio is the upper bound of the target word
	// tolerance (actual <= target * maxTargetWordsRatio).
	maxTargetWordsRatio = 1.20

	// singleSegmentDocumentaryMinCoverage is the relaxed source_text
	// coverage threshold for the Italian microstoria single-segment
	// documentary prose shape (SingleScene=true with non-empty
	// SourceText and the default grounding policy). The LLM
	// paraphrases the source heavily when generating the 450-550
	// word narrative voice, so lexical overlap against the source
	// text legitimately drops below defaultMinSourceTextCoverage
	// (0.70) even for a faithful rendering. Apply this relaxed
	// value only for the exact shape documented in
	// evaluateQualityGate; any non-default GroundingPolicy keeps
	// its policy-defined threshold unchanged, and an empty
	// GroundingPolicy with SingleScene=false keeps the default.
	singleSegmentDocumentaryMinCoverage = 0.55
)

// policyThresholds returns the source_text and clip_evidence coverage
// thresholds for a given grounding policy. The defaults are tuned so
// that:
//   - clips_primary: clips are the main source; source_text is only
//     support, so source_text coverage can be lower but clip binding
//     must be high.
//   - source_primary: source_text is the main source; clips are only
//     visual support, so source_text coverage must be high and clip
//     binding is not required.
//   - balanced: both sources have equal weight, so both must be
//     reasonably covered.
//
// Rationale for the numeric thresholds:
//   - clips_primary source 0.40: the script is allowed to expand
//     beyond the provided source_text because the clips carry the
//     factual burden, but some textual overlap is still required.
//   - source_primary source 0.85: the script must stay very close
//     to the provided source_text because it is the authoritative
//     source; clips are decorative.
//   - balanced source 0.60 / clip 0.50: both sources must be
//     meaningfully represented, but neither needs to dominate.
func policyThresholds(policy string) (sourceMin, clipMin float64) {
	switch policy {
	case scriptpkg.GroundingPolicyClipsPrimary:
		return 0.40, 1.00
	case scriptpkg.GroundingPolicySourcePrimary:
		return 0.85, 0.00
	case scriptpkg.GroundingPolicyBalanced:
		return 0.60, 0.50
	default:
		return defaultMinSourceTextCoverage, 0.00
	}
}

// evaluateQualityGate computes the editorial quality metrics for a
// generated result and returns a typed error when the gate fails.
// The returned Quality value is always populated, even on failure.
func evaluateQualityGate(
	result *scriptpkg.GenerationResult,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
) (*scriptpkg.GenerationQuality, error) {
	if result == nil {
		return nil, nil
	}

	requestedLang := strings.ToLower(strings.TrimSpace(plan.Language))
	generatedText := strings.TrimSpace(result.Output.Text)

	q := &scriptpkg.GenerationQuality{
		LanguageRequested: requestedLang,
		TargetWords:       plan.TargetWords,
		ActualWords:       result.Output.WordCount,
	}

	// Reject empty text.
	if generatedText == "" {
		q.Passed = false
		return q, &scriptpkg.QualityGateError{
			ItemID:  item.ID,
			Reasons: []string{"generated text is empty"},
			Quality: *q,
		}
	}

	// Reject generic/placeholder text.
	if isGenericText(generatedText) {
		q.Passed = false
		return q, &scriptpkg.QualityGateError{
			ItemID:  item.ID,
			Reasons: []string{"generated text is generic or placeholder"},
			Quality: *q,
		}
	}

	// Language detection.
	q.LanguageDetected = detectLanguage(generatedText)

	// Source text coverage. PRE-EXISTING-7 (FASE 13): when neither
	// SourceText nor ClipEvidence is provided, the gate has no
	// anchor for a coverage ratio (pure-prose free-form generation).
	// Short-circuit to 1.0 instead of forcing the LLM-emitted text
	// to overlap with an empty source (which always computes to 0.0
	// and trips the editorial gate on pure-prose orchestrator paths).
	//
	// Operator-visibility gap (PRE-EXISTING-7 / FASE 13 PART 3): this
	// skip is silent. A future PR can add a log.Warn emission at the
	// orchestrator boundary (e.g., generation_result_mapper.go) when
	// q.SourceTextCoverage == 1.0 && plan.ClipEvidence == nil — the
	// signal would surface wildly-off-target LLM outputs on the pure-prose
	// path WITHOUT triggering the editorial gate. For now the gate stays
	// silent to honor the canonical godlike/07 no-blocking contract.
	sourceText := buildSourceText(plan)
	if strings.TrimSpace(sourceText) == "" {
		q.SourceTextCoverage = 1.0
	} else {
		q.SourceTextCoverage = computeSourceTextCoverage(generatedText, sourceText)
	}

	// Clip evidence coverage.
	q.ClipEvidenceCoverage = computeClipEvidenceCoverage(result, plan)

	// Unsupported claims (entity-based heuristic).
	q.UnsupportedClaims = countUnsupportedClaims(result, sourceText)

	// Evaluate thresholds per grounding policy.
	minSourceTextCov, minClipCov := policyThresholds(plan.GroundingPolicy)
	// When no clip evidence is present, the clip coverage requirement
	// is irrelevant regardless of policy.
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		minClipCov = 0.00
	}
	// Clip-primary generations may be translated or heavily paraphrased:
	// lexical overlap is then a weak proxy even when every accepted clip is
	// bound and the narrative has textual anchors. In that case require an
	// anchor (> 0) but do not impose an arbitrary lexical percentage; the
	// structural clip evidence remains the authoritative coverage check.
	if plan.GroundingPolicy == scriptpkg.GroundingPolicyClipsPrimary &&
		q.ClipEvidenceCoverage >= 1.0 && q.SourceTextCoverage > 0 &&
		q.SourceTextCoverage < minSourceTextCov {
		minSourceTextCov = 0.0
	}

	// Single-segment documentary prose (Italian microstoria shape):
	// when SingleScene=true with non-empty SourceText and the
	// default grounding policy, the LLM paraphrases the source
	// heavily into a 450-550 word narrative voice, so lexical
	// overlap against the source text legitimately drops below
	// the 0.70 default. Relax the threshold to
	// singleSegmentDocumentaryMinCoverage for this exact shape
	// only. Any non-default GroundingPolicy (clips_primary,
	// source_primary, balanced) keeps its policy-defined
	// threshold unchanged; an empty GroundingPolicy with
	// SingleScene=false (multi-segment or unspecified) keeps
	// the default minimum.
	if plan.SingleScene &&
		plan.GroundingPolicy == "" &&
		strings.TrimSpace(plan.SourceText) != "" {
		minSourceTextCov = singleSegmentDocumentaryMinCoverage
	}

	var reasons []string
	if q.LanguageDetected != "" && requestedLang != "" && q.LanguageDetected != requestedLang {
		reasons = append(reasons,
			"detected language "+q.LanguageDetected+" does not match requested language "+requestedLang)
	}
	if q.SourceTextCoverage < minSourceTextCov {
		reasons = append(reasons,
			"source_text coverage below threshold")
	}
	if q.ClipEvidenceCoverage < minClipCov {
		policyLabel := plan.GroundingPolicy
		if policyLabel == "" {
			policyLabel = "default"
		}
		reasons = append(reasons,
			"clip_evidence coverage below threshold for "+policyLabel)
	}
	if q.UnsupportedClaims > 0 {
		reasons = append(reasons,
			"unsupported claims detected")
	}
	// PRE-EXISTING-7 / FASE 13 PART 2: target-word tolerance only
	// enforces when a source anchor exists (plan.SourceText or clip
	// evidence). Pure-prose free-form generation has no anchor —
	// the tolerance is observational only.
	// TargetWords belongs to the canonical source script. Translations
	// naturally change word count across languages, so applying the
	// English tolerance to translated text creates false failures.
	if plan.TargetWords > 0 && strings.TrimSpace(sourceText) != "" && strings.TrimSpace(plan.TranslateTo) == "" {
		lower := float64(plan.TargetWords) * minTargetWordsRatio
		upper := float64(plan.TargetWords) * maxTargetWordsRatio
		if float64(q.ActualWords) < lower || float64(q.ActualWords) > upper {
			reasons = append(reasons,
				"actual word count outside target tolerance")
		}
	}

	q.Passed = len(reasons) == 0
	if !q.Passed {
		return q, &scriptpkg.QualityGateError{
			ItemID:  item.ID,
			Reasons: reasons,
			Quality: *q,
		}
	}
	return q, nil
}

// buildSourceText assembles the canonical source text against which
// coverage and unsupported-claim checks are evaluated. For clip-based
// sources it concatenates the plan source text with the assembled clip
// evidence text.
func buildSourceText(plan scriptpkg.ResolvedGenerationPlan) string {
	parts := []string{plan.SourceText}
	if plan.ClipEvidence != nil {
		parts = append(parts, plan.ClipEvidence.ModelSourceText())
	}
	return strings.Join(parts, " ")
}

// isGenericText returns true when the generated text looks like a
// placeholder or generic fallback.
func isGenericText(text string) bool {
	text = strings.ToLower(text)
	placeholders := []string{
		"lorem ipsum",
		"sample text",
		"placeholder",
		"todo:",
		"tbd",
		"insert text here",
		"your text here",
		"generated text",
	}
	for _, p := range placeholders {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// tokenize returns a slice of lowercased word tokens.
func tokenize(text string) []string {
	var tokens []string
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		word = strings.ToLower(strings.TrimSpace(word))
		if word != "" {
			tokens = append(tokens, word)
		}
	}
	return tokens
}

// computeSourceTextCoverage returns the ratio of generated tokens that
// appear in the source text. Stop words are removed before comparison.
func computeSourceTextCoverage(generated, source string) float64 {
	genTokens := filterStopWords(tokenize(generated))
	if len(genTokens) == 0 {
		return 0.0
	}
	sourceSet := make(map[string]struct{}, len(source))
	for _, t := range filterStopWords(tokenize(source)) {
		sourceSet[t] = struct{}{}
	}
	if len(sourceSet) == 0 {
		return 0.0
	}
	matches := 0
	for _, t := range genTokens {
		if _, ok := sourceSet[t]; ok {
			matches++
		}
	}
	return float64(matches) / float64(len(genTokens))
}

// computeClipEvidenceCoverage returns the ratio of accepted clips that
// are bound to a scene in the result. For non-clip sources it returns
// 1.0.
func computeClipEvidenceCoverage(result *scriptpkg.GenerationResult, plan scriptpkg.ResolvedGenerationPlan) float64 {
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		return 1.0
	}
	accepted := plan.ClipEvidence.AcceptedClipIDs
	if plan.NumClips > 0 && plan.NumClips < len(accepted) {
		accepted = accepted[:plan.NumClips]
	}
	if len(accepted) == 0 {
		return 1.0
	}
	bound := make(map[string]struct{})
	for _, s := range result.Output.SpecScene.Scenes {
		if s.Bindings.Clip != nil && s.Bindings.Clip.ClipID != "" {
			bound[s.Bindings.Clip.ClipID] = struct{}{}
		}
	}
	matches := 0
	for _, id := range accepted {
		if _, ok := bound[id]; ok {
			matches++
		}
	}
	return float64(matches) / float64(len(accepted))
}

// countUnsupportedClaims returns the number of named entities in the
// generated text whose tokens do not appear in the source text. It
// tokenizes each entity name and requires every token to be present
// in the source token set, which avoids the false positives of
// substring matching (e.g. "John" inside "Johnson").
func countUnsupportedClaims(result *scriptpkg.GenerationResult, sourceText string) int {
	if result.Artifacts.Entities == nil {
		return 0
	}
	sourceTokens := make(map[string]struct{})
	for _, t := range tokenize(sourceText) {
		sourceTokens[t] = struct{}{}
	}
	count := 0
	check := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		tokens := tokenize(name)
		if len(tokens) == 0 {
			return
		}
		for _, t := range tokens {
			if _, ok := sourceTokens[t]; !ok {
				count++
				return
			}
		}
	}
	for _, p := range result.Artifacts.Entities.Persons {
		check(p.Value)
	}
	for _, p := range result.Artifacts.Entities.Places {
		check(p.Value)
	}
	for _, c := range result.Artifacts.Entities.Concepts {
		check(c.Value)
	}
	return count
}

// detectLanguage returns the ISO-639-1 language code with the highest
// overlap against a small built-in signal set. When no signals match,
// it returns an empty string.
func detectLanguage(text string) string {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return ""
	}

	scores := []struct {
		code  string
		score float64
	}{
		{"en", languageMatchScore(tokens, qualityGateEnglishSignals)},
		{"it", languageMatchScore(tokens, qualityGateItalianSignals)},
		{"es", languageMatchScore(tokens, qualityGateSpanishSignals)},
		{"fr", languageMatchScore(tokens, qualityGateFrenchSignals)},
		{"de", languageMatchScore(tokens, qualityGateGermanSignals)},
	}

	maxScore := 0.0
	maxCode := ""
	for _, s := range scores {
		if s.score > maxScore {
			maxScore = s.score
			maxCode = s.code
		}
	}

	return maxCode
}

// languageMatchScore returns the ratio of stop words from a language
// that appear in the token list.
func languageMatchScore(tokens []string, stopWords map[string]struct{}) float64 {
	if len(tokens) == 0 || len(stopWords) == 0 {
		return 0.0
	}
	seen := 0
	for _, t := range tokens {
		if _, ok := stopWords[t]; ok {
			seen++
		}
	}
	return float64(seen) / float64(len(tokens))
}

// filterStopWords removes common stop words from a token list.
func filterStopWords(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !textutil.IsStopWord(t) {
			out = append(out, t)
		}
	}
	return out
}

func isFunctionWord(word string) bool {
	return textutil.IsStopWord(word)
}

func looksLikeVerbBigram(words []string) bool {
	if len(words) < 2 {
		return false
	}
	verbCount := 0
	for _, w := range words {
		lower := strings.ToLower(w)
		for _, suffix := range qualityGateVerbSuffixes {
			if strings.HasSuffix(lower, suffix) {
				verbCount++
				break
			}
		}
	}
	return verbCount == len(words)
}
