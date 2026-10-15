package overlays

import "strings"

// documentaryPhraseMotionAliases preserves each showcase identity all the way
// into RenderingGen, where the phrase-relative runtime compiler applies its
// own target spans and sweep geometry to the supplied long copy.
var documentaryPhraseMotionAliases = map[string]string{
	"doc_01_yellow_marker_sweep":         "doc_01_yellow_marker_sweep",
	"doc_02_multiline_stagger_highlight": "doc_02_multiline_stagger_highlight",
	"doc_03_documentary_quote_marker":    "doc_03_documentary_quote_marker",
	"doc_04_archival_source_highlight":   "doc_04_archival_source_highlight",
	"doc_05_selective_phrase_tracking":   "doc_05_selective_phrase_tracking",
	"doc_06_red_keyword_punch":           "doc_06_red_keyword_punch",
	"doc_07_word_impact_reveal":          "doc_07_word_impact_reveal",
	"doc_08_mixed_font_emphasis":         "doc_08_mixed_font_emphasis",
	"doc_09_cinematic_underline_draw":    "doc_09_cinematic_underline_draw",
	"doc_10_selective_contrast_focus":    "doc_10_selective_contrast_focus",
	"doc_11_headline_cinematic_zoom":     "doc_11_headline_cinematic_zoom",
	"doc_12_quote_line_by_line_reveal":   "doc_12_quote_line_by_line_reveal",
	"doc_13_analog_scanner_highlight":    "doc_13_analog_scanner_highlight",
	"doc_14_dual_highlight_sequence":     "doc_14_dual_highlight_sequence",
	"doc_15_documentary_source_focus":    "doc_15_documentary_source_focus",
}

// ResolveDocumentaryPhraseMotion maps a showcase preset ID to its adaptive
// runtime motion. Unknown IDs pass through so the normal certified-catalog
// validation remains the single authority for all other motion IDs.
func ResolveDocumentaryPhraseMotion(id string) string {
	id = strings.TrimSpace(id)
	if motion, ok := documentaryPhraseMotionAliases[id]; ok {
		return motion
	}
	return id
}

func resolveDocumentaryPhraseMotions(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	resolved := make([]string, len(ids))
	for i, id := range ids {
		resolved[i] = ResolveDocumentaryPhraseMotion(id)
	}
	return resolved
}
