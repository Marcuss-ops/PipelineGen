package script

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// ── the coverage table must match the shipped fonts ──────────────
//
// These tests read the REAL font files: the declared coverage in
// output_spec.go (subtitleFontScriptCoverage, keyed by the font asset a style
// projects to) is what the runtime trusts to decide whether a preset font can
// burn a language, so a font swap in assets/fonts must break the declaration
// here rather than at frame 283 of a live render.

func fontAssetPath(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "assets", "fonts", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("font asset %s: %v", name, err)
	}
	return path
}

func TestSubtitleFontCoverageMatchesShippedFontFiles(t *testing.T) {
	t.Parallel()
	probes := map[FontScript][]rune{
		ScriptLatin:    {'A', 'z', '0'},
		ScriptCyrillic: {'А', 'я', 'Ы', 'ё'}, // U+0410 А, U+044F я, U+042B Ы, U+0451 ё
	}
	// Keyed by the ASSET (the file the render plan burns), not by a family
	// label: the declared coverage must describe the glyphs that are actually
	// rasterized.
	assets := map[SubtitleFontAsset]string{
		SubtitleFontAssetMontserrat: "Montserrat-Bold.ttf",
		SubtitleFontAssetPoppins:    "Poppins-Bold.ttf",
	}
	for declared, file := range assets {
		data, err := os.ReadFile(fontAssetPath(t, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		table, ok := subtitleFontScriptCoverage[declared]
		if !ok {
			t.Fatalf("%s: no declared coverage", declared)
		}
		for script, runes := range probes {
			missing := 0
			for _, r := range runes {
				if !cmapHasRune(data, r) {
					missing++
				}
			}
			actual := missing == 0
			if table[script] != actual {
				t.Errorf("%s (%s): declared %s coverage = %v, but the font file has %d/%d probe runes",
					declared, file, script, table[script], len(runes)-missing, len(runes))
			}
		}
	}
}

// TestSubtitleFontProjectionIsTotalAndDeclared pins the property that CLOSED
// the "unknown font" hole. Coverage used to be keyed by family name and any
// unlisted family was reported as covered — a guess that certified Impact,
// Anton, Bebas Neue and Roboto styles for Cyrillic on the strength of a label
// the renderer never burns. Now every font name projects onto one of the two
// shipped assets (mirroring renderinggen's clip-plan mapper), and that asset's
// coverage is declared, so there is no unknown case left to guess about.
func TestSubtitleFontProjectionIsTotalAndDeclared(t *testing.T) {
	t.Parallel()

	// The complete font vocabulary the style presets can produce (see
	// scriptStylePresets) plus the empty/default case.
	for _, font := range []string{"", "Montserrat", "montserrat_bold", "Poppins", "Impact", "Anton", "Bebas Neue", "Roboto", "Inter"} {
		style := &VideoVisualStyleSpec{Font: font}
		asset := SubtitleFontAssetForStyle(style)
		table, declared := subtitleFontScriptCoverage[asset]
		if !declared {
			t.Fatalf("font %q projects to asset %q with no declared coverage: a style could reach the renderer uncertified", font, asset)
		}
		if !table[ScriptLatin] {
			t.Fatalf("font %q projects to asset %q without Latin coverage: every shipped subtitle font covers Latin", font, asset)
		}
	}

	// The two projections that decide the live defect, spelled out: Poppins is
	// the only name that burns the Poppins file (no Cyrillic), and every other
	// name — including the families whose .ttf is not shipped — burns
	// Montserrat (Cyrillic covered).
	tests := []struct {
		font      string
		asset     SubtitleFontAsset
		cyrillic  bool
		alsoLatin bool
	}{
		{font: "Poppins", asset: SubtitleFontAssetPoppins, cyrillic: false, alsoLatin: true},
		{font: "poppins_extra", asset: SubtitleFontAssetPoppins, cyrillic: false, alsoLatin: true},
		{font: "Montserrat", asset: SubtitleFontAssetMontserrat, cyrillic: true, alsoLatin: true},
		{font: "Impact", asset: SubtitleFontAssetMontserrat, cyrillic: true, alsoLatin: true},
		{font: "Anton", asset: SubtitleFontAssetMontserrat, cyrillic: true, alsoLatin: true},
		{font: "Bebas Neue", asset: SubtitleFontAssetMontserrat, cyrillic: true, alsoLatin: true},
		{font: "Roboto", asset: SubtitleFontAssetMontserrat, cyrillic: true, alsoLatin: true},
	}
	for _, tc := range tests {
		style := &VideoVisualStyleSpec{Font: tc.font}
		if got := SubtitleFontAssetForStyle(style); got != tc.asset {
			t.Errorf("font %q projects to %q, want %q", tc.font, got, tc.asset)
		}
		covers, known := FontCoversScript(tc.font, ScriptCyrillic)
		if !known {
			t.Errorf("font %q: coverage must be KNOWN for every projected font; got unknown", tc.font)
		}
		if covers != tc.cyrillic {
			t.Errorf("font %q covers Cyrillic = %v, want %v", tc.font, covers, tc.cyrillic)
		}
		if latin, known := FontCoversScript(tc.font, ScriptLatin); !known || latin != tc.alsoLatin {
			t.Errorf("font %q covers Latin = %v (known=%v), want %v", tc.font, latin, known, tc.alsoLatin)
		}
	}
}

// TestEnsureSubtitleFontForLanguage pins the live defect: a Cyrillic target must
// never keep a font that has no Cyrillic glyphs, because the GPU-native render
// path treats the resulting empty glyph run as fatal and takes the whole run
// down. The Latin languages must keep the caller's typography untouched.
func TestEnsureSubtitleFontForLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		font     string
		language string
		wantFont string
		wantSwap bool
	}{
		{"cyrillic target gets a covering font", "Poppins", "ru", "Montserrat", true},
		{"cyrillic region variant", "Poppins", "ru-RU", "Montserrat", true},
		{"cyrillic case variant", "Poppins", "RU", "Montserrat", true},
		{"cyrillic target already covered", "Montserrat", "ru", "Montserrat", false},
		{"latin target keeps poppins", "Poppins", "it", "Poppins", false},
		{"latin-region target keeps poppins", "Poppins", "pt-BR", "Poppins", false},
		{"latin target keeps montserrat", "Montserrat", "de", "Montserrat", false},
		// These families name no shipped .ttf, but they project onto the shipped
		// Montserrat asset (the render plan's rule), so the Cyrillic run is
		// certified by the file that actually burns — the font field is kept as
		// the caller asked, the glyphs come from Montserrat.
		{"unshipped family projected to montserrat is left alone", "Impact", "ru", "Impact", false},
		{"unshipped family projected to montserrat is left alone (roboto)", "Roboto", "ru", "Roboto", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			style := &VideoVisualStyleSpec{Font: tc.font, FontSizePX: 60, Position: "bottom_center"}
			got, swapped, err := EnsureSubtitleFontForLanguage(style, tc.language)
			if err != nil {
				t.Fatalf("EnsureSubtitleFontForLanguage(%s, %s): %v", tc.font, tc.language, err)
			}
			if swapped != tc.wantSwap {
				t.Fatalf("swapped = %v, want %v", swapped, tc.wantSwap)
			}
			if got.Font != tc.wantFont {
				t.Fatalf("font = %q, want %q", got.Font, tc.wantFont)
			}
			// The swap must not mutate the caller's style: the same style is
			// shared by every language of a fan-out.
			if style.Font != tc.font {
				t.Fatalf("caller style mutated: %q -> %q", tc.font, style.Font)
			}
			if got.FontSizePX != style.FontSizePX || got.Position != style.Position {
				t.Fatalf("swap dropped the rest of the typography: %+v", *got)
			}
		})
	}
}

