package script

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestVoiceoverTimingBinding_JSONRoundTrip pins the wire contract of the
// per-language timing bundle: the canonical timing.json link, SRT/VTT
// projections and SHA-256 bindings survive a JSON round-trip (the exact
// path used by script-row persistence and by the job result payload
// surfaced via GET /api/jobs/:id/full). The word-level timing array is
// intentionally NOT inlined in the binding — it lives in the published
// timing.json artifact.
func TestVoiceoverTimingBinding_JSONRoundTrip(t *testing.T) {
	binding := &VoiceoverBinding{
		Status: "completed",
		Link:   "https://drive.google.com/file/d/audio-it/view",
		Links:  map[string]string{"it": "https://drive.google.com/file/d/audio-it/view"},
		Timing: map[string]VoiceoverTimingBinding{
			"it": {
				Status:       "completed",
				JSONLink:     "https://drive.google.com/file/d/timing-it/view",
				SRTLink:      "https://drive.google.com/file/d/subtitles-it-srt/view",
				VTTLink:      "https://drive.google.com/file/d/subtitles-it-vtt/view",
				BoundaryMode: "word",
				WordCount:    184,
				DurationUS:   18_342_000,
				TextSHA256:   strings.Repeat("a", 64),
				AudioSHA256:  strings.Repeat("b", 64),
			},
		},
	}

	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("marshal voiceover binding: %v", err)
	}
	if strings.Contains(string(raw), `"words"`) {
		t.Fatal("the voiceover binding must not inline the word-level timing array")
	}

	var decoded VoiceoverBinding
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal voiceover binding: %v", err)
	}
	timing, ok := decoded.Timing["it"]
	if !ok {
		t.Fatalf("timing map was not preserved in the round-trip: %s", raw)
	}
	if timing.Status != "completed" || timing.JSONLink != "https://drive.google.com/file/d/timing-it/view" ||
		timing.SRTLink != "https://drive.google.com/file/d/subtitles-it-srt/view" ||
		timing.VTTLink != "https://drive.google.com/file/d/subtitles-it-vtt/view" ||
		timing.BoundaryMode != "word" || timing.WordCount != 184 || timing.DurationUS != 18_342_000 ||
		timing.TextSHA256 != strings.Repeat("a", 64) || timing.AudioSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("timing bundle fields drifted in the round-trip: %+v", timing)
	}
}

// TestVoiceoverTimingBinding_EmptyStatusSurvives verifies that an
// explicit best-effort failure status ("failed"/"unavailable") survives
// the JSON round-trip instead of being dropped as an empty value — the
// no-fake-availability contract for absent timing.
func TestVoiceoverTimingBinding_EmptyStatusSurvives(t *testing.T) {
	binding := &VoiceoverBinding{
		Status: "completed",
		Timing: map[string]VoiceoverTimingBinding{
			"it": {Status: "failed"},
		},
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded VoiceoverBinding
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := decoded.Timing["it"].Status; got != "failed" {
		t.Fatalf("timing status = %q, want failed (explicit absence must survive)", got)
	}
}
