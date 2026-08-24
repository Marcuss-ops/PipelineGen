// Package translation contains the Ollama-backed TranslationPort adapter.
package translation

import (
	"context"
	"strings"

	ollama "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	"go.uber.org/zap"
)

// OllamaTranslator is the concrete adapter for TranslationPort.
//
// Field contract:
//   - gen: the canonical ollama chat client wrapper. *NOT* nil in
//     production; nil-guard at translation-time per the legacy
//     pattern (returns "ollama client not initialized" rather than
//     panicking).
//   - log: optional zap logger; nil-tolerant (translations proceed
//     silently on nil logger).
type OllamaTranslator struct {
	gen *ollama.Generator
	log *zap.Logger
}

// NewOllamaTranslator constructs the canonical concrete. The gen
// parameter is the production ollama wrapper (carrying the
// translation cache wired at composition time via SetTranslationCache).
// log is optional; nil is silently tolerated.
//
// Construction never fails — the adapter is fully constructed at
// build time and lazily validates the ollama client at translation
// time (mirrors the legacy *ollama.Generator.TranslateTextWithModel
// nil-client guard semantics).
func NewOllamaTranslator(gen *ollama.Generator, log *zap.Logger) *OllamaTranslator {
	return &OllamaTranslator{
		gen: gen,
		log: log,
	}
}

// ── TranslationPort impl ─────────────────────────────────────────────────

// Translate implements translation.TranslationPort. Reads cmd.ModelPolicy
// to resolve the effective model (cmd.ModelHints honoring is a
// Fase 9 step-3 follow-up; today hints are accepted but ignored so
// FUTURE adding of honour is the BACKFILL-extension and not a
// signature-breaking surface change. Calls gen.TranslateTextWithModel
// internally. Hydrates the TranslationResult envelope with the
// translated text + echoed InputFields + CacheStatus placeholder
// (cache audit is delegated to the underlying gen today; a future
// step inlines the cache audit into the result envelope).
func (o *OllamaTranslator) Translate(ctx context.Context, cmd TranslationCommand) (TranslationResult, error) {
	// Resolve the model: cmd.ModelPolicy.Model > "" (server picks).
	resolvedModel := ""
	if cmd.ModelPolicy != nil && cmd.ModelPolicy.Model != "" {
		resolvedModel = cmd.ModelPolicy.Model
	}

	translated, err := o.gen.TranslateTextWithModel(ctx, cmd.Text, cmd.TargetLang, resolvedModel)
	if err != nil {
		// Wrap the error so the typed-error path is preserved at the
		// application-layer boundary. Callers recover the inner error
		// via errors.Is/As against ollama.Transient markers
		// (canonical taxonomy in pkg/retry).
		return TranslationResult{
			SourceLang:   cmd.SourceLang,
			TargetLang:   cmd.TargetLang,
			UsedModel:    resolvedModel,
			UsedProvider: "ollama",
			// CacheStatus left empty: cache hit/miss is logged at the
			// gen layer today (see gen.TranslateTextWithModel
			// "translation cache HIT" log line). Future step
			// inlines the cache-status into the result envelope.
		}, err
	}

	// Empty translated output is a godlike/07 silent-fake-success
	// anti-pattern: surface an empty Text result so the caller can
	// detect the no-output case explicitly. Returning "" + nil
	// would mask the failure.
	return TranslationResult{
		TranslatedText: strings.TrimSpace(translated),
		Confidence:     0, // unknown — provider doesn't return one for ollama
		UsedModel:      resolvedModel,
		UsedProvider:   "ollama",
		SourceLang:     cmd.SourceLang,
		TargetLang:     cmd.TargetLang,
		// CacheStatus left empty as above (deferred to Fase 9 step-3).
	}, nil
}

var _ TranslationPort = (*OllamaTranslator)(nil)