func TestEnsureSubtitleFontForLanguageLeavesAbsentFontToThePreset(t *testing.T) {
	t.Parallel()
	if got, swapped, err := EnsureSubtitleFontForLanguage(&VideoVisualStyleSpec{FontSizePX: 60}, "ru"); err != nil || swapped || got == nil {
		t.Fatalf("absent font: got=%v swapped=%v err=%v; want the style untouched", got, swapped, err)
	}
	if got, swapped, err := EnsureSubtitleFontForLanguage(nil, "ru"); err != nil || swapped || got != nil {
		t.Fatalf("nil style: got=%v swapped=%v err=%v; want nil,false,nil", got, swapped, err)
	}
}

func TestSubtitleStyleHashFollowsTheEffectiveFont(t *testing.T) {
	t.Parallel()
	if got := SubtitleStyleHash(&VideoVisualStyleSpec{Font: "Poppins"}); got != "vidrush-default-poppins" {
		t.Fatalf("poppins hash = %q", got)
	}
	if got := SubtitleStyleHash(&VideoVisualStyleSpec{Font: " Montserrat "}); got != "vidrush-default-montserrat" {
		t.Fatalf("montserrat hash = %q", got)
	}
	if got := SubtitleStyleHash(nil); got != LocalizationSubtitleStyleHash {
		t.Fatalf("nil style hash = %q", got)
	}
}

// ── minimal cmap reader (test-only) ──────────────────────────────

