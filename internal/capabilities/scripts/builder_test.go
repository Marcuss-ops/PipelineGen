package scriptgeneration

import (
	"encoding/json"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestBuildGenerateRequest_MapsExplicitDocsConfig(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	err := json.Unmarshal([]byte(`{
		"version": 2,
		"items": [{
			"title": "test",
			"language": "it",
			"source": {"type": "text", "topic": "topic"},
			"docs": {"enabled": true, "languages": ["it"], "folder_id": "folder"}
		}]
	}`), &env)
	if err != nil {
		t.Fatal(err)
	}

	got, err := BuildGenerateRequest(&env, "key")
	if err != nil {
		t.Fatal(err)
	}

	if !got.Docs.Enabled || got.Docs.FolderID != "folder" {
		t.Fatalf("docs config not mapped: %+v", got.Docs)
	}
	if len(got.Docs.Languages) != 1 || got.Docs.Languages[0] != "it" {
		t.Fatalf("docs languages not mapped: %v", got.Docs.Languages)
	}
}

func TestBuildGenerateRequest_MapsExplicitAudioMode(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"combined","language":"en","source":{"type":"text","topic":"topic"},"output":{"generate_timeline":true,"voiceover_enabled":true},"audio":{"mode":"COMBINED_TIMELINE"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := BuildGenerateRequest(&env, "audio-mode-key")
	if err != nil {
		t.Fatal(err)
	}
	if got.Audio != capabilityaudio.AudioModeCombinedTimeline {
		t.Fatalf("audio mode = %q, want %q", got.Audio, capabilityaudio.AudioModeCombinedTimeline)
	}
}

func TestBuildGenerateRequestRejectsCombinedAudioWithoutRender(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(`{"version":2,"items":[{"title":"invalid","language":"en","source":{"type":"text","topic":"topic"},"audio":{"mode":"COMBINED_TIMELINE"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildGenerateRequest(&env, "audio-mode-invalid"); err == nil {
		t.Fatal("expected combined mode without render_video to fail")
	}
}
