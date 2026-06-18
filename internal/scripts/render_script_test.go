package scripts

import (
	"strings"
	"testing"
)

// TestRenderScript_ClipOnly verifies a pack with only clip scenes (no
// narration) renders as N [Clip: id] blocks separated by blank lines,
// regardless of whether the raw LLM input emitted markers.
func TestRenderScript_ClipOnly(t *testing.T) {
	pack := &ClipSourcePack{
		Clips: []ClipEvidence{
			{ClipID: "clip-A", DriveLink: "https://drive/A"},
			{ClipID: "clip-B", DriveLink: "https://drive/B"},
		},
	}
	raw := "Opening body text.\n\nMid body text.\n\nClosing body text."
	scenes := BuildScenesWithMarkers(raw, pack)
	got := RenderScript(scenes)

	// Exactly 2 clip markers, no narration markers
	clipCount := strings.Count(got, "[Clip:")
	narCount := strings.Count(got, "[Narration:")
	if clipCount != 2 {
		t.Errorf("expected 2 [Clip:] markers, got %d\nscript:\n%s", clipCount, got)
	}
	if narCount != 0 {
		t.Errorf("expected 0 [Narration:] markers, got %d\nscript:\n%s", narCount, got)
	}
	if !strings.Contains(got, "[Clip: clip-A]") {
		t.Error("missing [Clip: clip-A] marker")
	}
	if !strings.Contains(got, "[Clip: clip-B]") {
		t.Error("missing [Clip: clip-B] marker")
	}
}

// TestRenderScript_PreservesLLMMarkers verifies that when the LLM emitted
// perfect markers, RenderScript preserves the layout (no corruption).
func TestRenderScript_PreservesLLMMarkers(t *testing.T) {
	pack := &ClipSourcePack{
		Clips: []ClipEvidence{
			{ClipID: "clip-A", DriveLink: "https://drive/A"},
			{ClipID: "clip-B", DriveLink: "https://drive/B"},
		},
	}
	raw := "[Clip: clip-A]\nFirst clip body.\n\n[Narration: intro]\nOpening hook.\n\n[Clip: clip-B]\nSecond clip body."
	scenes := BuildScenesWithMarkers(raw, pack)
	got := RenderScript(scenes)

	for _, want := range []string{"[Clip: clip-A]", "[Clip: clip-B]", "[Narration: intro]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing expected marker %q\ngot:\n%s", want, got)
		}
	}
	// Body text must be preserved exactly
	if !strings.Contains(got, "First clip body.") {
		t.Error("clip-A body not preserved")
	}
	if !strings.Contains(got, "Second clip body.") {
		t.Error("clip-B body not preserved")
	}
	if !strings.Contains(got, "Opening hook.") {
		t.Error("narration body not preserved")
	}
}

// TestRenderScript_EmptyPack verifies that with zero clips, every
// paragraph becomes a [Narration: transition] scene (consistent default).
func TestRenderScript_EmptyPack(t *testing.T) {
	pack := &ClipSourcePack{}
	raw := "Solo paragraph one.\n\nSolo paragraph two."
	scenes := BuildScenesWithMarkers(raw, pack)
	got := RenderScript(scenes)

	if strings.Contains(got, "[Clip:") {
		t.Errorf("empty pack should produce NO clip markers\ngot:\n%s", got)
	}
	narCount := strings.Count(got, "[Narration:")
	if narCount != 2 {
		t.Errorf("expected 2 [Narration:] markers, got %d\ngot:\n%s", narCount, got)
	}
	if !strings.Contains(got, "[Narration: transition]") {
		t.Errorf("default role should be 'transition'\ngot:\n%s", got)
	}
}

// TestRenderScript_FiltersEmptyScenes verifies that fully-empty pseudo
// scenes (no text, no clip, no role) are silently dropped, not emitted
// as dangling blank markers.
func TestRenderScript_FiltersEmptyScenes(t *testing.T) {
	scenes := []ClipScene{
		{SceneIndex: 1, Kind: "clip", ClipID: "clip-A", Text: "Hello"},
		{SceneIndex: 2, Kind: "", ClipID: "", NarrationRole: "", Text: ""}, // empty
		{SceneIndex: 3, Kind: "clip", ClipID: "clip-B", Text: "World"},
	}
	got := RenderScript(scenes)
	if strings.Contains(got, "[Clip: ]") || strings.Contains(got, "[Narration: ]") {
		t.Errorf("empty scenes should not produce dangling markers\ngot:\n%s", got)
	}
	if !strings.Contains(got, "[Clip: clip-A]") || !strings.Contains(got, "[Clip: clip-B]") {
		t.Errorf("non-empty scene markers should still be present\ngot:\n%s", got)
	}
	want := "[Clip: clip-A]\nHello\n\n[Clip: clip-B]\nWorld"
	if got != want {
		t.Errorf("unexpected output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestRenderScript_DemotesEmptyClipID verifies that scenes with Kind=clip
// but empty ClipID are demoted to [Narration: transition] markers
// (so we never emit `[Clip: ]` with an empty ID, which the validator
// treats as a hard failure).
func TestRenderScript_DemotesEmptyClipID(t *testing.T) {
	scenes := []ClipScene{
		{SceneIndex: 1, Kind: "clip", ClipID: "clip-A", Text: "Real"},
		{SceneIndex: 2, Kind: "clip", ClipID: "", Text: "Orphan"},
	}
	got := RenderScript(scenes)
	if strings.Contains(got, "[Clip: ]") {
		t.Errorf("must never emit empty-ID clip markers\ngot:\n%s", got)
	}
	if !strings.Contains(got, "[Narration: transition]") {
		t.Errorf("empty clip ID scene should be demoted to narration\ngot:\n%s", got)
	}
}
