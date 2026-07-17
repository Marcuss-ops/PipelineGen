package remotionjob

import (
	"encoding/json"
	"testing"
)

func TestRenderJobJSONContract(t *testing.T) {
	job := RenderJob{
		SchemaVersion:    SchemaVersion,
		ID:               "job-001",
		Composition:      "YouTubeShortComposition",
		DurationInFrames: 300,
		FPS:              30,
		Width:            1080,
		Height:           1920,
		Props:            map[string]any{"quoteText": "A deterministic hand-off."},
	}

	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal RenderJob: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal RenderJob: %v", err)
	}
	if got, want := decoded["schemaVersion"], SchemaVersion; got != want {
		t.Fatalf("schemaVersion = %v, want %v", got, want)
	}
	if got, want := decoded["composition"], "YouTubeShortComposition"; got != want {
		t.Fatalf("composition = %v, want %v", got, want)
	}
}
