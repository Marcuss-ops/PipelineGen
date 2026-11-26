package finaljob

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// legacyCompositeVideoScene is the map spelling FinalJobRemoteScene replaced,
// frozen here as the characterization oracle. It is deliberately a copy of the
// removed implementation (including the zero-width separator and the
// local_path strip): the point of the typed struct is a compile-time contract,
// not a wire change, so the serialized form must stay identical.
func legacyCompositeVideoScene(id string, index int, kind string, asset map[string]any, durationMS int64, text string) map[string]any {
	ref := make(map[string]any, len(asset))
	for k, v := range asset {
		if k == "local_path" {
			continue
		}
		ref[k] = v
	}
	if driveID := FinalJobStringValue(ref["drive_file_id"]); driveID != "" {
		ref["url"] = "velox-drive://" + strings.TrimSpace(driveID)
	}
	scene := map[string]any{"scene_id": id, "index": index, "kind": kind, "duration_seconds": float64(durationMS) / 1000, "stock": ref}
	if kind == "stock" || strings.TrimSpace(text) == "" {
		text = "\u200b"
	}
	scene["text"] = text
	return scene
}

// TestCompositeVideoSceneJSONUnchanged is the safety net for replacing the
// map[string]any scene with FinalJobRemoteScene: the serialized form of a scene
// must stay byte-identical to the historical map spelling. Two facts are
// load-bearing and easy to lose in a refactor:
//   - local_path never leaves the Producer (worker-local path),
//   - a Drive reference is mirrored into url as the velox-drive locator.
//
// The Master today consumes the typed CompiledPlanV2, but a scene is still the
// unit a diagnostic or a future DTO serializes; a silent key change here would
// surface as a remote prefetch rejection long after the plan was accepted.
func TestCompositeVideoSceneJSONUnchanged(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		index    int
		kind     string
		asset    map[string]any
		duration int64
		text     string
	}{
		{
			name: "stock scene with a drive reference", id: "scene-0001", index: 1, kind: "stock", duration: 5000,
			asset: map[string]any{
				"asset_id": "stock-1", "drive_file_id": "drive-1", "sha256": "abc",
				"local_path": "/tmp/worker-only.mp4", "duration_ms": int64(5000),
			},
			text: "narration is discarded for stock",
		},
		{name: "clip scene keeps its caption", id: "scene-0000", index: 0, kind: "clip", duration: 1234, text: "Testo"},
		{name: "empty media slot", id: "scene-0002", index: 2, kind: "clip", duration: 999, text: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typed, err := json.Marshal(CompositeVideoScene(tc.id, tc.index, tc.kind, tc.asset, tc.duration, tc.text))
			if err != nil {
				t.Fatalf("marshal typed scene: %v", err)
			}
			legacy, err := json.Marshal(legacyCompositeVideoScene(tc.id, tc.index, tc.kind, tc.asset, tc.duration, tc.text))
			if err != nil {
				t.Fatalf("marshal legacy scene: %v", err)
			}
			// Compare the decoded documents, not the raw bytes: Go emits map keys
			// sorted and struct fields in declaration order, so the byte order
			// differs by construction and is not part of any contract. The key
			// set and every value must match exactly, and no consumer hashes a
			// scene (the sealed plan is the typed CompiledPlanV2).
			var typedDoc, legacyDoc any
			if err := json.Unmarshal(typed, &typedDoc); err != nil {
				t.Fatalf("unmarshal typed scene: %v", err)
			}
			if err := json.Unmarshal(legacy, &legacyDoc); err != nil {
				t.Fatalf("unmarshal legacy scene: %v", err)
			}
			if !reflect.DeepEqual(typedDoc, legacyDoc) {
				t.Fatalf("scene JSON changed\n typed: %s\nlegacy: %s", typed, legacy)
			}
		})
	}
}

// TestCompositeVideoSceneStockPoolSlotMarshalsOnlyWhenSet keeps the optional
// marker out of the wire for every ordinary scene: the historical map set the
// key only for a stock-pool slot, so an always-present false would still be a
// wire change.
func TestCompositeVideoSceneStockPoolSlotMarshalsOnlyWhenSet(t *testing.T) {
	scene := CompositeVideoScene("scene-0002", 2, "stock", nil, 2000, "")
	raw, err := json.Marshal(scene)
	if err != nil {
		t.Fatalf("marshal scene: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal scene: %v", err)
	}
	if _, present := decoded["stock_pool_slot"]; present {
		t.Fatalf("stock_pool_slot present on an ordinary scene: %s", raw)
	}
	scene.StockPoolSlot = true
	raw, err = json.Marshal(scene)
	if err != nil {
		t.Fatalf("marshal pool scene: %v", err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal pool scene: %v", err)
	}
	if decoded["stock_pool_slot"] != true {
		t.Fatalf("stock_pool_slot missing on a pool scene: %s", raw)
	}
}
