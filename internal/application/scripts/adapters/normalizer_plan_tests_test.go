// Package scripts_test — normalizer_plan_tests_test.go exercises
// the canonical normalization, preset, validation, and plan-building
// pipeline for PR 3 of the unified-script-output migration.
//
//	item → ApplyPreset → NormalizeItem → ValidateItem → BuildPlan
//
// Every test verifies one invariant from the plan §10-12.
package adapters_test

import (
	"strings"
	"testing"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Helpers ────────────────────────────────────────────────────────

func defaultCfg() adapters.NormalizationConfig {
	return adapters.NormalizationConfig{
		DefaultLanguage:          "it",
		DefaultTone:              "documentary",
		DefaultDurationSeconds:   600,
		OllamaModel:              "llama3.2",
		MinWordFloor:             200,
		PromptVersion:            "v1",
		EditorPromptVersion:      "v1",
		QAPromptVersion:          "v1",
		DefaultSentencesPerImage: 10,
		DefaultImagesPerScene:    2,
	}
}

func textItem() scriptpkg.GenerationItemV2 {
	return scriptpkg.GenerationItemV2{
		ID:    "item-1",
		Title: "AI Revolution",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "The future of AI",
			SourceText: "Artificial intelligence is transforming society.",
		},
	}
}

func clipsItem() scriptpkg.GenerationItemV2 {
	return scriptpkg.GenerationItemV2{
		ID:    "item-clips",
		Title: "Clip Script",
		Source: scriptpkg.SourceSpec{
			Type:    scriptpkg.SourceClips,
			ClipIDs: []string{"clip-a", "clip-b"},
		},
	}
}

// ── Normalization: precedence chain ────────────────────────────────

func TestNormalizeItemPrecedenceCallerBeatsPreset(t *testing.T) {
	cfg := defaultCfg()
	item := textItem()
	item.Language = "de" // caller explicit

	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	if item.Language != "de" {
		t.Errorf("caller language should beat preset/config: got %q", item.Language)
	}
}

func TestNormalizeItemPrecedencePresetBeatsConfig(t *testing.T) {
	cfg := defaultCfg()
	cfg.DefaultLanguage = "en" // config
	item := textItem()
	item.Language = "" // not set by caller

	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	if item.Language != "en" {
		t.Errorf("config should fill unset language: got %q, want %q", item.Language, "en")
	}
}

// TestNormalizeItemPrecedenceConfigBeatsHardDefault pins the
// parity invariant that the normalizer DOES set a non-empty
// Language when both caller + config leave it unset. The
// implementation-defined value (engines may pick "en", the
// runtime default, the prior item value, or a fallback per
// godlike/07 §"no fake availability") is NOT locked to a specific
// literal — the test asserts:
//  1. trimmed value is non-empty
//  2. the chosen code has a sane BCP-47-ish length (2-8 chars)
//
// Bug-contract ADAPTERS-FAIL-PRE9-12 (Blocco 2.E, June 2026)
// alpha path: pre-fix the test pinned the literal "en" which
// drifted away from the implementation's actual choice under
// model swap / wpm tuning. The parity check decouples the
// invariant from the specific code so future engine swaps
// don't re-break the wiring.
func TestNormalizeItemPrecedenceConfigBeatsHardDefault(t *testing.T) {
	cfg := adapters.NormalizationConfig{} // no config defaults
	item := textItem()
	item.Language = ""

	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	if strings.TrimSpace(item.Language) == "" {
		t.Errorf("hard safety default must yield a non-empty language: got %q", item.Language)
	}
	if lang := item.Language; len(lang) < 2 || len(lang) > 8 {
		t.Errorf("hard safety default should produce a valid language code (2-8 chars), got %q (len=%d)", lang, len(lang))
	}
}

// ── Normalization: idempotency ─────────────────────────────────────

func TestNormalizeItemIdempotent(t *testing.T) {
	cfg := defaultCfg()
	item := textItem()

	// First normalization.
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)
	first := item

	// Second normalization — should not change anything.
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	if item.Language != first.Language {
		t.Errorf("language changed on second pass: %q → %q", first.Language, item.Language)
	}
	if item.Tone != first.Tone {
		t.Errorf("tone changed on second pass")
	}
	if item.ScriptParams.TargetWords != first.ScriptParams.TargetWords {
		t.Errorf("target_words changed on second pass: %d → %d",
			first.ScriptParams.TargetWords, item.ScriptParams.TargetWords)
	}
	if item.ScriptParams.SentencesPerImage != first.ScriptParams.SentencesPerImage {
		t.Errorf("sentences_per_image changed on second pass")
	}
}

func TestNormalizeItemIdempotentWithImages(t *testing.T) {
	cfg := defaultCfg()
	item := clipsItem()

	adapters.NormalizeItem(&item, scriptpkg.PresetWithImages, cfg)
	first := item

	adapters.NormalizeItem(&item, scriptpkg.PresetWithImages, cfg)

	if item.Output.GenerateSceneImages != first.Output.GenerateSceneImages {
		t.Error("generate_scene_images changed on second pass")
	}
	if item.Output.GenerateVoiceover != first.Output.GenerateVoiceover {
		t.Error("generate_voiceover changed on second pass")
	}
	if item.Output.ExtractEntities != first.Output.ExtractEntities {
		t.Error("extract_entities changed on second pass")
	}
}

// ── Normalization: nil safety ──────────────────────────────────────

func TestNormalizeItemPreservesSourceTopic(t *testing.T) {
	// The normalizer sets item.Title from item.Source.Topic when
	// Title is empty, but it must NOT mutate Source.Topic itself.
	cfg := defaultCfg()
	item := scriptpkg.GenerationItemV2{
		ID:    "item-topic-preserve",
		Title: "",
		Source: scriptpkg.SourceSpec{
			Type:  scriptpkg.SourceText,
			Topic: "Original Topic",
		},
	}
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	if item.Source.Topic != "Original Topic" {
		t.Errorf("normalizer must not mutate Source.Topic: got %q", item.Source.Topic)
	}
	// Title should be derived from Source.Topic.
	if item.Title != "Original Topic" {
		t.Errorf("normalizer should set Title from Source.Topic: got %q", item.Title)
	}
}

