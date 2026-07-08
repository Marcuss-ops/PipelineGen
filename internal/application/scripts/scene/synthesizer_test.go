// Package scene_test — synthesizer_test.go: hermetic TDD coverage
// for SceneSynthesizer.FromProse + the 6 unexported prose-parsing
// helpers (buildScenesFromProse, splitProseSentences,
// cleanProseFallbackText, kindForPosition + the JSON helpers).
//
// External test package (scene_test): exercises only the canonical
// exported API. The 6 unexported helpers are verified INDIRECTLY
// through FromProse behaviour (godlike/06 SSOT — the package-owned
// seam is the canonical surface; the helpers stay private per
// godlike/07 minimum-blast-radius).
package scene_test

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestSceneSynthesizer_FromProse_NilOrEmpty covers the canonical
// no-op branches preserved verbatim from the pre-Phase-2
// buildScenesFromProse behaviour.
func TestSceneSynthesizer_FromProse_NilOrEmpty(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()

	cases := []struct {
		name string
		text string
		n    int
	}{
		{"n<=0", "Some prose text here.", 0},
		{"n<0", "Some prose text here.", -3},
		{"empty text", "", 3},
		{"whitespace-only text", "   \n\t  ", 3},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := s.FromProse(tc.text, tc.n)
			if got != nil {
				t.Fatalf("FromProse(%q, %d) = %v scenes, want nil", tc.text, tc.n, len(got))
			}
		})
	}
}

// TestSceneSynthesizer_FromProse_RespectsNWidth verifies the
// canvas-size contract: FromProse must always return exactly N
// scenes (the canonical "balanced distribution" promise). Empty
// chunks land "Scene {i+1}" placeholders so SpecScene.Validate
// passes (model_output.go:265 contract).
func TestSceneSynthesizer_FromProse_RespectsNWidth(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "First sentence here. Second sentence here. Third sentence here. Fourth sentence here."

	for _, n := range []int{1, 2, 3, 4, 5, 6, 7} {
		got := s.FromProse(prose, n)
		if len(got) != n {
			t.Errorf("FromProse(_, %d) returned %d scenes, want exactly %d", n, len(got), n)
		}
		for i := range got {
			if got[i].Index != i {
				t.Errorf("scene[%d].Index = %d, want %d", i, got[i].Index, i)
			}
			if got[i].ID == "" {
				t.Errorf("scene[%d].ID is empty (canonical contract: ID present)", i)
			}
			if got[i].Text == "" {
				t.Errorf("scene[%d].Text is empty (placeholder contract must always fire)", i)
			}
		}
	}
}

// TestSceneSynthesizer_FromProse_KindAssignment verifies the
// position-to-kind policy: scene[0]=intro + scene[n-1]=outro for
// n>=3; scene-clip otherwise. Matches the canonical taxonomy in
// model_output.go (SceneIntro / SceneOutro / SceneClip).
func TestSceneSynthesizer_FromProse_KindAssignment(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "Opening line. Middle fight scene. Closing thought."

	// n<3 → every scene is SceneClip (no intro/outro bleed).
	for _, n := range []int{1, 2} {
		got := s.FromProse(prose, n)
		for i := range got {
			if got[i].Kind != scriptpkg.SceneClip {
				t.Errorf("FromProse(_, %d) scene[%d].Kind = %q, want %q (n<3 contract: all SceneClip)",
					n, i, got[i].Kind, scriptpkg.SceneClip)
			}
		}
	}

	// n>=3 → first=SceneIntro, last=SceneOutro, middle=SceneClip.
	got := s.FromProse(prose, 5)
	if got[0].Kind != scriptpkg.SceneIntro {
		t.Errorf("FromProse(_, 5) scene[0].Kind = %q, want %q", got[0].Kind, scriptpkg.SceneIntro)
	}
	if got[4].Kind != scriptpkg.SceneOutro {
		t.Errorf("FromProse(_, 5) scene[4].Kind = %q, want %q", got[4].Kind, scriptpkg.SceneOutro)
	}
	for i := 1; i < 4; i++ {
		if got[i].Kind != scriptpkg.SceneClip {
			t.Errorf("FromProse(_, 5) scene[%d].Kind = %q, want %q (middle-scene SceneClip contract)",
				i, got[i].Kind, scriptpkg.SceneClip)
		}
	}
}

// TestSceneSynthesizer_FromProse_JSONEnvelopeStripped verifies the
// canonical pattern: when a model emits structured-output JSON
// with a "text" key alongside schema_version / specscene keys, the
// binder-level cleanProseFallbackText helper strips the JSON
// envelope and keeps the tail as the prose.
//
// Imported as scene_test external package — the JSON-stripping
// happens INSIDE FromProse (calls cleanProseFallbackText), so we
// can verify the observable FromProse output.
func TestSceneSynthesizer_FromProse_JSONEnvelopeStripped(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()

	jsonNoise := `{"schema_version": 1, "specscene": {"version": 1, "scenes": []}, "text": "First sentence. Second sentence. Third sentence."}`
	got := s.FromProse(jsonNoise, 3)
	if len(got) != 3 {
		t.Fatalf("FromProse on JSON envelope returned %d scenes, want 3", len(got))
	}
	// Each scene's text should be the cleaned prose — NOT contain
	// "schema_version" or "specscene" raw.
	for i, scene := range got {
		if strings.Contains(scene.Text, "schema_version") ||
			strings.Contains(scene.Text, `"specscene"`) {
			t.Errorf("scene[%d].Text still contains JSON noise: %q", i, scene.Text)
		}
	}
}

