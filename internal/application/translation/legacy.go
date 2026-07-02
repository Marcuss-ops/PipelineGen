// Package translation — Fase 9 step 2 (Spina Dorsale, July 2026):
// canonical home for the three legacy translation interfaces relocated
// from internal/application/scripts/usecase/services.go (the two
// straggler ports) and internal/application/scripts/dto/metadata.go
// (the dto-level combined port). Each declaration below is the
// godlike/06 SSOT home for the legacy interface; the old-package
// sites now expose Go type-alias bindings (`type X = translation.LegacyX`)
// so existing references continue to compile unchanged during the
// godlike/07 EXPAND window.
//
// ── EXPAND window (godlike/07 §Migration sequence) ────────────────
//
// This file is the first half of the EXPAND-phase surface. The
// companion file ollama_translator.go in this same directory:
//
//   * declares the canonical concrete that implements ALL FOUR
//     interfaces (TranslationPort + the 3 legacy), so the
//     composition root can swap a single OllamaTranslator in for
//     the legacy two-concrete shape.
//
// Production callers (internal/application/scripts/usecase/flow_helpers.go
// ::artlistSearchPhrase) continue to use the same svc.Translator field
// (now type-aliased to translation.LegacyTranslatorService) AND a
// new svc.TranslationPort field (translation.TranslationPort) is
// added to ClipServices for forward-compat pathway migration.
//
// The deprecation tracking record lives at
// architecture/deprecations.yaml#TRANSLATION-LEGACY-SERVICES-MIGRATION
// with migration_phase: EXPAND, status: in_progress. Removal
// deadline 2026-Q4 per the canonical tracker.
package translation

import "context"

// ── LegacyTextTranslationService ───────────────────────────────────────────

// LegacyTextTranslationService is the historical 3-arg straggler
// from internal/application/scripts/usecase/services.go
// (pre-Fase 9), renamed method from the original Translate() to
// TranslateText() to avoid name-collision with TranslationPort.Translate
// AND to match the canonical *ollama.Generator.TranslateText method
// shape so the concrete ollama.Generator satisfies this port
// structurally. The original Translate() method name would have
// collided with TranslationPort.Translate at the OllamaTranslator
// concrete definition site.
//
// Byte-stable signature; references resolve via
// `type TextTranslationService = translation.LegacyTextTranslationService`
// at internal/application/scripts/usecase/services.go (the old home).
type LegacyTextTranslationService interface {
	TranslateText(ctx context.Context, text, targetLang string) (string, error)
}

// ── LegacyTranslatorService ───────────────────────────────────────────────

// LegacyTranslatorService is the historical 4-arg straggler from
// scripts/usecase/services.go (pre-Fase 9). Byte-stable signature.
// Method name TranslateTextWithModel matches the canonical
// *ollama.Generator.TranslateTextWithModel concrete so the ollama
// generator satisfies this port structurally without an adapter.
//
// References resolve via
// `type TranslatorService = translation.LegacyTranslatorService`
// at internal/application/scripts/usecase/services.go (the old home).
type LegacyTranslatorService interface {
	TranslateTextWithModel(ctx context.Context, text, lang, model string) (string, error)
}

// ── LegacyMetadataTranslator ──────────────────────────────────────────────

// LegacyMetadataTranslator is the historical 2-method combined port
// from internal/application/scripts/dto/metadata.go (pre-Fase 9),
// consumed by dto.GenerateVideoMetadata. The two methods reflect
// the pre-Fase-9 surface: GenerateVideoMetadataWithModel (English
// metadata generation, single LLM call shared across all languages)
// + TranslateTextWithModel (per-language metadata translation via the
// same ollama chat path used by TranslateTextWithModel).
//
// Byte-stable signature; references resolve via
// `type MetadataTranslator = translation.LegacyMetadataTranslator`
// at internal/application/scripts/dto/metadata.go (the old home).
// Test mocks at internal/application/scripts/dto/metadata_test.go
// continue to satisfy the renamed surface via the type alias.
type LegacyMetadataTranslator interface {
	GenerateVideoMetadataWithModel(ctx context.Context, title, model string) (string, []string, error)
	TranslateTextWithModel(ctx context.Context, text, lang, model string) (string, error)
}