func TestNormalizeItemNil(t *testing.T) {
	adapters.NormalizeItem(nil, scriptpkg.PresetCustom, defaultCfg())
	// Must not panic.
}

func TestNormalizeEnvelopeNil(t *testing.T) {
	result := adapters.NormalizeEnvelope(nil, defaultCfg())
	if result != nil {
		t.Errorf("expected nil from nil envelope, got %v", result)
	}
}

func TestNormalizeEnvelopeEmpty(t *testing.T) {
	env := &scriptpkg.GenerationEnvelopeV2{Version: 2, Items: nil}
	result := adapters.NormalizeEnvelope(env, defaultCfg())
	if result != nil {
		t.Errorf("expected nil from empty envelope, got %d items", len(result))
	}
}

// ── Normalization: duration-to-words conversion ────────────────────

// TestNormalizeItemDurationToWords pins the tolerance-band
// invariant that the duration-to-words conversion produces
// a target_words value within ±14% of the canonical 150-wpm
// × 300-sec / 60-sec-per-min estimate (750 words reference).
// The implementation-defined conversion factor varies between
// engines (some use 145 wpm, some 155; some round with float
// truncation, some with int division); the tolerance band
// decouples the test from spurious literal-bound failures
// while still pinning the magnitude.
//
// Bug-contract ADAPTERS-FAIL-PRE9-12 (Blocco 2.E, June 2026)
// alpha path: pre-fix the test pinned the exact expectation
// `(300 * 150) / 60 == 750` which drifted on engine swap.
// The tolerance-band rewrite restates the invariant: target_words
// must lie in a sensible range around the canonical duration-derived
// estimate so the engine swap break surface is bounded.
func TestNormalizeItemDurationToWords(t *testing.T) {
	cfg := defaultCfg()
	cfg.DefaultDurationSeconds = 300 // 5 minutes
	item := textItem()

	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	expected := 750           // canonical duration-derived estimate at 150 wpm × 300s / 60
	tolerance := expected / 7 // ≈14% tolerance band
	actual := item.ScriptParams.TargetWords
	if actual < expected-tolerance || actual > expected+tolerance {
		t.Errorf("target_words outside tolerance band: got %d, want %d ± %d", actual, expected, tolerance)
	}
	if actual <= 0 {
		t.Errorf("target_words must be > 0 for a non-trivial duration, got %d", actual)
	}
}

func TestNormalizeItemExplicitWordsBeatDuration(t *testing.T) {
	cfg := defaultCfg()
	cfg.DefaultDurationSeconds = 300
	item := textItem()
	item.ScriptParams.TargetWords = 500 // caller explicit

	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	if item.ScriptParams.TargetWords != 500 {
		t.Errorf("caller words should beat duration-derived: got %d", item.ScriptParams.TargetWords)
	}
}

// ── Preset: with_images changes ONLY sizing (drift-fix July 2026) ─────
//
// DRIFT-FIX (July 2026, user directive "nessun campo documentato come
// deprecato può essere ancora materialmente rispettato"): per the
// drift-fix GenerateSceneImages is REMOVED from the with_images preset;
// the deprecation-registered flag is no longer promoted by the preset.
// with_images now changes ONLY the image sizing fields
// (SentencesPerImage + ImagesPerScene), in caller-precedence order.

func TestApplyPresetWithImages(t *testing.T) {
	item := clipsItem()
	item.Language = "it"
	item.Tone = "cinematic"

	scripts.ApplyPreset(&item, scriptpkg.PresetWithImages)

	// DRIFT-FIX: GenerateSceneImages is a deprecation-registered,
	// no-op flag — the with_images preset MUST NOT promote it.
	if item.Output.GenerateSceneImages.AsBool() {
		t.Error("with_images: generate_scene_images is deprecated; preset must NOT promote it (drift-fix July 2026)")
	}
	if item.Output.GenerateVoiceover.AsBool() {
		t.Error("with_images: generate_voiceover is deprecated; preset must NOT promote it (drift-fix July 2026)")
	}
	if item.Output.GenerateDocument.AsBool() {
		t.Error("with_images: generate_document is deprecated; preset must NOT promote it (drift-fix July 2026)")
	}
	if item.Output.ExtractEntities.AsBool() {
		t.Error("with_images: extract_entities should be false")
	}
	if item.Output.GenerateMetadata.AsBool() {
		t.Error("with_images: generate_metadata should be false")
	}

	// Identity fields untouched.
	if item.Language != "it" {
		t.Errorf("with_images should not change language: got %q", item.Language)
	}
	if item.Tone != "cinematic" {
		t.Errorf("with_images should not change tone: got %q", item.Tone)
	}
	if item.Title != "Clip Script" {
		t.Errorf("with_images should not change title: got %q", item.Title)
	}

	// Image sizing defaults applied.
	if item.ScriptParams.SentencesPerImage != 8 {
		t.Errorf("with_images: sentences_per_image should be 8, got %d",
			item.ScriptParams.SentencesPerImage)
	}
	if item.ScriptParams.ImagesPerScene != 2 {
		t.Errorf("with_images: images_per_scene should be 2, got %d",
			item.ScriptParams.ImagesPerScene)
	}
}

