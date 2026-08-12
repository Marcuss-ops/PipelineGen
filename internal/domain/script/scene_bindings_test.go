package script

import (
	"encoding/json"
	"testing"
)

func TestSceneBindings_MultiClipJSONPreservesLegacyAlias(t *testing.T) {
	bindings := SceneBindings{
		Clips: []ClipBinding{
			{ClipID: "clip-a"},
			{ClipID: "clip-b"},
		},
		Clip: &ClipBinding{ClipID: "clip-a"},
	}

	encoded, err := json.Marshal(bindings)
	if err != nil {
		t.Fatalf("marshal SceneBindings: %v", err)
	}

	var decoded struct {
		Clips []ClipBinding `json:"clips"`
		Clip  *ClipBinding  `json:"clip"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal SceneBindings: %v", err)
	}
	if len(decoded.Clips) != 2 {
		t.Fatalf("decoded clips = %d, want 2", len(decoded.Clips))
	}
	if decoded.Clips[0].ClipID != "clip-a" || decoded.Clips[1].ClipID != "clip-b" {
		t.Fatalf("decoded clips = %+v, want clip-a then clip-b", decoded.Clips)
	}
	if decoded.Clip == nil || decoded.Clip.ClipID != "clip-a" {
		t.Fatalf("decoded legacy clip = %+v, want first clip-a binding", decoded.Clip)
	}
}

func TestSceneBindings_LegacyClipJSONStillUnmarshals(t *testing.T) {
	var bindings SceneBindings
	if err := json.Unmarshal([]byte(`{"clip":{"clip_id":"legacy-clip"}}`), &bindings); err != nil {
		t.Fatalf("unmarshal legacy SceneBindings: %v", err)
	}
	if bindings.Clip == nil || bindings.Clip.ClipID != "legacy-clip" {
		t.Fatalf("legacy clip = %+v, want legacy-clip binding", bindings.Clip)
	}
}
