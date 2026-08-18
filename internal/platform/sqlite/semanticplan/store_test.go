package semanticplan

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	_ "github.com/mattn/go-sqlite3"
)

// testSchema mirrors migration 220 (plus a minimal scripts table so the
// foreign keys resolve when PRAGMA foreign_keys is on).
const testSchema = `
CREATE TABLE IF NOT EXISTS scripts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS script_semantic_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    semantic_id TEXT NOT NULL,
    scene_id TEXT NOT NULL,
    type TEXT NOT NULL,
    subtype TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    normalized_text TEXT NOT NULL,
    canonical_entity_id TEXT NOT NULL DEFAULT '',
    start_char INTEGER NOT NULL,
    end_char INTEGER NOT NULL,
    start_us INTEGER NOT NULL,
    end_us INTEGER NOT NULL,
    confidence REAL NOT NULL DEFAULT 1.0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(script_id, semantic_id)
);

CREATE TABLE IF NOT EXISTS script_visual_bindings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    semantic_id TEXT NOT NULL,
    visual_event_id TEXT NOT NULL,
    preset_family TEXT NOT NULL,
    preset_id TEXT NOT NULL DEFAULT '',
    asset_id TEXT NOT NULL DEFAULT '',
    animation_in TEXT NOT NULL DEFAULT '',
    animation_idle TEXT NOT NULL DEFAULT '',
    animation_out TEXT NOT NULL DEFAULT '',
    start_us INTEGER NOT NULL,
    duration_us INTEGER NOT NULL,
    resolver_version TEXT NOT NULL DEFAULT '',
    sampler_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(script_id, visual_event_id)
);`

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:semanticplan-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(testSchema); err != nil {
		t.Fatal(err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testItems() []entities.SemanticItem {
	return []entities.SemanticItem{
		{
			SemanticID: "sem_scene03_person_01", SceneID: "scene_03", Type: entities.SemanticPerson,
			Text: "Floyd Mayweather", NormalizedText: "floyd mayweather",
			StartChar: 153, EndChar: 170, StartUS: 12_400_000, EndUS: 13_900_000, Confidence: 0.98,
			CanonicalEntityID: "person:floyd-mayweather-jr",
		},
		{
			SemanticID: "sem_scene03_money_02", SceneID: "scene_03", Type: entities.SemanticMoney,
			Text: "100 million dollars", NormalizedText: "100 million dollars",
			StartChar: 180, EndChar: 200, StartUS: 17_200_000, EndUS: 19_500_000, Confidence: 0.95,
		},
	}
}

func testBindings(scriptID int64) []overlays.VisualBinding {
	return []overlays.VisualBinding{
		{
			ScriptID: scriptID, SemanticID: "sem_scene03_person_01", VisualEventID: "visual-scene-03-sem-person-01",
			PresetFamily: "person_image", PresetID: "portrait_right", AssetID: "floyd_img_04",
			AnimationIn: "slide_left", StartUS: 12_400_000, DurationUS: 1_500_000,
			ResolverVersion: overlays.VisualIntentResolverVersion, SamplerVersion: overlays.DeterministicPresetSamplerVersion,
		},
		{
			ScriptID: scriptID, SemanticID: "sem_scene03_money_02", VisualEventID: "visual-scene-03-sem-money-02",
			PresetFamily: "money", PresetID: "money_large_center",
			AnimationIn: "scale_pop", StartUS: 17_200_000, DurationUS: 2_300_000,
			ResolverVersion: overlays.VisualIntentResolverVersion, SamplerVersion: overlays.DeterministicPresetSamplerVersion,
		},
	}
}

func TestSavePlanRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.SavePlan(ctx, 7, testItems(), testBindings(7)); err != nil {
		t.Fatal(err)
	}

	items, err := store.ListItems(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].SemanticID != "sem_scene03_person_01" || items[1].SemanticID != "sem_scene03_money_02" {
		t.Fatalf("items not in play order: %+v", items)
	}
	if items[0].CanonicalEntityID != "person:floyd-mayweather-jr" {
		t.Fatalf("canonical_entity_id did not round-trip: %+v", items[0])
	}

	bindings, err := store.ListBindings(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Fatalf("want 2 bindings, got %d", len(bindings))
	}
	if bindings[0].PresetID != "portrait_right" || bindings[0].AnimationIn != "slide_left" {
		t.Fatalf("binding did not round-trip: %+v", bindings[0])
	}
	if bindings[1].ResolverVersion != overlays.VisualIntentResolverVersion {
		t.Fatalf("resolver_version did not round-trip: %+v", bindings[1])
	}
}

func TestSaveItemsReplacesWholeSet(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.SaveItems(ctx, 7, testItems()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveItems(ctx, 7, testItems()[:1]); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListItems(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("re-save must replace the whole set, got %d rows", len(got))
	}
}

func TestSaveItemsRejectsInvalid(t *testing.T) {
	store := newTestStore(t)
	items := testItems()
	items[0].StartUS = -1
	if err := store.SaveItems(context.Background(), 7, items); err == nil {
		t.Fatal("invalid item must not be persisted")
	}
}

func TestSaveBindingsRejectsMismatchedScriptID(t *testing.T) {
	store := newTestStore(t)
	bindings := testBindings(1) // script id 1
	if err := store.SaveBindings(context.Background(), 2, bindings); err == nil {
		t.Fatal("mismatched binding script_id must be rejected")
	}
}

func TestListEmptyScript(t *testing.T) {
	store := newTestStore(t)
	items, err := store.ListItems(context.Background(), 999)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items, got %d", len(items))
	}
	bindings, err := store.ListBindings(context.Background(), 999)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 {
		t.Fatalf("expected no bindings, got %d", len(bindings))
	}
}

func TestNewRejectsNilDB(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil database must be rejected")
	}
}