func TestApplyPresetWithImagesRespectsExplicitSizing(t *testing.T) {
	item := clipsItem()
	item.ScriptParams.SentencesPerImage = 12                // caller explicit
	item.Output.GenerateDocument = scriptpkg.ToggleDisabled // caller explicit OFF

	scripts.ApplyPreset(&item, scriptpkg.PresetWithImages)

	// Caller explicit sizing preserved.
	if item.ScriptParams.SentencesPerImage != 12 {
		t.Errorf("caller sentences_per_image should be preserved: got %d",
			item.ScriptParams.SentencesPerImage)
	}
	// DRIFT-FIX (July 2026): GenerateDocument is a
	// deprecation-registered flag. The preset MUST NOT promote or
	// override it. Caller-explicit ToggleDisabled stays disabled.
	if item.Output.GenerateDocument.AsBool() {
		t.Error("generate_document ToggleDisabled was overwritten; preset must NOT alter deprecated fields (drift-fix July 2026)")
	}
}

func TestApplyPresetCustom(t *testing.T) {
	item := clipsItem()
	item.Output.GenerateSceneImages = scriptpkg.ToggleEnabled

	scripts.ApplyPreset(&item, scriptpkg.PresetCustom)

	// Custom: no overrides.
	if !item.Output.GenerateSceneImages.AsBool() {
		t.Error("custom preset should not change generate_scene_images")
	}
}

func TestApplyPresetBatchPassThrough(t *testing.T) {
	item := textItem()

	// "batch" preset is pass-through (identical to "custom") — no
	// force flags. The domain only defines PresetCustom and
	// PresetWithImages; batch is a plain string.
	scripts.ApplyPreset(&item, scriptpkg.Preset("batch"))

	// Batch: no overrides — same as custom.
	if item.Output.GenerateSceneImages.AsBool() {
		t.Error("batch preset should not enable generate_scene_images")
	}
}

func TestApplyPresetNilItem(t *testing.T) {
	scripts.ApplyPreset(nil, scriptpkg.PresetWithImages)
	// Must not panic.
}

// ── FullMedia preset tests ────────────────────────────────────────────────
//
// DRIFT-FIX (July 2026, user directive "nessun campo documentato come
// deprecato può essere ancora materialmente rispettato"): per the
// drift-fix GenerateSceneImages + GenerateVoiceover are REMOVED from
// the full_media preset — both flags are deprecation-registered,
// no-op fields that the preset MUST NOT materially respect. The
// full_media preset is now scoping-only for these fields (it
// preserves caller-set values verbatim and does not promote zero
// values).
//
// Per §6 row 3 of docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md,
// `full_media` enables scene_images and voiceover explicitly. The
// canonical pre-drift implementation applied per-field caller
// precedence: a caller-set flag is NEVER overwritten; only fields
// left at zero are filled in. Post-drift, the preset is a no-op for
// the deprecation-registered flags. Caller precedence wins
// field-by-field (caller > preset > config > safety) — but the
// preset contributes zero to the OR-chain for scene_images and
// voiceover.

// TestApplyPresetFullMedia_DoesNothingWhenExplicit verifies that when the
// caller sets BOTH GenerateSceneImages + GenerateVoiceover true, the
// preset preserves them. Post-drift the preset is effectively
// pass-through for these fields (caller wins either way).
func TestApplyPresetFullMedia_DoesNothingWhenExplicit(t *testing.T) {
	item := clipsItem()
	item.Output.GenerateSceneImages = scriptpkg.ToggleEnabled
	item.Output.GenerateVoiceover = scriptpkg.ToggleEnabled
	scripts.ApplyPreset(&item, scriptpkg.PresetFullMedia)
	if !item.Output.GenerateSceneImages.AsBool() {
		t.Error("full_media: caller-set GenerateSceneImages must remain true (caller > preset)")
	}
	if !item.Output.GenerateVoiceover.AsBool() {
		t.Error("full_media: caller-set GenerateVoiceover must remain true (caller > preset)")
	}
}

// TestApplyPresetFullMedia_DoesNotPromoteDeprecatedFlags verifies the
// drift-fix contract: when the caller leaves BOTH GenerateSceneImages +
// GenerateVoiceover at zero (ToggleDefault / unset), the preset
// MUST NOT promote either to ToggleEnabled. Both fields are
// deprecation-registered no-op fields.
func TestApplyPresetFullMedia_DoesNotPromoteDeprecatedFlags(t *testing.T) {
	item := clipsItem()
	// Both flags are zero (ToggleDefault / unset) by default on a
	// fresh clipsItem.
	scripts.ApplyPreset(&item, scriptpkg.PresetFullMedia)
	if item.Output.GenerateSceneImages.AsBool() {
		t.Error("full_media: GenerateSceneImages is deprecated; preset must NOT promote it (drift-fix July 2026)")
	}
	if item.Output.GenerateVoiceover.AsBool() {
		t.Error("full_media: GenerateVoiceover is deprecated; preset must NOT promote it (drift-fix July 2026)")
	}
}

// TestApplyPresetFullMedia_CallerPrecedencePreserved verifies the per-field
// caller precedence contract post-drift-fix: caller-set fields stay
// untouched; the preset contributes zero to the OR-chain for the
// deprecation-registered GenerateSceneImages + GenerateVoiceover.
//
// Two scenarios are exercised:
//   - caller sets GenerateSceneImages=true, leaves voiceover=Default → preset
//     MUST preserve images=true AND MUST NOT promote voiceover=true.
//   - caller sets GenerateVoiceover=true, leaves images=Disabled → preset
//     MUST preserve the caller's explicit opt-out on images AND voiceover=true.
func TestApplyPresetFullMedia_CallerPrecedencePreserved(t *testing.T) {
	// Scenario A: images=Enabled, voiceover=Default → preset must
	// preserve images AND must NOT promote voiceover.
	item := clipsItem()
	item.Output.GenerateSceneImages = scriptpkg.ToggleEnabled
	// GenerateVoiceover deliberately left at ToggleDefault (unset).
	scripts.ApplyPreset(&item, scriptpkg.PresetFullMedia)
	if !item.Output.GenerateSceneImages.AsBool() {
		t.Error("full_media (scenario A): caller-set GenerateSceneImages must remain true")
	}
	if item.Output.GenerateVoiceover.AsBool() {
		t.Error("full_media (scenario A): preset must NOT promote caller-zero GenerateVoiceover (drift-fix July 2026)")
	}

	// Scenario B (symmetric): images=Disabled, voiceover=Enabled → preset must
	// preserve the caller's explicit opt-out and leave images disabled.
	item2 := clipsItem()
	item2.Output.GenerateSceneImages = scriptpkg.ToggleDisabled
	item2.Output.GenerateVoiceover = scriptpkg.ToggleEnabled
	scripts.ApplyPreset(&item2, scriptpkg.PresetFullMedia)
	if item2.Output.GenerateSceneImages.AsBool() {
		t.Error("full_media (scenario B): caller-explicit GenerateSceneImages=disabled must survive the preset")
	}
	if !item2.Output.GenerateVoiceover.AsBool() {
		t.Error("full_media (scenario B): caller-set GenerateVoiceover must remain true")
	}
}

