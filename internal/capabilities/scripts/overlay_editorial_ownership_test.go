package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// editorialSnapshot is the part of an item that Rust owns after the seal.
// PresetID and Params are deliberately excluded: materialization may set a
// preset or attach map/chain metadata, but it must never reorder items or
// rewrite identity, timing, kind, template, text or motion.
type editorialSnapshot struct {
	ID            string
	SceneID       string
	Kind          string
	TemplateID    string
	Text          string
	EntityCaption string
	MotionID      string
	GroupMotionID string
	StartUS       int64
	DurationUS    int64
	StartMs       int64
	EndMs         int64
	MapProvider   string
	MapPins       int
	MapMotionID   string
	// Segments carries the same editorial fields for every chain segment.
	// Segments are excluded from the item snapshot above, so without this a
	// post-seal pass could rewrite a chain's identity segment by segment and
	// stay invisible. PresetID is deliberately absent: preset resolution is a
	// declared post-seal materialization step (ADR-029), and it is pinned
	// separately by TestChainSegmentPresetResolutionTouchesOnlyPresetID.
	Segments []segmentEditorialSnapshot
}

// segmentEditorialSnapshot is the Rust-owned part of one chain segment.
type segmentEditorialSnapshot struct {
	Kind       string
	TemplateID string
	Text       string
	EntityID   string
	MotionID   string
	GroupID    string
	StartUS    int64
	DurationUS int64
}

func snapshotEditorial(plan *capmodel.OverlayPlan) []editorialSnapshot {
	out := make([]editorialSnapshot, len(plan.Items))
	for i, item := range plan.Items {
		out[i] = editorialSnapshot{
			ID: item.ID, SceneID: item.SceneID, Kind: item.Kind,
			TemplateID: item.TemplateID, Text: item.Text,
			EntityCaption: item.EntityCaption, MotionID: item.MotionID,
			GroupMotionID: item.GroupMotionID,
			StartUS:       item.StartUS, DurationUS: item.DurationUS,
			StartMs: item.StartMs, EndMs: item.EndMs,
		}
		if item.Map != nil {
			out[i].MapProvider = item.Map.Provider
			out[i].MapPins = len(item.Map.Pins)
			out[i].MapMotionID = item.Map.MotionID
		}
		if segments, ok := capmodel.ChainSegmentMaps(item.Params); ok {
			out[i].Segments = make([]segmentEditorialSnapshot, 0, len(segments))
			for _, segment := range segments {
				out[i].Segments = append(out[i].Segments, segmentEditorialSnapshot{
					Kind:       segmentString(segment, "kind"),
					TemplateID: segmentString(segment, "template_id"),
					Text:       segmentString(segment, "text"),
					EntityID:   segmentString(segment, "entity_id"),
					MotionID:   segmentString(segment, "motion_id"),
					GroupID:    segmentString(segment, "group_id"),
					StartUS:    segmentInt(segment, "start_us"),
					DurationUS: segmentInt(segment, "duration_us"),
				})
			}
		}
	}
	return out
}

func segmentString(segment map[string]any, key string) string {
	value, _ := segment[key].(string)
	return value
}

func segmentInt(segment map[string]any, key string) int64 {
	switch value := segment[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	}
	return 0
}

// TestPostRustPlanPreservesEditorialDecisions is the ownership invariant for
// the Go boundary after BrainRust returns a sealed plan: materialization may
// attach presets, chain metadata and local map assets, but the editorial
// decisions (order, kind, timing, text, motion, template) are read-only.
func TestPostRustPlanPreservesEditorialDecisions(t *testing.T) {
	plan := &capmodel.OverlayPlan{
		PlanID: "post-seal", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Items: []capmodel.OverlayItem{
			{
				ID: "phrase-b", SceneID: "scene-2", Kind: "text_phrase",
				TemplateID: "IMPORTANT_PHRASE", Text: "second",
				StartUS: 5_000_000, DurationUS: 1_000_000,
				StartMs: 5000, EndMs: 6000, MotionID: "phrase_default",
				PresetID: "phrase_default",
			},
			{
				ID: "chain", SceneID: "scene-1", Kind: "mixed_chain",
				TemplateID: "MIXED_CHAIN", StartUS: 2_000_000, DurationUS: 2_000_000,
				StartMs: 2000, EndMs: 4000,
				Params: map[string]any{
					"chain_segments": []any{
						map[string]any{"kind": "location", "template_id": "gpe_default"},
					},
				},
			},
			{
				ID: "map", SceneID: "scene-1", Kind: "map",
				TemplateID: "MAP", StartUS: 1_000_000, DurationUS: 2_000_000,
				StartMs: 1000, EndMs: 3000,
				Map: &capmodel.MapOverlay{
					Provider: "local", SourceID: "natural-earth", MotionID: "map_zoom",
					Pins: []capmodel.MapOverlayPin{{}},
				},
			},
			{
				ID: "phrase-a", SceneID: "scene-3", Kind: "text_phrase",
				TemplateID: "IMPORTANT_PHRASE", Text: "first",
				StartUS: 7_000_000, DurationUS: 1_000_000,
				StartMs: 7000, EndMs: 8000, MotionID: "phrase_default",
				PresetID: "phrase_default",
			},
		},
	}
	before := snapshotEditorial(plan)
	require.NoError(t, materializeEditorialMapItems(plan, nil, OverlayCanvasSpec{}, "seed"))
	after := snapshotEditorial(plan)

	require.Equal(t, before, after, "post-seal materialization changed an editorial field")
	require.Equal(t, []string{"phrase-b", "chain", "map", "phrase-a"},
		[]string{plan.Items[0].ID, plan.Items[1].ID, plan.Items[2].ID, plan.Items[3].ID},
		"materialization must not reorder Rust-selected items")
}

// A Rust map item without a grounded declaration must fail closed at the
// materialization boundary instead of silently dropping the map.
func TestPostRustMapWithoutDeclarationFailsClosed(t *testing.T) {
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{{
		ID: "map", Kind: "map", TemplateID: "MAP",
	}}}
	err := materializeEditorialMapItems(plan, nil, OverlayCanvasSpec{}, "seed")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without a grounded map declaration")
}
