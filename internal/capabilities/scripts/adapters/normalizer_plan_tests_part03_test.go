package adapters_test

import (
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	generation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/generation"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"strings"
	"testing"
)

func TestBuildPlansEmpty(t *testing.T) {
	plans := generation.BuildPlans(nil)
	if plans != nil {
		t.Errorf("BuildPlans(nil) should return nil, got %v", plans)
	}
	plans = generation.BuildPlans([]scriptpkg.GenerationItemV2{})
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

	plans := generation.BuildPlans([]scriptpkg.GenerationItemV2{item1, item2})

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

	plan := generation.BuildPlan(item)

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

	plan := generation.BuildPlan(item)

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

	plan := generation.BuildPlan(item)

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