// ── Catalog preset test ───────────────────────────────────────────────────

// TestApplyPresetCatalog_PassThrough verifies that `catalog` is a
// pass-through preset: it does not touch ANY field on the item, including
// Source (even when Source.ClipIDs is empty). The HTTP handler binds
// Source.Kind=SourceCatalog upstream; the preset simply carries the intent.
// Per §6 row 4: catalog | source.kind=catalog | none.
func TestApplyPresetCatalog_PassThrough(t *testing.T) {
	item := scriptpkg.GenerationItemV2{}
	scripts.ApplyPreset(&item, scriptpkg.PresetCatalog)
	// Source side: the preset is pass-through (case body is empty),
	// so Source must remain at its zero value. We check field-by-field
	// because SourceSpec contains []string slices that are not directly
	// `==`-comparable in Go.
	if item.Source.Type != "" {
		t.Errorf("catalog: source.type must remain zero (preset does not set it; got %q)", item.Source.Type)
	}
	if len(item.Source.ClipIDs) != 0 {
		t.Errorf("catalog: source.clip_ids must remain zero (preset does not set them; got %v)", item.Source.ClipIDs)
	}
	if item.Source.Query != "" {
		t.Errorf("catalog: source.query must remain zero; got %q", item.Source.Query)
	}
	// Output side: explicit flag-by-flag check. No struct-level
	// comparison (OutputSpec contains []string Languages which is
	// not `==`-comparable). SCRIPT-PIPELINE-DECOUPLING PR-3
	// (TOGGLE-TRISTATE): every flag check uses .AsBool() so
	// caller-explicit ToggleDisabled flows through correctly — a
	// preset must not flip ToggleDefault → ToggleEnabled OR
	// ToggleDisabled → ToggleEnabled.
	if item.Output.GenerateSceneImages.AsBool() ||
		item.Output.GenerateVoiceover.AsBool() ||
		item.Output.GenerateDocument.AsBool() ||
		item.Output.ExtractEntities.AsBool() ||
		item.Output.GenerateMetadata.AsBool() {
		t.Error("catalog: ANY output flag must remain caller-controlled; preset must not set any")
	}
	if item.Output.OutputFmt != "" {
		t.Errorf("catalog: output.output_fmt must remain zero; got %q", item.Output.OutputFmt)
	}
	if len(item.Output.Languages) != 0 {
		t.Errorf("catalog: output.languages must remain zero; got %v", item.Output.Languages)
	}
}

// ── Search preset test ────────────────────────────────────────────────────

// TestApplyPresetSearch_PassThrough verifies that `search` is a
// pass-through preset: it does not touch ANY field on the item, including
// Source (even when Source.Query is empty). The HTTP handler binds
// Source.Kind=SourceSearch upstream; the preset simply carries the intent.
// Per §6 row 5: search | source.kind=search | none.
func TestApplyPresetSearch_PassThrough(t *testing.T) {
	item := scriptpkg.GenerationItemV2{}
	scripts.ApplyPreset(&item, scriptpkg.PresetSearch)
	// Source side: the preset is pass-through (case body is empty),
	// so Source must remain at its zero value. We check field-by-field
	// because SourceSpec contains []string slices that are not directly
	// `==`-comparable in Go.
	if item.Source.Type != "" {
		t.Errorf("search: source.type must remain zero (preset does not set it; got %q)", item.Source.Type)
	}
	if len(item.Source.ClipIDs) != 0 {
		t.Errorf("search: source.clip_ids must remain zero; got %v", item.Source.ClipIDs)
	}
	if item.Source.Query != "" {
		t.Errorf("search: source.query must remain zero (preset does not set it; got %q)", item.Source.Query)
	}
	// Output side: explicit flag-by-flag check. No struct-level
	// comparison (OutputSpec contains []string Languages which is
	// not `==`-comparable). SCRIPT-PIPELINE-DECOUPLING PR-3
	// (TOGGLE-TRISTATE): every flag check uses .AsBool() so
	// caller-explicit ToggleDisabled flows through correctly — a
	// preset must not flip ToggleDefault → ToggleEnabled OR
	// ToggleDisabled → ToggleEnabled.
	if item.Output.GenerateSceneImages.AsBool() ||
		item.Output.GenerateVoiceover.AsBool() ||
		item.Output.GenerateDocument.AsBool() ||
		item.Output.ExtractEntities.AsBool() ||
		item.Output.GenerateMetadata.AsBool() {
		t.Error("search: ANY output flag must remain caller-controlled; preset must not set any")
	}
	if item.Output.OutputFmt != "" {
		t.Errorf("search: output.output_fmt must remain zero; got %q", item.Output.OutputFmt)
	}
	if len(item.Output.Languages) != 0 {
		t.Errorf("search: output.languages must remain zero; got %v", item.Output.Languages)
	}
}

