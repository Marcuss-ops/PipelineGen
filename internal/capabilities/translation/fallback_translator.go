// Package translation — fallback_translator.go: a TranslationPort that
// fans a request through a primary provider and, on failure, falls back to
// a secondary provider.
//
// Canonical use (PR-ARGOS-TRANSLATION, Aug 2026): Argos Translate is the
// deterministic, CPU-only primary; Ollama is the quality fallback. The
// chain is fail-soft and never fakes a success — godlike/07.
//
// Quality gate (Sept 2026): "the primary answered with a non-empty string" is
// NOT the same as "the primary translated correctly". Argos can drop a clause,
// loop, copy the source, or answer in the wrong language — all with a
// non-empty answer that the old chain accepted and certified. Every answer is
// now assessed by AssessTranslation (quality.go) before it is accepted:
//
//   - a degenerate PRIMARY answer is not returned; the request continues to the
//     fallback provider (the LLM), which is the whole point of having one;
//   - a degenerate FALLBACK answer is a loud typed error
//     (*ErrDegenerateTranslation) — never a wrong subtitle persisted under a
//     real translation key;
//   - when no fallback is wired, a degenerate primary answer is returned with
//     the typed error so the caller can fail the language instead of burning it.
package translation

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// FallbackTranslator routes each Translate call through `primary` and, when
// the primary errors, returns empty output, or produces a degenerate answer,
// retries through `fallback`. The winning provider's provenance (UsedProvider
// / UsedModel) is preserved on the returned result so the materializer
// persists the honest source.
type FallbackTranslator struct {
	primary  TranslationPort
	fallback TranslationPort
	log      *zap.Logger
}

// NewFallbackTranslator constructs the chain. Either provider may be nil
// (a nil provider is skipped); at least one must be non-nil or Translate
// returns a typed error.
func NewFallbackTranslator(primary, fallback TranslationPort, log *zap.Logger) *FallbackTranslator {
	return &FallbackTranslator{primary: primary, fallback: fallback, log: log}
}

// Translate implements TranslationPort.
func (f *FallbackTranslator) Translate(ctx context.Context, cmd TranslationCommand) (TranslationResult, error) {
	if f == nil {
		return TranslationResult{}, ErrUnimplemented
	}

	if f.primary != nil {
		res, err := f.primary.Translate(ctx, cmd)
		if err == nil && res.TranslatedText != "" {
			if assess := AssessTranslation(cmd.Text, res.TranslatedText, cmd.SourceLang, cmd.TargetLang); assess.Acceptable() {
				return res, nil
			} else {
				// Keep the primary answer so a no-fallback chain can still
				// surface it alongside the typed error.
				f.logDegenerate("primary", cmd, res, assess)
				if f.fallback == nil {
					return res, &ErrDegenerateTranslation{
						SourceLang: cmd.SourceLang,
						TargetLang: cmd.TargetLang,
						Provider:   res.UsedProvider,
						Issues:     assess.Issues,
						Reason:     assess.Reason,
					}
				}
			}
		} else {
			if f.log != nil {
				reason := "primary provider returned empty"
				if err != nil {
					reason = err.Error()
				}
				f.log.Warn("translation: primary provider failed, falling back",
					zap.String("source", cmd.SourceLang),
					zap.String("target", cmd.TargetLang),
					zap.String("reason", reason),
				)
			}
		}
	}

	if f.fallback != nil {
		res, err := f.fallback.Translate(ctx, cmd)
		if err != nil {
			return res, err
		}
		if res.TranslatedText == "" {
			return res, fmt.Errorf("translation: fallback provider returned empty output")
		}
		if assess := AssessTranslation(cmd.Text, res.TranslatedText, cmd.SourceLang, cmd.TargetLang); !assess.Acceptable() {
			f.logDegenerate("fallback", cmd, res, assess)
			return res, &ErrDegenerateTranslation{
				SourceLang: cmd.SourceLang,
				TargetLang: cmd.TargetLang,
				Provider:   res.UsedProvider,
				Issues:     assess.Issues,
				Reason:     assess.Reason,
			}
		}
		return res, nil
	}

	return TranslationResult{}, fmt.Errorf("translation: no provider available for %s->%s", cmd.SourceLang, cmd.TargetLang)
}

// logDegenerate records why an answer was rejected. Observability only: the
// verdict itself is deterministic and does not depend on the logger.
func (f *FallbackTranslator) logDegenerate(role string, cmd TranslationCommand, res TranslationResult, assess Assessment) {
	if f.log == nil {
		return
	}
	issues := make([]string, 0, len(assess.Issues))
	for _, issue := range assess.Issues {
		issues = append(issues, string(issue))
	}
	f.log.Warn("translation: degenerate answer rejected",
		zap.String("role", role),
		zap.String("provider", res.UsedProvider),
		zap.String("model", res.UsedModel),
		zap.String("source", cmd.SourceLang),
		zap.String("target", cmd.TargetLang),
		zap.Strings("issues", issues),
		zap.String("reason", assess.Reason),
		zap.Float64("length_ratio", assess.LengthRatio),
	)
}

// Compile-time assertion: *FallbackTranslator satisfies TranslationPort.
var _ TranslationPort = (*FallbackTranslator)(nil)
