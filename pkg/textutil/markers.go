package textutil

import (
	"regexp"
	"strings"
)

// ── Script Clip / Narration Markers ─────────────────────────────────────

// ClipMarkerRe matches `[Clip: <id>]` at the start of a line, capturing the
// clip ID in group 1. The capture is `.*?` (zero or more) so whitespace-only
// IDs like `[Clip:   ]` are matched; callers should flag empty captures.
// Used by script_validator to parse generated scripts into scene blocks.
var ClipMarkerRe = regexp.MustCompile(`(?m)^\[Clip:\s*(.*?)\s*\]\s*$`)

// NarrationMarkerRe matches `[Narration: <role>]` at the start of a line,
// capturing the role name in group 1. Role must be [a-z_]+.
// Used by script_validator to parse generated scripts into scene blocks.
var NarrationMarkerRe = regexp.MustCompile(`(?m)^\[Narration:\s*([a-z_]+)\s*\]\s*$`)

// StripClipMarkerRe matches `[Clip: ...]` anywhere on a line (leading
// whitespace allowed). Does NOT capture the ID — intended for stripping
// markers from text previews and display output.
var StripClipMarkerRe = regexp.MustCompile(`(?m)^\s*\[Clip:\s*[^\]]+\s*\]\s*`)

// StripNarrationMarkerRe matches `[Narration: ...]` anywhere on a line
// (leading whitespace allowed). Does NOT capture the role — intended for
// stripping markers from text previews and display output.
var StripNarrationMarkerRe = regexp.MustCompile(`(?m)^\s*\[Narration:\s*[a-z_]+\s*\]\s*`)

// ── Language Detection Markers ──────────────────────────────────────────

// EnMarkers are common English function words used for interference
// detection when checking if translated text is actually in the target
// language (not leaked English from the source).
var EnMarkers = []string{" the ", " and ", " is ", " are ", " in ", " to ", " of "}

// LanguageMarkers defines per-language high-frequency stopwords/constructs
// used by LooksTranslated to verify a translated text is actually in the
// target language.
var LanguageMarkers = map[string][]string{
	"it": {" il ", " la ", " le ", " gli ", " che ", " è ", " sono ", " una ", " del ", " della ", " con ", " per ", " non ", " dei ", " delle ", " nel ", " sul "},
	"es": {" el ", " la ", " los ", " las ", " que ", " es ", " son ", " una ", " del ", " por ", " para ", " con ", " su ", " como ", " más ", " entre "},
	"fr": {" le ", " la ", " les ", " que ", " est ", " sont ", " une ", " du ", " de la ", " pour ", " dans ", " avec ", " sur ", " des ", " ce ", " cette "},
	"de": {" der ", " die ", " das ", " ist ", " sind ", " ein ", " eine ", " des ", " dem ", " den ", " mit ", " auf ", " für ", " wird ", " werden ", " nicht "},
	"pt": {" o ", " a ", " os ", " as ", " que ", " é ", " são ", " uma ", " do ", " da ", " para ", " com ", " como ", " mais ", " dos ", " das "},
	"nl": {" de ", " het ", " een ", " is ", " zijn ", " met ", " voor ", " op ", " in ", " van ", " het ", " wordt ", " geen ", " ook "},
	"pl": {" się ", " w ", " na ", " z ", " do ", " jest ", " to ", " nie ", " i ", " że ", " jako ", " przez "},
	"ru": {" и ", " в ", " не ", " на ", " с ", " что ", " это ", " как ", " его ", " по ", " но ", " из ", " от "},
	"ja": {" の ", " を ", " は ", " が ", " に ", " た ", " する ", " いる ", " ある ", " て "},
	"zh": {" 的 ", " 是 ", " 在 ", " 了 ", " 不 ", " 和 ", " 有 ", " 就 ", " 人 ", " 都 ", " 一 "},
	"ko": {" 이 ", " 가 ", " 을 ", " 를 ", " 는 ", " 은 ", " 에 ", " 의 ", " 로 ", " 하다 ", " 있다 "},
	"ar": {" في ", " من ", " على ", " إلى ", " عن ", " كان ", " مع ", " هذا ", " لا ", " أن "},
	"tr": {" bir ", " bu ", " ve ", " ile ", " için ", " ki ", " olan ", " daha ", " çok ", " gibi "},
}

// LooksTranslated returns true if the translated text appears to actually be
// in the target language (not still in the original source language). It uses
// a tiered confidence system:
//   - High confidence (>=4 target markers): definitely translated
//   - Medium confidence (2-3 target markers): check English and source interference
//   - Low confidence (<2 target markers): probably untranslated
//
// sourceLang is the original language (e.g. "it") — when set, source language
// markers in the translated text reduce confidence, catching cases where the
// model echoes the source.
func LooksTranslated(text, targetLang, sourceLang string) bool {
	sample := strings.ToLower(text)
	if len(sample) < 20 {
		return false
	}
	// Take first 300 chars for analysis
	if len(sample) > 300 {
		sample = sample[:300]
	}

	targetMarkers, targetOk := LanguageMarkers[targetLang]
	if !targetOk {
		return true // unknown language, assume it's fine
	}

	// Count target language markers
	targetFound := 0
	for _, m := range targetMarkers {
		if strings.Contains(sample, m) {
			targetFound++
			// High confidence: 4+ markers found — definitely translated
			if targetFound >= 4 {
				return true
			}
		}
	}

	// Tier 2: Medium confidence (2-3 markers) — check for interference
	if targetFound >= 2 {
		// Check English interference
		englishHits := 0
		for _, m := range EnMarkers {
			if strings.Contains(sample, m) {
				englishHits++
			}
		}

		// Check source language interference (if different from target)
		sourceHits := 0
		if sourceLang != "" && sourceLang != targetLang {
			if sourceMarkers, sourceOk := LanguageMarkers[sourceLang]; sourceOk {
				for _, m := range sourceMarkers {
					if strings.Contains(sample, m) {
						sourceHits++
					}
				}
			}
		}

		// If English markers are excessive (>= target markers) OR source markers
		// strictly exceed target markers, flag as untranslated.
		if englishHits >= targetFound || sourceHits > targetFound {
			return false
		}
		return true
	}

	// Tier 3: Low confidence (<2 markers) — probably untranslated
	return false
}