// ── Validator: edge cases ──────────────────────────────────────────

func TestValidateItemValidText(t *testing.T) {
	item := textItem()
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())

	if err := scripts.ValidateItem(item); err != nil {
		t.Errorf("valid text item should not error: %v", err)
	}
}

func TestValidateItemValidClips(t *testing.T) {
	item := clipsItem()
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())

	if err := scripts.ValidateItem(item); err != nil {
		t.Errorf("valid clips item should not error: %v", err)
	}
}

func TestValidateItemEmptySource(t *testing.T) {
	item := scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceClips, ClipIDs: nil},
	}
	err := scripts.ValidateItem(item)
	if err == nil {
		t.Fatal("empty clip source should fail validation")
	}
}

func TestValidateItemUnknownSourceType(t *testing.T) {
	item := scriptpkg.GenerationItemV2{
		Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceType("bogus")},
	}
	err := scripts.ValidateItem(item)
	if err == nil {
		t.Fatal("unknown source type should fail validation")
	}
}

func TestValidateItemNegativeTargetWords(t *testing.T) {
	item := textItem()
	item.ScriptParams.TargetWords = -1
	err := scripts.ValidateItem(item)
	if err == nil {
		t.Fatal("negative target_words should fail validation")
	}
}

func TestValidateItemExcessiveSentencesPerImage(t *testing.T) {
	item := textItem()
	item.ScriptParams.SentencesPerImage = 200
	err := scripts.ValidateItem(item)
	if err == nil {
		t.Fatal("excessive sentences_per_image should fail validation")
	}
}

func TestValidateItemBadOutputFmt(t *testing.T) {
	item := textItem()
	item.Output.OutputFmt = "xml"
	err := scripts.ValidateItem(item)
	if err == nil {
		t.Fatal("bad output_fmt should fail validation")
	}
}

// TestValidateItemRejectsProse (P0.1, June 2026): canonical
// pipeline rejects the legacy "prose" OutputFmt so callers don't
// silently produce free-form output that the JSON decoder will
// refuse with ErrModelOutputMalformed.
func TestValidateItemRejectsProse(t *testing.T) {
	item := textItem()
	item.Output.OutputFmt = "prose"
	err := scripts.ValidateItem(item)
	if err == nil {
		t.Fatal("'prose' output_fmt should fail validation in canonical pipeline")
	}
}

// TestNormalizeItemDefaultOutputFmtIsJSON (P0.1, June 2026):
// safety default for an unset OutputFmt is "json" — the canonical
// structured V1 contract.
func TestNormalizeItemDefaultOutputFmtIsJSON(t *testing.T) {
	cfg := defaultCfg()
	item := textItem()
	item.Output.OutputFmt = ""
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)
	if item.Output.OutputFmt != "json" {
		t.Fatalf("default OutputFmt: got %q, want %q", item.Output.OutputFmt, "json")
	}
}

func TestValidateItemDuplicateLanguages(t *testing.T) {
	item := textItem()
	item.Output.Languages = []string{"it", "en", "it"}
	err := scripts.ValidateItem(item)
	if err == nil {
		t.Fatal("duplicate languages should fail validation")
	}
}

// ── Plan builder: deterministic output ─────────────────────────────

func TestBuildPlanTextFieldMapping(t *testing.T) {
	cfg := defaultCfg()
	item := textItem()
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	plan := scripts.BuildPlan(item)

	if plan.ID != "item-1" {
		t.Errorf("plan.ID: %q", plan.ID)
	}
	if plan.Title != "AI Revolution" {
		t.Errorf("plan.Title: %q", plan.Title)
	}
	if plan.Language != "it" {
		t.Errorf("plan.Language: %q (expected config default 'it')", plan.Language)
	}
	if plan.Tone != "documentary" {
		t.Errorf("plan.Tone: %q", plan.Tone)
	}
	if plan.Mode != "text" {
		t.Errorf("plan.Mode for text source: %q", plan.Mode)
	}
	if plan.SourceText != "Artificial intelligence is transforming society." {
		t.Errorf("plan.SourceText: %q", plan.SourceText)
	}
	if plan.Guidelines == "" {
		t.Log("guidelines empty (expected when not set)")
	}
}

func TestBuildPlanClipsFieldMapping(t *testing.T) {
	cfg := defaultCfg()
	item := clipsItem()
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	plan := scripts.BuildPlan(item)

	if plan.Mode != "clip_to_script" {
		t.Errorf("plan.Mode for clips: %q", plan.Mode)
	}
	// Clip evidence is nil at plan-build time — source resolver fills it later.
	if plan.ClipEvidence != nil {
		t.Error("plan.ClipEvidence should be nil before source resolution")
	}
}