// TestSceneSynthesizer_FromProse_SentenceAwareChunking verifies
// that the prose chunker respects sentence boundaries (does NOT
// split in the middle of a sentence).
func TestSceneSynthesizer_FromProse_SentenceAwareChunking(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	prose := "First short. Second short. Third short. Fourth short. Fifth short."

	got := s.FromProse(prose, 3)
	if len(got) != 3 {
		t.Fatalf("FromProse returned %d scenes, want 3", len(got))
	}
	// Each chunk should end in a sentence boundary (period, !, or ?)
	// per the splitProseSentences contract — partial-sentence chunks
	// would be a regression of the heuristic.
	for i := range got {
		txt := strings.TrimSpace(got[i].Text)
		if txt == "" {
			continue
		}
		endsWithBoundary := strings.HasSuffix(txt, ".") ||
			strings.HasSuffix(txt, "!") ||
			strings.HasSuffix(txt, "?")
		if !endsWithBoundary {
			t.Errorf("scene[%d].Text %q does not end in a sentence boundary (split-in-middle regression)",
				i, txt)
		}
	}
}

// TestSceneSynthesizer_FromProse_ThreeScenesIntroClipOutro verifies
// the canonical n=3 case: scene[0]=SceneIntro, scene[1]=SceneClip,
// scene[2]=SceneOutro. Also pins the ID='scene-0' + Index=0
// contract for the first scene (the canonical "balanced 3-scene
// bundle" shape used by the document+voiceover orchestration).
func TestSceneSynthesizer_FromProse_ThreeScenesIntroClipOutro(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "First sentence. Second sentence. Third sentence. Fourth sentence."

	got := s.FromProse(prose, 3)
	if len(got) != 3 {
		t.Fatalf("FromProse returned %d scenes, want 3", len(got))
	}

	if got[0].Kind != scriptpkg.SceneIntro {
		t.Errorf("scene[0].Kind = %q, want %q", got[0].Kind, scriptpkg.SceneIntro)
	}
	if got[1].Kind != scriptpkg.SceneClip {
		t.Errorf("scene[1].Kind = %q, want %q", got[1].Kind, scriptpkg.SceneClip)
	}
	if got[2].Kind != scriptpkg.SceneOutro {
		t.Errorf("scene[2].Kind = %q, want %q", got[2].Kind, scriptpkg.SceneOutro)
	}

	if got[0].ID != "scene-0" {
		t.Errorf("scene[0].ID = %q, want %q", got[0].ID, "scene-0")
	}
	if got[0].Index != 0 {
		t.Errorf("scene[0].Index = %d, want 0", got[0].Index)
	}
}

// TestSceneSynthesizer_FromProse_TwoScenesAreClipKind verifies the
// n<3 contract: every scene is SceneClip (no intro/outro bleed for
// short bundles — the "every requested clip is a real narrative
// beat" intent wins over the "frame with intro/outro" heuristic).
func TestSceneSynthesizer_FromProse_TwoScenesAreClipKind(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "One. Two. Three."

	got := s.FromProse(prose, 2)
	if len(got) != 2 {
		t.Fatalf("FromProse returned %d scenes, want 2", len(got))
	}

	for i, sc := range got {
		if sc.Kind != scriptpkg.SceneClip {
			t.Errorf("scene[%d].Kind = %q, want %q (n<3 contract: all SceneClip)",
				i, sc.Kind, scriptpkg.SceneClip)
		}
	}
}

// TestSceneSynthesizer_CleansJSONEnvelopeNoise verifies the
// canonical JSON-envelope stripping contract via the exported
// CleanProseFallbackText wrapper. The canonical pattern: when a
// model emits structured-output JSON (with "text" key +
// schema_version noise) FOLLOWED BY prose after the closing brace,
// the function drops the envelope and returns the trailing prose
// (the §4 test in the pasted plan exercises this surface directly
// instead of going through FromProse orchestration).
func TestSceneSynthesizer_CleansJSONEnvelopeNoise(t *testing.T) {
	t.Parallel()

	input := `{"schema_version":1,"text":"bad"} Real prose starts here.`
	got := scene.CleanProseFallbackText(input)
	if got != "Real prose starts here." {
		t.Errorf("CleanProseFallbackText returned %q, want %q", got, "Real prose starts here.")
	}
}

// TestSceneSynthesizer_NewSynthesizer_IsIdempotent verifies the
// stateless contract: multiple synthesizers produce byte-stable
// output for identical inputs (no RNG seeds, no clock state, no
// logger state — guaranteed deterministic).
func TestSceneSynthesizer_NewSynthesizer_IsIdempotent(t *testing.T) {
	t.Parallel()
	s1 := scene.NewSceneSynthesizer()
	s2 := scene.NewSceneSynthesizer()
	const prose2 = "Idempotent prose. Should yield identical scenes. Across multiple calls."
	got1 := s1.FromProse(prose2, 4)
	got2 := s2.FromProse(prose2, 4)
	if len(got1) != 4 || len(got2) != 4 {
		t.Fatalf("len mismatch: s1=%d, s2=%d (want 4)", len(got1), len(got2))
	}
	for i := range got1 {
		if got1[i].Text != got2[i].Text {
			t.Errorf("scene[%d].Text: s1=%q, s2=%q (idempotent contract violated)",
				i, got1[i].Text, got2[i].Text)
		}
	}
}
