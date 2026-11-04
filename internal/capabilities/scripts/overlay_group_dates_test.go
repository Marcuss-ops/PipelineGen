package scriptgeneration

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"testing"
)

func TestOverlayGroupDatesCapsNumberAndDateCards(t *testing.T) {
	items := []capmodel.OverlayItem{
		{ID: "date-a", SceneID: "scene-1", Kind: "number", TemplateID: "TIMELINE_DATE_CARD"},
		{ID: "date-b", SceneID: "scene-1", Kind: "number", TemplateID: "TIMELINE_DATE_CARD"},
		{ID: "date-c", SceneID: "scene-2", Kind: "number", TemplateID: "TIMELINE_DATE_CARD"},
		{ID: "metric", SceneID: "scene-2", Kind: "number", TemplateID: "METRIC_STAT_CARD"},
		{ID: "phrase", SceneID: "scene-1", Kind: "text_phrase", Text: "Garlasco, provincia di Pavia"},
	}
	if got := overlayGroupOf(items[0]); got != overlayGroupDates {
		t.Fatalf("date group = %q, want %q", got, overlayGroupDates)
	}
	if got := applyOverlayGroupLimits(append([]capmodel.OverlayItem(nil), items...), map[string]int{overlayGroupDates: 2}); len(got) != 3 {
		t.Fatalf("capped items = %d, want 2 dates + 1 phrase: %+v", len(got), got)
	}
	kept := applyOverlayGroupLimits(append([]capmodel.OverlayItem(nil), items...), map[string]int{overlayGroupDates: 0})
	if len(kept) != 1 || kept[0].ID != "phrase" {
		t.Fatalf("zero date cap kept %+v, want only the phrase", kept)
	}
}