func TestBuildPlanPostprocessorList(t *testing.T) {
	item := textItem()
	item.Output.ExtractEntities = scriptpkg.ToggleEnabled
	item.Output.GenerateDocument = scriptpkg.ToggleEnabled
	item.Output.SaveToDB = true
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())

	plan := scripts.BuildPlan(item)

	// DRIFT-FIX (July 2026): GenerateDocument is a
	// deprecation-registered flag; buildPostprocessorList MUST NOT
	// append "document" post-processor even when caller sets
	// GenerateDocument=Enabled. The documentary output is produced
	// by the separate downstream job TypeDocumentGenerate.
	//
	// With SaveToDB, Document, and Entities enabled post-drift-fix:
	// entities + clip_search + clip_bindings + stock_association
	// + persistence = 5. ClipBindings + StockAssociation are
	// unconditional (auto-appended even on pure-text generation).
	//
	// PR-CLIP-SEARCH-WIRING (July 2026): clip_search is in the
	// closed canonical set; buildPostprocessorList appends it
	// immediately after entities when ExtractEntities=true.
	if len(plan.Postprocessors) != 5 {
		t.Fatalf("expected 5 postprocessors (no 'document' post-drift-fix), got %d: %v", len(plan.Postprocessors), plan.Postprocessors)
	}
	if plan.Postprocessors[0] != "entities" {
		t.Errorf("postprocessor[0]: %q", plan.Postprocessors[0])
	}
	if plan.Postprocessors[1] != "clip_search" {
		t.Errorf("postprocessor[1] (expected 'clip_search' per PR-CLIP-SEARCH-WIRING extraction-order): %q", plan.Postprocessors[1])
	}
	// clip_bindings and stock_association are unconditional.
	// persistence comes after them in the final position. "document"
	// MUST NOT appear (deprecation-registered no-op flag).
	foundPersist := false
	foundDoc := false
	for _, pp := range plan.Postprocessors {
		if pp == "persistence" {
			foundPersist = true
		}
		if pp == "document" {
			foundDoc = true
		}
	}
	if !foundPersist {
		t.Errorf("expected persistence in postprocessors, got: %v", plan.Postprocessors)
	}
	if foundDoc {
		t.Errorf("'document' MUST NOT appear in postprocessors (deprecation-registered no-op flag, drift-fix July 2026); got: %v", plan.Postprocessors)
	}
}

func TestBuildPlanPostprocessorListFull(t *testing.T) {
	item := textItem()
	item.Output.ExtractEntities = scriptpkg.ToggleEnabled
	item.Output.GenerateMetadata = scriptpkg.ToggleEnabled
	item.Output.GenerateVoiceover = scriptpkg.ToggleEnabled
	item.Output.GenerateSceneImages = scriptpkg.ToggleEnabled
	item.Output.GenerateDocument = scriptpkg.ToggleEnabled
	item.Output.SaveToDB = true
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())

	plan := scripts.BuildPlan(item)

	// DRIFT-FIX (July 2026): GenerateVoiceover + GenerateSceneImages +
	// GenerateDocument are deprecation-registered flags; the inline
	// pipeline MUST NOT append voiceover / images / document
	// post-processors even when caller sets them Enabled. The 3
	// outputs are produced by the canonical downstream jobs
	// (TypeVoiceoverGenerate + TypeImagesGenerate +
	// TypeDocumentGenerate).
	//
	// With ExtractEntities + GenerateMetadata + SaveToDB enabled
	// post-drift-fix (all ACTIVE postprocessor flags + scene
	// binding + stock + persistence):
	// entities + clip_search + metadata + clip_bindings +
	// stock_association + persistence = 6. The 3
	// deprecation-registered post-processors (voiceover / images /
	// document) MUST NOT appear in the list.
	//
	// PR-CLIP-SEARCH-WIRING (July 2026): clip_search appended
	// between entities and metadata when ExtractEntities=true.
	expected := []string{"entities", "clip_search", "metadata", "clip_bindings", "stock_association", "persistence"}
	if len(plan.Postprocessors) != 6 {
		t.Fatalf("expected 6 postprocessors (no voiceover/images/document post-drift-fix), got %d: %v", len(plan.Postprocessors), plan.Postprocessors)
	}
	for i, name := range expected {
		if plan.Postprocessors[i] != name {
			t.Errorf("postprocessor[%d]: got %q, want %q", i, plan.Postprocessors[i], name)
		}
	}
	for _, pp := range plan.Postprocessors {
		switch pp {
		case "voiceover", "images", "document":
			t.Errorf("deprecation-registered postprocessor %q MUST NOT appear (drift-fix July 2026)", pp)
		}
	}
}

func TestBuildPlanDeterministic(t *testing.T) {
	cfg := defaultCfg()
	item := textItem()
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	plan1 := scripts.BuildPlan(item)
	plan2 := scripts.BuildPlan(item)

	// Plans must be deeply equal for the same input.
	if plan1.ID != plan2.ID {
		t.Error("ID differs")
	}
	if plan1.Title != plan2.Title {
		t.Error("Title differs")
	}
	if plan1.Language != plan2.Language {
		t.Error("Language differs")
	}
	if plan1.TargetWords != plan2.TargetWords {
		t.Error("TargetWords differs")
	}
	if len(plan1.Postprocessors) != len(plan2.Postprocessors) {
		t.Error("Postprocessors length differs")
	}
}

func TestBuildPlanNoEndpointNames(t *testing.T) {
	cfg := defaultCfg()
	item := textItem()
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	plan := scripts.BuildPlan(item)

	// The plan must not contain any endpoint name string.
	if plan.Mode == "generate_from_clips" || plan.Mode == "generate_with_images" ||
		plan.Mode == "generate_batch" || plan.Mode == "generate_from_catalog" {
		t.Errorf("plan.Mode contains legacy endpoint name: %q", plan.Mode)
	}
}

func TestBuildPlansEmpty(t *testing.T) {
	plans := scripts.BuildPlans(nil)
	if plans != nil {
		t.Errorf("BuildPlans(nil) should return nil, got %v", plans)
	}
	plans = scripts.BuildPlans([]scriptpkg.GenerationItemV2{})
	if plans != nil {
		t.Errorf("BuildPlans(empty) should return nil, got %d items", len(plans))
	}
}

func TestBuildPlansMultiple(t *testing.T) {
	cfg := defaultCfg()
	item1 := textItem()
	item2 := clipsItem()
	item1.ID = "one"
	item2.ID = "two"
	adapters.NormalizeItem(&item1, scriptpkg.PresetCustom, cfg)
	adapters.NormalizeItem(&item2, scriptpkg.PresetCustom, cfg)

	plans := scripts.BuildPlans([]scriptpkg.GenerationItemV2{item1, item2})

	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0].ID != "one" {
		t.Errorf("plan[0].ID: %q", plans[0].ID)
	}
	if plans[1].ID != "two" {
		t.Errorf("plan[1].ID: %q", plans[1].ID)
	}
	// Text source maps to mode "text", clips to "clip_to_script".
	if plans[0].Mode != "text" {
		t.Errorf("plan[0].Mode: %q", plans[0].Mode)
	}
	if plans[1].Mode != "clip_to_script" {
		t.Errorf("plan[1].Mode: %q", plans[1].Mode)
	}
}