// cmapHasRune reports whether the font's cmap maps a code point to a glyph.
// Only the two subtable formats the shipped fonts use are handled: format 4
// (BMP segment mapping) and format 12 (sequential groups).
func cmapHasRune(font []byte, r rune) bool {
	cmap, ok := fontTable(font, "cmap")
	if !ok || len(cmap) < 4 {
		return false
	}
	subtable := selectCmapSubtable(cmap, r)
	if subtable == nil {
		return false
	}
	switch binary.BigEndian.Uint16(subtable[0:2]) {
	case 4:
		return cmap4HasRune(subtable, uint16(r))
	case 12:
		return cmap12HasRune(subtable, uint32(r))
	default:
		return false
	}
}

func fontTable(font []byte, tag string) ([]byte, bool) {
	if len(font) < 12 {
		return nil, false
	}
	numTables := int(binary.BigEndian.Uint16(font[4:6]))
	for i := 0; i < numTables; i++ {
		rec := 12 + i*16
		if rec+16 > len(font) {
			return nil, false
		}
		if string(font[rec:rec+4]) != tag {
			continue
		}
		offset := int(binary.BigEndian.Uint32(font[rec+8 : rec+12]))
		length := int(binary.BigEndian.Uint32(font[rec+12 : rec+16]))
		if offset < 0 || offset+length > len(font) {
			return nil, false
		}
		return font[offset : offset+length], true
	}
	return nil, false
}

func selectCmapSubtable(cmap []byte, r rune) []byte {
	numSubtables := int(binary.BigEndian.Uint16(cmap[2:4]))
	var fallback []byte
	for i := 0; i < numSubtables; i++ {
		rec := 4 + i*8
		if rec+8 > len(cmap) {
			return fallback
		}
		platform := binary.BigEndian.Uint16(cmap[rec : rec+2])
		encoding := binary.BigEndian.Uint16(cmap[rec+2 : rec+4])
		offset := int(binary.BigEndian.Uint32(cmap[rec+4 : rec+8]))
		if offset < 0 || offset+2 > len(cmap) {
			continue
		}
		sub := cmap[offset:]
		format := binary.BigEndian.Uint16(sub[0:2])
		// Prefer the full Unicode range for a supplementary code point and the
		// BMP/Unicode subtables otherwise.
		if platform == 3 && encoding == 10 && format == 12 {
			return sub
		}
		if r > 0xFFFF && format == 12 && (platform == 0 || platform == 3) {
			return sub
		}
		if fallback == nil && (platform == 3 && encoding == 1 || platform == 0) {
			fallback = sub
		}
	}
	return fallback
}

func cmap4HasRune(sub []byte, r uint16) bool {
	if len(sub) < 14 {
		return false
	}
	segCount := int(binary.BigEndian.Uint16(sub[6:8]) / 2)
	endBase := 14
	startBase := endBase + segCount*2 + 2
	deltaBase := startBase + segCount*2
	rangeBase := deltaBase + segCount*2
	if rangeBase+segCount*2 > len(sub) {
		return false
	}
	for i := 0; i < segCount; i++ {
		end := binary.BigEndian.Uint16(sub[endBase+i*2 : endBase+i*2+2])
		if r > end {
			continue
		}
		start := binary.BigEndian.Uint16(sub[startBase+i*2 : startBase+i*2+2])
		if r < start {
			return false
		}
		delta := binary.BigEndian.Uint16(sub[deltaBase+i*2 : deltaBase+i*2+2])
		rangeOffset := binary.BigEndian.Uint16(sub[rangeBase+i*2 : rangeBase+i*2+2])
		if rangeOffset == 0 {
			return (r+delta)&0xFFFF != 0
		}
		glyphAt := rangeBase + i*2 + int(rangeOffset) + int(r-start)*2
		if glyphAt+2 > len(sub) {
			return false
		}
		glyph := binary.BigEndian.Uint16(sub[glyphAt : glyphAt+2])
		return glyph != 0 || (r+delta)&0xFFFF != 0
	}
	return false
}

func cmap12HasRune(sub []byte, r uint32) bool {
	if len(sub) < 16 {
		return false
	}
	groups := int(binary.BigEndian.Uint32(sub[12:16]))
	base := 16
	if base+groups*12 > len(sub) {
		return false
	}
	for i := 0; i < groups; i++ {
		off := base + i*12
		start := binary.BigEndian.Uint32(sub[off : off+4])
		end := binary.BigEndian.Uint32(sub[off+4 : off+8])
		if r < start {
			return false
		}
		if r <= end {
			return true
		}
	}
	return false
}
