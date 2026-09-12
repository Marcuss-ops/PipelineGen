package overlays

import (
	"encoding/json"
	"testing"
)

func TestOverlayItemMarshalIncludesDerivedDurationMS(t *testing.T) {
	item := OverlayItem{
		ID:         "phrase-1",
		TemplateID: "IMPORTANT_PHRASE",
		StartMs:    667,
		EndMs:      2000,
	}

	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal overlay item: %v", err)
	}
	var got struct {
		StartMS    int64 `json:"start_ms"`
		EndMS      int64 `json:"end_ms"`
		DurationMS int64 `json:"duration_ms"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode overlay item: %v", err)
	}
	if got.StartMS != 667 || got.EndMS != 2000 || got.DurationMS != 1333 {
		t.Fatalf("timing tuple = (%d,%d,%d), want (667,2000,1333)", got.StartMS, got.EndMS, got.DurationMS)
	}
}
