package scriptgeneration

import (
	"errors"
	"fmt"
	"strings"
)

// ErrTranslationIncomplete is the typed failure for a translation phase that
// completed without producing every requested target. It is deliberately a
// postcondition, not a provider error: R108 reached SUCCEEDED with zero calls
// and source text tagged as the target language.
var ErrTranslationIncomplete = errors.New("scriptgeneration: translation coverage incomplete")

// validateTranslationCoverage proves the translation phase delivered what the
// request asked for. A target language is covered only when every translatable
// scene carries a non-empty target text that differs from the source text.
// Fixed-display scenes are included because their display text is translated
// for subtitle/caption burn.
func validateTranslationCoverage(result *GenerateResult, req GenerateRequest) error {
	if result == nil || len(req.Languages) == 0 {
		return nil
	}
	var gaps []string
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		fixedDisplay := scene.ExecutionMode.IsFixedMedia()
		if !scene.ExecutionMode.AllowsTranslation() && !fixedDisplay {
			continue
		}
		if fixedDisplay && !scene.ExecutionMode.AllowsDisplayTextTranslation() {
			continue
		}
		sourceText := strings.TrimSpace(scene.Text[req.SourceLanguage])
		if sourceText == "" {
			// A scene without source text never entered translation work.
			continue
		}
		for _, lang := range req.Languages {
			if lang == req.SourceLanguage {
				continue
			}
			translated := strings.TrimSpace(scene.Text[lang])
			switch {
			case translated == "":
				gaps = append(gaps, fmt.Sprintf("scene %s: missing %s translation", scene.ID, lang))
			case len([]rune(sourceText)) >= 20 && normalizeTranslationText(translated) == normalizeTranslationText(sourceText):
				gaps = append(gaps, fmt.Sprintf("scene %s: %s translation is identical to the %s source", scene.ID, lang, req.SourceLanguage))
			}
		}
	}
	if len(gaps) == 0 {
		return nil
	}
	const maxReported = 8
	reported := gaps
	if len(reported) > maxReported {
		reported = reported[:maxReported]
	}
	suffix := ""
	if len(gaps) > len(reported) {
		suffix = fmt.Sprintf("; ... and %d more", len(gaps)-len(reported))
	}
	return fmt.Errorf("%w: %s%s", ErrTranslationIncomplete, strings.Join(reported, "; "), suffix)
}

// normalizeTranslationText compares translations structurally: case and
// repeated whitespace are not a translation.
func normalizeTranslationText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}
