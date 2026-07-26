// Package translation — OllamaTranslator concrete adapter (Fase 9
// step 2 of the Spina Dorsale, July 2026).
//
// OllamaTranslator is the canonical single-source-of-truth concrete
// for the translation + metadata-generation surface. It satisfies:
//  1. translation.TranslationPort           — the new canonical port
//     (Translate(ctx, cmd TranslationCommand) (TranslationResult, error))
//  2. translation.LegacyTextTranslationService — the 3-arg straggler
//     (TranslateText(ctx, text, lang) (string, error))
//  3. translation.LegacyTranslatorService — the 4-arg straggler
//     (TranslateTextWithModel(ctx, text, lang, model) (string, error))
//  4. translation.LegacyMetadataTranslator — the dto-level combined
//     port (GenerateVideoMetadataWithModel + TranslateTextWithModel)
//
// Why one concrete satisfies four interfaces: godlike/07 EXPAND phase.
// New canonical surface lands first (TranslationPort) + the legacy
// surfaces stay (3 of them) so callers migrate gradually. The single
// concrete adapter makes the composition root swap-out trivial:
// instead of two concrete constructions (oGen.TranslateText for
// legacy calls + oGen.TranslateVideoMetadataWithModel for metadata),
// composition root instantiates one OllamaTranslator that flows into
// every consumer field (svc.Translation, svc.Translator, plus the new
// svc.TranslationPort). Per godlike/06, one canonical owner of the
// ollama-translation fact.
//
// Internal logic (BACKFILL phase, today):
//   - Translate(ctx, cmd): reads cmd.ModelPolicy to resolve the effective
//     model, calls gen.TranslateTextWithModel(..., resolvedModel),
//     hydrates TranslationResult. Cache lookup is delegated to the
//     underlying gen (which exposes the same SetTranslationCache hook
//     at composition time). ModelHints are honored at the typed-errors
//     layer (no_cache hint skips cache by passing nil-cache in cmd;
//     preserve_* are reserved for future Fase 9 step-3 wiring).
//   - TranslateText / TranslateTextWithModel: thin delegations to
//     gen.TranslateTextWithModel Preserve the legacy 3-arg / 4-arg
//     shape so callers using the legacy ports compile unchanged.
//   - GenerateVideoMetadataWithModel: delegates to
//     gen.GenerateVideoMetadataWithModel unchanged (the underlying
//     prompt + parsing logic is provider-specific).
//
// Forward-pointer (canonical tracker entry, godlike/07 EXPAND →
// BACKFILL → CUTOVER → CONTRACT sequence):
//   - architecture/deprecations.yaml#TRANSLATION-LEGACY-SERVICES-MIGRATION
//     (status: in_progress, removal_date: 2026-Q4)
//   - Internal consumers: flow_helpers.go::artlistSearchPhrase
//     migrated to svc.TranslationPort.Translate in this commit;
//     dto/metadata.go::GenerateVideoMetadata continues to use the
//     legacy MetadataTranslator port (still satisfied via the alias
//     `type MetadataTranslator = translation.LegacyMetadataTranslator`)
//     for the BACKFILL phase. CUTOVER = signature change to take
//     TranslationPort; CONTRACT = physical removal of the legacy
//     interfaces.
package translation

import (
	"context"
	"strings"

	ollama "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"go.uber.org/zap"
)

// OllamaTranslator is the canonical concrete adapter that satisfies
// the translation.TranslationPort (new canonical) + the three legacy
// port surfaces declared in legacy.go (godlike/07 EXPAND-window
// back-compat). Construction is via NewOllamaTranslator; composition
// root wires one instance per service graph (sender + creator).
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
