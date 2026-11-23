package script

import (
	"strings"
	"testing"
)

// TestNarrationWordFloor pins the ONE floor contract shared by the ingress,
// `velox preflight` and the runner: authored narration needs
// MinimumClipSceneWords, clip cutaways and intro copy are exempt.
func TestNarrationWordFloor(t *testing.T) {
	cases := []struct {
		name    string
		segment ScriptSegment
		want    int
	}{
		{"narration uses the canonical floor", ScriptSegment{Kind: "narration"}, MinimumClipSceneWords},
		{"clip narration uses the canonical floor", ScriptSegment{Kind: "clip"}, MinimumClipSceneWords},
		{"explicit min_words wins", ScriptSegment{Kind: "narration", MinWords: 20}, 20},
		{"intro is exempt", ScriptSegment{Kind: "intro"}, 0},
		{"intro is exempt even with min_words", ScriptSegment{Kind: " Intro ", MinWords: 12}, 0},
		{"no_voiceover cutaway is exempt", ScriptSegment{Kind: "clip", NoVoiceover: true}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NarrationWordFloor(tc.segment); got != tc.want {
				t.Fatalf("NarrationWordFloor(%+v) = %d, want %d", tc.segment, got, tc.want)
			}
		})
	}
}

func TestValidateAuthoredNarration(t *testing.T) {
	short := strings.Repeat("parola ", 10) // ten words, below the floor
	long := strings.Repeat("parola ", MinimumClipSceneWords)

	t.Run("verbatim short narration is rejected at ingress", func(t *testing.T) {
		spec := ScriptSpec{
			SourceTextVerbatim: true,
			Segments: []ScriptSegment{
				{ID: "seg-01", Kind: "narration", SourceText: short},
			},
		}
		problems := ValidateAuthoredNarration(spec)
		if len(problems) != 1 {
			t.Fatalf("want 1 problem, got %v", problems)
		}
		if !strings.Contains(problems[0], "SCRIPT_SCENE_TEXT_INVALID") {
			t.Fatalf("want the runner's stable code in the message, got %q", problems[0])
		}
	})

	t.Run("verbatim narration at the floor passes", func(t *testing.T) {
		spec := ScriptSpec{
			SourceTextVerbatim: true,
			Segments:           []ScriptSegment{{ID: "seg-01", Kind: "narration", SourceText: long}},
		}
		if problems := ValidateAuthoredNarration(spec); len(problems) != 0 {
			t.Fatalf("want no problems, got %v", problems)
		}
	})

	t.Run("intro copy is exempt", func(t *testing.T) {
		spec := ScriptSpec{
			SourceTextVerbatim: true,
			Segments:           []ScriptSegment{{ID: "intro-01", Kind: "intro", SourceText: short}},
		}
		if problems := ValidateAuthoredNarration(spec); len(problems) != 0 {
			t.Fatalf("want no problems, got %v", problems)
		}
	})

	t.Run("no_voiceover cutaway is exempt", func(t *testing.T) {
		spec := ScriptSpec{
			SourceTextVerbatim: true,
			Segments:           []ScriptSegment{{ID: "clip-01", Kind: "clip", NoVoiceover: true, SourceText: short}},
		}
		if problems := ValidateAuthoredNarration(spec); len(problems) != 0 {
			t.Fatalf("want no problems, got %v", problems)
		}
	})

	t.Run("generated prose is not checked statically", func(t *testing.T) {
		spec := ScriptSpec{
			Segments: []ScriptSegment{{ID: "seg-01", Kind: "narration", SourceText: short}},
		}
		if problems := ValidateAuthoredNarration(spec); len(problems) != 0 {
			t.Fatalf("want no problems, got %v", problems)
		}
	})
}

func TestNarrationFloorForScene(t *testing.T) {
	segments := []ScriptSegment{
		{ID: "intro-01", Kind: "intro", MinWords: 1},
		{ID: "clip-01", Kind: "clip", NoVoiceover: true},
		{ID: "seg-01", Kind: "narration"},
	}
	if got := NarrationFloorForScene(segments, "intro-01"); got != 0 {
		t.Fatalf("intro floor = %d, want 0", got)
	}
	if got := NarrationFloorForScene(segments, "clip-01"); got != 0 {
		t.Fatalf("cutaway floor = %d, want 0", got)
	}
	if got := NarrationFloorForScene(segments, "seg-01"); got != MinimumClipSceneWords {
		t.Fatalf("narration floor = %d, want %d", got, MinimumClipSceneWords)
	}
	if got := NarrationFloorForScene(segments, "unknown"); got != MinimumClipSceneWords {
		t.Fatalf("unknown scene must keep the canonical floor, got %d", got)
	}
}
