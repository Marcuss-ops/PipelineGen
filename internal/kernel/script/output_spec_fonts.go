// Package script — output_spec_fonts.go: the subtitle font/script policy of
// the output spec. Split out of output_spec.go on 2026-10-26 to satisfy the
// archcheck-strict 600-line cap (architecture/policy.yaml#max_lines_per_file_strict);
// same package, no behaviour change.
package script

import (
	"fmt"
	"strings"
)

// ── the preset font must render the language it burns ────────────────
//
// The shipped subtitle fonts do not all cover the same writing systems.
// `Poppins-Bold.ttf` (471 glyphs) carries NO Cyrillic code point at all, while
// `Montserrat-Bold.ttf` (1312 glyphs) carries 222 of them. A preset that asks
// for Poppins therefore produces no glyphs for a Russian subtitle — and with
// the GPU-native text policy (a render plan whose output requires GPU-native
// work, e.g. require_gpu=true) the renderer treats the resulting empty glyph
// run as an UnsupportedCapability error instead of falling back to its software
// text path. That is a whole-run failure, and it landed mid-render (observed at
// frame 283 of a 25 s clip), long after every plan, track and folder had been
// resolved correctly.
//
// The languages of a fan-out are known BEFORE the render, so the font is chosen
// per language where the plan is built, and the swap is explicit: the plan
// carries the font it actually burns, and SubtitleStyleHash (which the ASS
// compiler resolves the preset font from) is derived from that same effective
// style, so the ASS style id and the materialized font cannot disagree.
//
// The declared coverage below is verified against the real font files by
// subtitle_font_coverage_test.go, which parses the shipped cmap tables: a font
// swap in assets/fonts cannot leave this table lying.

// FontScript names a writing system subtitles are rendered in. Only the scripts
// the shipped fonts distinguish are modelled: every declared font covers Latin,
// and Cyrillic is the one script where the presets genuinely differ.
type FontScript string

const (
	ScriptLatin    FontScript = "latin"
	ScriptCyrillic FontScript = "cyrillic"
)

// LocalizationSubtitleStyleHash is the canonical ASS style + generator hash
// prefix. The full style id is this prefix plus the effective font slug
// ("vidrush-default-montserrat"), which is what
// assets/texttracks/ass_materializer.go::ResolveFontPreset resolves the burnt
// font from.
const LocalizationSubtitleStyleHash = "vidrush-default"

// cyrillicLanguages is the BCP-47 base tag of every language the supported set
// writes in Cyrillic. Anything absent is treated as Latin: Latin is the baseline
// the declared fonts all cover, so an unknown language keeps the caller's font
// instead of being swapped on a guess.
var cyrillicLanguages = map[string]bool{
	"ru": true, "uk": true, "be": true, "bg": true, "sr": true, "mk": true,
	"mn": true, "kk": true, "ky": true, "tg": true, "tt": true, "ba": true,
	"cv": true, "ce": true, "os": true, "ab": true, "av": true, "sah": true,
	"udm": true, "kv": true, "kbd": true, "lez": true, "mhr": true, "myv": true,
	"cu": true, "hy": true,
}

// SubtitleFontAsset identifies a font FILE the clip render can burn. Coverage
// belongs to the file, not to the family label the ASS header carries: the
// renderer rasterizes the asset, so a declaration keyed by family name could
// certify a font whose file is never used.
type SubtitleFontAsset string

const (
	// SubtitleFontAssetMontserrat mirrors renderinggen.FontMontserratBold.
	SubtitleFontAssetMontserrat SubtitleFontAsset = "font-montserrat-bold"
	// SubtitleFontAssetPoppins mirrors renderinggen.FontPoppinsBold.
	SubtitleFontAssetPoppins SubtitleFontAsset = "font-poppins-bold"
)

// SubtitleFontAssetForStyle projects a subtitle style onto the font asset the
// clip-render plan will burn for it. It mirrors the projection owned by
// internal/platform/renderinggen (clip_plan_mapper.go::fontAssetID): a style
// whose font names Poppins burns Poppins-Bold.ttf, EVERY other font burns
// Montserrat-Bold.ttf. The mirror is deliberate — the coverage decision must be
// about the file that is rasterized — and the two sides are pinned together by
// renderinggen's font-asset test, so neither rule can drift alone.
func SubtitleFontAssetForStyle(style *VideoVisualStyleSpec) SubtitleFontAsset {
	if style != nil && strings.Contains(strings.ToLower(strings.TrimSpace(style.Font)), "poppins") {
		return SubtitleFontAssetPoppins
	}
	return SubtitleFontAssetMontserrat
}