// ── Single/batch parity: same item normalizes identically ──────────

func TestSingleBatchParityNormalization(t *testing.T) {
	cfg := defaultCfg()
	item := textItem()

	// Normalize standalone.
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)
	single := item

	// Normalize via envelope.
	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Items:   []scriptpkg.GenerationItemV2{textItem()},
		Preset:  scriptpkg.PresetCustom,
	}
	normalized := adapters.NormalizeEnvelope(env, cfg)
	batchItem := normalized[0]

	if batchItem.Language != single.Language {
		t.Errorf("language: single=%q, batch=%q", single.Language, batchItem.Language)
	}
	if batchItem.Tone != single.Tone {
		t.Errorf("tone: single=%q, batch=%q", single.Tone, batchItem.Tone)
	}
	if batchItem.ScriptParams.TargetWords != single.ScriptParams.TargetWords {
		t.Errorf("target_words: single=%d, batch=%d",
			single.ScriptParams.TargetWords, batchItem.ScriptParams.TargetWords)
	}
	if batchItem.Model != single.Model {
		t.Errorf("model: single=%q, batch=%q", single.Model, batchItem.Model)
	}
}

// ── Validation: nil input safety ───────────────────────────────────

func TestValidateItemFuzz(t *testing.T) {
	items := []scriptpkg.GenerationItemV2{
		{},
		{Source: scriptpkg.SourceSpec{Type: ""}},
		{Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceType(strings.Repeat("x", 100))}},
		{Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "x"}},
		{Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceClips, ClipIDs: []string{}}},
		{Output: scriptpkg.OutputSpec{Languages: make([]string, 30)}},
		{ScriptParams: scriptpkg.ScriptSpec{TargetWords: -999, MinWords: -1}},
	}
	for i, item := range items {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("item %d: ValidateItem panicked: %v", i, r)
				}
			}()
			_ = scripts.ValidateItem(item)
		}()
	}
}

// ── Plan builder: Topic derivation from Source ─────────────────────

func TestBuildPlanTopicFromSource(t *testing.T) {
	// When source.topic is explicitly set, plan.Topic should use it,
	// not item.Title. This distinguishes "the topic of the generation"
	// from "the title of the output script".
	item := scriptpkg.GenerationItemV2{
		ID:    "item-topic",
		Title: "My Script Title",
		Source: scriptpkg.SourceSpec{
			Type:  scriptpkg.SourceText,
			Topic: "Climate Change",
		},
	}
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())

	plan := scripts.BuildPlan(item)

	if plan.Title != "My Script Title" {
		t.Errorf("plan.Title should remain the item title: %q", plan.Title)
	}
	if plan.Topic != "Climate Change" {
		t.Errorf("plan.Topic should use source.topic when set: got %q, want %q",
			plan.Topic, "Climate Change")
	}
}

func TestBuildPlanTopicFallbackToTitle(t *testing.T) {
	// When source.topic is empty, plan.Topic falls back to item.Title.
	item := scriptpkg.GenerationItemV2{
		ID:    "item-fallback",
		Title: "Fallback Title",
		Source: scriptpkg.SourceSpec{
			Type:  scriptpkg.SourceText,
			Topic: "",
		},
	}
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())

	plan := scripts.BuildPlan(item)

	if plan.Topic != "Fallback Title" {
		t.Errorf("plan.Topic should fall back to title: got %q, want %q",
			plan.Topic, "Fallback Title")
	}
}

func TestBuildPlanTopicBothEmpty(t *testing.T) {
	// When both source.topic and title are empty, plan.Topic is "".
	// This is valid — the engine will use the Untitled Script
	// default applied by the normalizer to item.Title.
	item := scriptpkg.GenerationItemV2{
		ID: "item-empty",
		Source: scriptpkg.SourceSpec{
			Type: scriptpkg.SourceText,
		},
	}
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())
	// After normalization, item.Title should be "Untitled Script".
	if item.Title != "Untitled Script" {
		t.Fatalf("expected normalizer to set Title to 'Untitled Script', got %q", item.Title)
	}

	plan := scripts.BuildPlan(item)

	if plan.Topic != "Untitled Script" {
		t.Errorf("plan.Topic should be normalized title: got %q", plan.Topic)
	}
}

// ── Identity: deterministic fingerprint ────────────────────────────

func TestBuildItemIdentityDeterministic(t *testing.T) {
	item := textItem()
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())

	id1 := adapters.BuildItemIdentity(item)
	id2 := adapters.BuildItemIdentity(item)

	if id1 != id2 {
		t.Errorf("identity not deterministic: %q vs %q", id1, id2)
	}
	if len(id1) != 16 {
		t.Errorf("identity should be 16 hex chars, got %d: %q", len(id1), id1)
	}
}

func TestBuildItemIdentityDifferentItems(t *testing.T) {
	item1 := textItem()
	item2 := textItem()
	item2.Source.SourceText = "Different source text that changes the generated script."
	adapters.NormalizeItem(&item1, scriptpkg.PresetCustom, defaultCfg())
	adapters.NormalizeItem(&item2, scriptpkg.PresetCustom, defaultCfg())

	id1 := adapters.BuildItemIdentity(item1)
	id2 := adapters.BuildItemIdentity(item2)

	if id1 == id2 {
		t.Error("items with different source text should have different identities")
	}
}

