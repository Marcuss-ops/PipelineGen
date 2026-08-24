// Package translation — fallback_translator.go: a TranslationPort that
// fans a request through a primary provider and, on failure, falls back to
// a secondary provider.
//
// Canonical use (PR-ARGOS-TRANSLATION, Aug 2026): Argos Translate is the
// deterministic, CPU-only primary; Ollama is the quality fallback. The
// chain is fail-soft and never fakes a success — godlike/07.
package translation

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// FallbackTranslator routes each Translate call through `primary` and, when
// the primary errors or returns empty output, retries through `fallback`.
// The winning provider's provenance (UsedProvider / UsedModel) is preserved
// on the returned result so the materializer persists the honest source.
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
			return res, nil
		}
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

	if f.fallback != nil {
		res, err := f.fallback.Translate(ctx, cmd)
		if err != nil {
			return res, err
		}
		if res.TranslatedText == "" {
			return res, fmt.Errorf("translation: fallback provider returned empty output")
		}
		return res, nil
	}

	return TranslationResult{}, fmt.Errorf("translation: no provider available for %s->%s", cmd.SourceLang, cmd.TargetLang)
}

// Compile-time assertion: *FallbackTranslator satisfies TranslationPort.
var _ TranslationPort = (*FallbackTranslator)(nil)