// subtitleFontScriptCoverage declares, per burnable font ASSET, the scripts its
// glyphs cover. Because the projection above is TOTAL (every font name resolves
// to one of these two shipped files), every style has a declared answer: there
// is no "unknown font" left for this guard to guess about. The previous shape
// keyed coverage by family name and treated anything absent as covered, which
// silently certified Impact/Anton/Bebas/Roboto styles for Cyrillic on the
// strength of a label no renderer burns — and would have kept them certified if
// one of those families ever became the effective font.
//
// The declaration is verified against the real .ttf cmap tables by
// subtitle_font_coverage_test.go, which parses the shipped assets: swapping a
// font file cannot leave this table (and the renderer) disagreeing.
var subtitleFontScriptCoverage = map[SubtitleFontAsset]map[FontScript]bool{
	SubtitleFontAssetMontserrat: {ScriptLatin: true, ScriptCyrillic: true},
	SubtitleFontAssetPoppins:    {ScriptLatin: true},
}

// subtitleFontsByScript is the preference order used to replace a font that
// cannot render the target script. The canonical clip font (Montserrat) leads
// because it is the deployment default and the one shipped asset guaranteed to
// carry Cyrillic glyphs.
var subtitleFontsByScript = map[FontScript][]string{
	ScriptCyrillic: {"Montserrat", "Poppins"},
	ScriptLatin:    {"Montserrat", "Poppins"},
}

// LanguageFontScript returns the writing system a language's subtitles are
// rendered in. Region and case variants are folded, so "ru-RU" and "RU" are the
// same script as "ru".
func LanguageFontScript(language string) FontScript {
	base := strings.ToLower(strings.TrimSpace(language))
	if i := strings.IndexAny(base, "-_"); i > 0 {
		base = base[:i]
	}
	if cyrillicLanguages[base] {
		return ScriptCyrillic
	}
	return ScriptLatin
}

// FontCoversScript reports whether the file a font name projects to can render
// a script. The second result stays in the signature because a coverage gap
// must still fail closed rather than invent coverage; with the total projection
// in place it is true for every input the render path can produce, and the
// false branch is reached only if a style ever projects outside the shipped
// assets (the test below pins that unreachable-by-construction property).
func FontCoversScript(font string, script FontScript) (covers bool, known bool) {
	scripts, ok := subtitleFontScriptCoverage[SubtitleFontAssetForStyle(&VideoVisualStyleSpec{Font: font})]
	if !ok {
		return false, false
	}
	return scripts[script], true
}

// FontCoversLanguage reports whether a font renders the script of a language.
// The question is answered through the projected asset, so the guard swaps
// exactly the fonts proven unable to burn the language, and never swaps one on
// a guess about a family label.

// SubtitleStyleHash is the canonical ASS style id for a subtitle style: the
// base hash plus the effective font slug. A style with no font keeps the bare
// base hash. This is the only owner of that rule; the localization wiring and
// the per-language plan builder both derive the id through it.
func SubtitleStyleHash(style *VideoVisualStyleSpec) string {
	if style == nil || strings.TrimSpace(style.Font) == "" {
		return LocalizationSubtitleStyleHash
	}
	return fmt.Sprintf("%s-%s", LocalizationSubtitleStyleHash, strings.ToLower(strings.TrimSpace(style.Font)))
}

// EnsureSubtitleFontForLanguage returns the subtitle style to burn for a
// language: the caller's style when its font can render the language, else a
// copy of it whose font is the first declared font that covers the script. The
// second result reports whether a swap happened, so the caller can record the
// substitution. A nil/absent font is never swapped (the preset default fills it
// in downstream), and a language whose script no shipped font covers fails
// closed with a typed error instead of reaching the renderer with no glyphs.
func EnsureSubtitleFontForLanguage(style *VideoVisualStyleSpec, language string) (*VideoVisualStyleSpec, bool, error) {
	if style == nil || strings.TrimSpace(style.Font) == "" {
		return style, false, nil
	}
	script := LanguageFontScript(language)
	covers, known := FontCoversScript(style.Font, script)
	if !known || covers {
		return style, false, nil
	}
	for _, font := range subtitleFontsByScript[script] {
		if covers, known := FontCoversScript(font, script); known && covers {
			swapped := *style
			swapped.Font = font
			return &swapped, true, nil
		}
	}
	return nil, false, fmt.Errorf(
		"subtitle font %q cannot render the %s script required by language %q and no shipped font covers it",
		style.Font, script, language)
}
