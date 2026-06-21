package script

import (
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// language_markers.go — language detection for translated scripts.
//
// The marker data and detection logic have been extracted to pkg/textutil/
// (textutil.EnMarkers, textutil.LanguageMarkers, textutil.LooksTranslated)
// so both this file and batch_translate.go read from the same source.

// looksTranslated delegates directly to the canonical implementation in pkg/textutil.
func looksTranslated(text, targetLang, sourceLang string) bool {
	return textutil.LooksTranslated(text, targetLang, sourceLang)
}