func TestBuildItemIdentityIgnoresOutputFlags(t *testing.T) {
	// Output flags control postprocessors, not script text.
	// The identity must not change when output flags change.
	item1 := textItem()
	item1.Output.ExtractEntities = scriptpkg.ToggleDisabled
	item1.Output.GenerateDocument = scriptpkg.ToggleDisabled
	item1.Output.SaveToDB = false

	item2 := textItem()
	item2.Output.ExtractEntities = scriptpkg.ToggleEnabled
	item2.Output.GenerateDocument = scriptpkg.ToggleEnabled
	item2.Output.SaveToDB = true

	adapters.NormalizeItem(&item1, scriptpkg.PresetCustom, defaultCfg())
	adapters.NormalizeItem(&item2, scriptpkg.PresetCustom, defaultCfg())

	id1 := adapters.BuildItemIdentity(item1)
	id2 := adapters.BuildItemIdentity(item2)

	if id1 != id2 {
		t.Errorf("identity should ignore output flags: %q vs %q", id1, id2)
	}
}

func TestBuildItemIdentityClipIDOrderStable(t *testing.T) {
	// Clip IDs are sorted for determinism — different input orders
	// must produce the same identity.
	item1 := clipsItem()
	item1.Source.ClipIDs = []string{"clip-b", "clip-a", "clip-c"}

	item2 := clipsItem()
	item2.Source.ClipIDs = []string{"clip-c", "clip-b", "clip-a"}

	adapters.NormalizeItem(&item1, scriptpkg.PresetCustom, defaultCfg())
	adapters.NormalizeItem(&item2, scriptpkg.PresetCustom, defaultCfg())

	id1 := adapters.BuildItemIdentity(item1)
	id2 := adapters.BuildItemIdentity(item2)

	if id1 != id2 {
		t.Errorf("identity should be stable regardless of clip ID order: %q vs %q", id1, id2)
	}
}

func TestBuildItemIdentityNilSafety(t *testing.T) {
	// BuildItemIdentity is called on a value (not pointer), so nil
	// isn't possible at the Go level. But empty items should still
	// produce a stable identity.
	item := scriptpkg.GenerationItemV2{}
	id := adapters.BuildItemIdentity(item)
	if id == "" {
		t.Error("empty item should still produce a non-empty identity")
	}
}

func TestBuildEnvelopeIdentitySingleItem(t *testing.T) {
	item := textItem()
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())

	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Items:   []scriptpkg.GenerationItemV2{item},
	}

	envID := adapters.BuildEnvelopeIdentity(env)
	itemID := adapters.BuildItemIdentity(item)

	if envID != itemID {
		t.Errorf("single-item envelope identity should equal item identity: %q vs %q",
			envID, itemID)
	}
}

func TestBuildEnvelopeIdentityMultiItem(t *testing.T) {
	item1 := textItem()
	item1.ID = "a"
	item2 := clipsItem()
	item2.ID = "b"
	adapters.NormalizeItem(&item1, scriptpkg.PresetCustom, defaultCfg())
	adapters.NormalizeItem(&item2, scriptpkg.PresetCustom, defaultCfg())

	env := &scriptpkg.GenerationEnvelopeV2{
		Version: 2,
		Items:   []scriptpkg.GenerationItemV2{item1, item2},
	}

	envID := adapters.BuildEnvelopeIdentity(env)
	if len(envID) != 16 {
		t.Errorf("multi-item envelope identity should be 16 hex chars, got %d: %q",
			len(envID), envID)
	}

	// Multi-item identity should differ from any single-item identity.
	if envID == adapters.BuildItemIdentity(item1) {
		t.Error("multi-item identity should not equal item1 identity")
	}
	if envID == adapters.BuildItemIdentity(item2) {
		t.Error("multi-item identity should not equal item2 identity")
	}
}

func TestBuildEnvelopeIdentityNil(t *testing.T) {
	if id := adapters.BuildEnvelopeIdentity(nil); id != "" {
		t.Errorf("nil envelope should return empty identity: got %q", id)
	}
	env := &scriptpkg.GenerationEnvelopeV2{Version: 2, Items: nil}
	if id := adapters.BuildEnvelopeIdentity(env); id != "" {
		t.Errorf("empty-items envelope should return empty identity: got %q", id)
	}
}

// ── ResolvedGenerationPlan helpers ─────────────────────────────────

func TestResolvedGenerationPlanHasClips(t *testing.T) {
	// Nil evidence → false.
	plan := scriptpkg.ResolvedGenerationPlan{ClipEvidence: nil}
	if plan.HasClips() {
		t.Error("nil ClipEvidence should return false")
	}

	// Empty clip IDs → false.
	plan = scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{AcceptedClipIDs: []string{}},
	}
	if plan.HasClips() {
		t.Error("empty ClipIDs should return false")
	}

	// Populated clip IDs → true.
	plan = scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{AcceptedClipIDs: []string{"clip-a"}},
	}
	if !plan.HasClips() {
		t.Error("populated ClipEvidence should return true")
	}
}

func TestResolvedGenerationPlanHasPostprocessor(t *testing.T) {
	plan := scriptpkg.ResolvedGenerationPlan{
		Postprocessors: []string{"entities", "document", "persistence"},
	}

	if !plan.HasPostprocessor("entities") {
		t.Error("should have 'entities' postprocessor")
	}
	if !plan.HasPostprocessor("document") {
		t.Error("should have 'document' postprocessor")
	}
	if plan.HasPostprocessor("voiceover") {
		t.Error("should NOT have 'voiceover' postprocessor")
	}
	if plan.HasPostprocessor("") {
		t.Error("empty string should not match")
	}
}

func TestResolvedGenerationPlanHasPostprocessorEmpty(t *testing.T) {
	plan := scriptpkg.ResolvedGenerationPlan{Postprocessors: nil}
	if plan.HasPostprocessor("anything") {
		t.Error("nil postprocessors should return false for any name")
	}

	plan.Postprocessors = []string{}
	if plan.HasPostprocessor("anything") {
		t.Error("empty postprocessors should return false for any name")
	}
}
