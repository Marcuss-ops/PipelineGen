package adapters_test

import (
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	generation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/generation"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"testing"
)

func TestApplyPresetNilItem(t *testing.T) {
	scripts.ApplyPreset(nil, scriptpkg.PresetWithImages)
	// Must not panic.
}

// ── FullMedia preset tests ────────────────────────────────────────────────
//
// Per §6 row 3 of docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md,
// `full_media` enables voiceover when the caller leaves the capability
// unset, while preserving explicit enable/disable choices.

// TestApplyPresetFullMedia_DoesNothingWhenExplicit verifies that caller-set
// fields are preserved.

// TestApplyPresetFullMedia_DoesNotPromoteDeprecatedFlags verifies that unset
// fields remain unset.

// TestApplyPresetFullMedia_CallerPrecedencePreserved verifies per-field
// caller precedence.

func TestNormalizeItemFullMediaResolvesVoiceoverCapability(t *testing.T) {
	item := scriptpkg.GenerationItemV2{}
	scripts.ApplyPreset(&item, scriptpkg.PresetFullMedia)
	if item.Output.VoiceoverEnabled != scriptpkg.ToggleEnabled {
		t.Fatalf("full_media voiceover_enabled=%q, want enabled", item.Output.VoiceoverEnabled)
	}
}

func TestNormalizeItemExplicitVoiceoverDisabledWinsOverRouting(t *testing.T) {
	item := scriptpkg.GenerationItemV2{Output: scriptpkg.OutputSpec{
		VoiceoverEnabled:  scriptpkg.ToggleDisabled,
		VoiceoverGroup:    "Comedy",
		VoiceoverFolderID: "folder-id",
	}}
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())
	if item.Output.VoiceoverEnabled != scriptpkg.ToggleDisabled {
		t.Fatalf("voiceover_enabled=%q, want disabled", item.Output.VoiceoverEnabled)
	}
}

func TestNormalizeItemLegacyVoiceoverRoutingEnablesCapability(t *testing.T) {
	item := scriptpkg.GenerationItemV2{Output: scriptpkg.OutputSpec{VoiceoverGroup: "Comedy"}}
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())
	if item.Output.VoiceoverEnabled != scriptpkg.ToggleEnabled {
		t.Fatalf("legacy routing voiceover_enabled=%q, want enabled", item.Output.VoiceoverEnabled)
	}
}

// ── Catalog preset test ───────────────────────────────────────────────────

// TestApplyPresetCatalog_PassThrough verifies that `catalog` is a
// pass-through preset: it does not touch ANY field on the item, including
// Source (even when Source.ClipIDs is empty). The HTTP handler binds
// Source.Kind=SourceCatalog upstream; the preset simply carries the intent.
// Per §6 row 4: catalog | source.kind=catalog | none.

// ── Search preset test ────────────────────────────────────────────────────

// TestApplyPresetSearch_PassThrough verifies that `search` is a
// pass-through preset: it does not touch ANY field on the item, including
// Source (even when Source.Query is empty). The HTTP handler binds
// Source.Kind=SourceSearch upstream; the preset simply carries the intent.
// Per §6 row 5: search | source.kind=search | none.

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

func TestNormalizeItemDefaultsClipTranscriptPolicyToStrict(t *testing.T) {
	item := clipsItem()
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, defaultCfg())

	if got := item.Source.TranscriptPolicy; got != scriptpkg.TranscriptPolicyStrict {
		t.Fatalf("clip transcript policy = %q, want strict", got)
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

func TestNormalizeItemMediaDensityExpandsCadenceAndPreservesOverrides(t *testing.T) {
	cfg := adapters.NormalizationConfig{DefaultSentencesPerImage: 10, DefaultImagesPerScene: 2}
	item := &scriptpkg.GenerationItemV2{ScriptParams: scriptpkg.ScriptSpec{MediaDensity: "dense"}}
	adapters.NormalizeItem(item, scriptpkg.PresetCustom, cfg)
	if item.ScriptParams.SentencesPerImage != 4 || item.ScriptParams.ImagesPerScene != 2 {
		t.Fatalf("dense cadence = (%d,%d), want (4,2)", item.ScriptParams.SentencesPerImage, item.ScriptParams.ImagesPerScene)
	}
	item = &scriptpkg.GenerationItemV2{ScriptParams: scriptpkg.ScriptSpec{
		MediaDensity: "sparse", SentencesPerImage: 3,
	}}
	adapters.NormalizeItem(item, scriptpkg.PresetCustom, cfg)
	if item.ScriptParams.SentencesPerImage != 3 || item.ScriptParams.ImagesPerScene != 1 {
		t.Fatalf("partial cadence override = (%d,%d), want (3,1)", item.ScriptParams.SentencesPerImage, item.ScriptParams.ImagesPerScene)
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

	plan := generation.BuildPlan(item)

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

	plan := generation.BuildPlan(item)

	if plan.Mode != "clip_to_script" {
		t.Errorf("plan.Mode for clips: %q", plan.Mode)
	}
	// Clip evidence is nil at plan-build time — source resolver fills it later.
	if plan.ClipEvidence != nil {
		t.Error("plan.ClipEvidence should be nil before source resolution")
	}
}

func TestBuildPlanDeterministic(t *testing.T) {
	cfg := defaultCfg()
	item := textItem()
	adapters.NormalizeItem(&item, scriptpkg.PresetCustom, cfg)

	plan1 := generation.BuildPlan(item)
	plan2 := generation.BuildPlan(item)

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

	plan := generation.BuildPlan(item)

	// The plan must not contain any legacy endpoint name string.
	if plan.Mode == "generate_with_images" ||
		plan.Mode == "generate_batch" ||
		plan.Mode == "generate_from_catalog" {
		t.Errorf("plan.Mode contains legacy endpoint name: %q", plan.Mode)
	}
}
