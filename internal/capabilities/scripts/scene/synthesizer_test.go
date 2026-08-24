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

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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

func TestSceneSynthesizer_FromProse_PreservesExplicitParagraphBoundaries(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	prose := "Mike Tyson resta al centro del primo blocco.\n\nMuhammad Ali guida il secondo blocco.\n\nSugar Ray Robinson chiude il racconto."

	got := s.FromProse(prose, 3)
	if len(got) != 3 {
		t.Fatalf("FromProse returned %d scenes, want 3", len(got))
	}
	want := []string{
		"Mike Tyson resta al centro del primo blocco.",
		"Muhammad Ali guida il secondo blocco.",
		"Sugar Ray Robinson chiude il racconto.",
	}
	for i := range want {
		if got[i].Text != want[i] {
			t.Errorf("scene[%d] = %q, want %q", i, got[i].Text, want[i])
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

// ── FromText tests (LLM-PLAIN-TEXT-CONTRACT wave PR-6) ─────────────

// TestSceneSynthesizer_FromText_PartitionsProse verifies the
// basic contract: FromText delegates to FromProse when no evidence
// is provided, producing N scenes with the same distribution as
// FromProse.
func TestSceneSynthesizer_FromText_PartitionsProse(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "First. Second. Third. Fourth. Fifth. Sixth."

	got := s.FromText(prose, 3, nil)
	if len(got) != 3 {
		t.Fatalf("FromText returned %d scenes, want 3", len(got))
	}
	for i, sc := range got {
		if sc.Text == "" {
			t.Errorf("scene[%d].Text is empty", i)
		}
		if sc.ID == "" {
			t.Errorf("scene[%d].ID is empty", i)
		}
	}
}

// TestSceneSynthesizer_FromText_BindsClipsOneToOne verifies the
// canonical 1:1 binding contract: when evidence is provided with
// N AcceptedClipIDs, the first min(N, len(scenes)) scenes get
// their Bindings.Clip populated in order.
func TestSceneSynthesizer_FromText_BindsClipsOneToOne(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "Scene one here. Scene two here. Scene three here."

	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"},
		ClipNames: map[string]string{
			"clip-a": "First Clip",
			"clip-b": "Second Clip",
			"clip-c": "Third Clip",
		},
		DriveLinks: map[string]string{
			"clip-a": "https://drive.google.com/file/d/a",
			"clip-b": "https://drive.google.com/file/d/b",
			"clip-c": "https://drive.google.com/file/d/c",
		},
	}

	got := s.FromText(prose, 3, evidence)
	if len(got) != 3 {
		t.Fatalf("FromText returned %d scenes, want 3", len(got))
	}

	// Scene 0 → clip-a.
	if got[0].Bindings.Clip == nil {
		t.Fatal("scene[0].Bindings.Clip is nil, want binding to clip-a")
	}
	if got[0].Bindings.Clip.ClipID != "clip-a" {
		t.Errorf("scene[0].Bindings.Clip.ClipID = %q, want %q", got[0].Bindings.Clip.ClipID, "clip-a")
	}
	if got[0].Bindings.Clip.ClipTitle != "First Clip" {
		t.Errorf("scene[0].Bindings.Clip.ClipTitle = %q, want %q", got[0].Bindings.Clip.ClipTitle, "First Clip")
	}
	if got[0].Bindings.Clip.DriveLink != "https://drive.google.com/file/d/a" {
		t.Errorf("scene[0].Bindings.Clip.DriveLink = %q, want drive link for clip-a", got[0].Bindings.Clip.DriveLink)
	}

	// Scene 1 → clip-b.
	if got[1].Bindings.Clip == nil {
		t.Fatal("scene[1].Bindings.Clip is nil, want binding to clip-b")
	}
	if got[1].Bindings.Clip.ClipID != "clip-b" {
		t.Errorf("scene[1].Bindings.Clip.ClipID = %q, want %q", got[1].Bindings.Clip.ClipID, "clip-b")
	}

	// Scene 2 → clip-c.
	if got[2].Bindings.Clip == nil {
		t.Fatal("scene[2].Bindings.Clip is nil, want binding to clip-c")
	}
	if got[2].Bindings.Clip.ClipID != "clip-c" {
		t.Errorf("scene[2].Bindings.Clip.ClipID = %q, want %q", got[2].Bindings.Clip.ClipID, "clip-c")
	}
}

// TestSceneSynthesizer_FromText_ParagraphsScenesAndBindingsOneToOne pins
// the complete clip-native contract in one assertion surface:
// exactly N non-empty input paragraphs must become exactly N scenes,
// preserve paragraph order/text, and receive exactly N ordered clip
// bindings with no drops or modulo reuse.
func TestSceneSynthesizer_FromText_ParagraphsScenesAndBindingsOneToOne(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()

	const prose = "PARAGRAPH ONE: Paul opens the round table.\n\n" +
		"PARAGRAPH TWO: Andrew answers with a concrete example.\n\n" +
		"PARAGRAPH THREE: Jeffrey describes the creative process.\n\n" +
		"PARAGRAPH FOUR: Demi closes the conversation."
	paragraphs := []string{
		"PARAGRAPH ONE: Paul opens the round table.",
		"PARAGRAPH TWO: Andrew answers with a concrete example.",
		"PARAGRAPH THREE: Jeffrey describes the creative process.",
		"PARAGRAPH FOUR: Demi closes the conversation.",
	}
	clipIDs := []string{"clip-paul", "clip-andrew", "clip-jeffrey", "clip-demi"}
	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: clipIDs,
		ClipNames: map[string]string{
			"clip-paul":    "Paul",
			"clip-andrew":  "Andrew",
			"clip-jeffrey": "Jeffrey",
			"clip-demi":    "Demi",
		},
		DriveLinks: map[string]string{
			"clip-paul":    "https://drive.test/paul",
			"clip-andrew":  "https://drive.test/andrew",
			"clip-jeffrey": "https://drive.test/jeffrey",
			"clip-demi":    "https://drive.test/demi",
		},
	}

	got := s.FromText(prose, len(paragraphs), evidence)
	if len(got) != len(paragraphs) {
		t.Fatalf("FromText returned %d scenes, want exactly %d", len(got), len(paragraphs))
	}
	if len(evidence.AcceptedClipIDs) != len(paragraphs) {
		t.Fatalf("fixture must contain exactly N clip IDs: got %d, want %d", len(evidence.AcceptedClipIDs), len(paragraphs))
	}

	seen := make(map[string]bool, len(got))
	for i, wantParagraph := range paragraphs {
		sc := got[i]
		if strings.TrimSpace(sc.Text) == "" {
			t.Fatalf("scene[%d].Text is empty; every paragraph must produce non-empty scene text", i)
		}
		if sc.Text != wantParagraph {
			t.Errorf("scene[%d].Text = %q, want paragraph[%d] %q", i, sc.Text, i, wantParagraph)
		}
		if sc.Index != i {
			t.Errorf("scene[%d].Index = %d, want %d", i, sc.Index, i)
		}
		if sc.Bindings.Clip == nil {
			t.Fatalf("scene[%d].Bindings.Clip is nil, want binding to %q", i, clipIDs[i])
		}
		binding := sc.Bindings.Clip
		if binding.ClipID != clipIDs[i] {
			t.Errorf("scene[%d].Bindings.Clip.ClipID = %q, want %q", i, binding.ClipID, clipIDs[i])
		}
		if binding.DriveLink != evidence.DriveLinks[clipIDs[i]] {
			t.Errorf("scene[%d].Bindings.Clip.DriveLink = %q, want %q", i, binding.DriveLink, evidence.DriveLinks[clipIDs[i]])
		}
		if seen[binding.ClipID] {
			t.Errorf("clip %q is bound more than once; one-to-one binding violated", binding.ClipID)
		}
		seen[binding.ClipID] = true
	}
	if len(seen) != len(clipIDs) {
		t.Fatalf("got %d unique clip bindings, want %d", len(seen), len(clipIDs))
	}
}

// TestSceneSynthesizer_FromText_NilEvidence_ReturnsScenesUnbound
// verifies that nil evidence returns scenes without any clip
// bindings (the caller preserves the no-op invariant).
func TestSceneSynthesizer_FromText_NilEvidence_ReturnsScenesUnbound(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "Prose without any clip evidence."

	got := s.FromText(prose, 2, nil)
	if len(got) != 2 {
		t.Fatalf("FromText returned %d scenes, want 2", len(got))
	}
	for i, sc := range got {
		if sc.Bindings.Clip != nil {
			t.Errorf("scene[%d].Bindings.Clip = %+v, want nil (nil evidence → no binding)",
				i, sc.Bindings.Clip)
		}
	}
}

// TestSceneSynthesizer_FromText_EmptyAcceptedClipIDs_ReturnsScenesUnbound
// verifies that evidence with empty AcceptedClipIDs returns scenes
// without any clip bindings.
func TestSceneSynthesizer_FromText_EmptyAcceptedClipIDs_ReturnsScenesUnbound(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "Prose with empty evidence."

	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{},
		ClipNames: map[string]string{
			"orphan": "Orphaned Clip",
		},
	}

	got := s.FromText(prose, 2, evidence)
	if len(got) != 2 {
		t.Fatalf("FromText returned %d scenes, want 2", len(got))
	}
	for i, sc := range got {
		if sc.Bindings.Clip != nil {
			t.Errorf("scene[%d].Bindings.Clip = %+v, want nil (empty AcceptedClipIDs → no binding)",
				i, sc.Bindings.Clip)
		}
	}
}

// TestSceneSynthesizer_FromText_NoModuloCycling verifies the P0 #2
// contract: when there are more scenes than clip IDs, scenes beyond
// len(AcceptedClipIDs) get nil Bindings.Clip — NO modulo cycling.
//
// godlike/07 NO-FAKE-AVAILABILITY: modulo cycling would silently
// re-bind scene[1] to clip-a, masking the evidence gap. The contract
// requires nil for unbound scenes so the binder downstream emits
// its own sentinel.
func TestSceneSynthesizer_FromText_NoModuloCycling(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "First scene text. Second scene text. Third scene text."

	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-a"},
		ClipNames:       map[string]string{"clip-a": "Solo Clip"},
		DriveLinks:      map[string]string{"clip-a": "https://drive.google.com/file/d/solo"},
	}

	got := s.FromText(prose, 3, evidence)
	if len(got) != 3 {
		t.Fatalf("FromText returned %d scenes, want 3", len(got))
	}

	// Scene 0: bound to clip-a.
	if got[0].Bindings.Clip == nil {
		t.Fatal("scene[0].Bindings.Clip is nil, want binding to clip-a")
	}
	if got[0].Bindings.Clip.ClipID != "clip-a" {
		t.Errorf("scene[0].Bindings.Clip.ClipID = %q, want %q", got[0].Bindings.Clip.ClipID, "clip-a")
	}

	// Scene 1 + Scene 2: MUST be nil (NO modulo cycling).
	for i := 1; i <= 2; i++ {
		if got[i].Bindings.Clip != nil {
			t.Errorf("scene[%d].Bindings.Clip = %+v, want nil (P0 #2 no-modulo-cycling contract: only 1 clip ID available)",
				i, got[i].Bindings.Clip)
		}
	}
}

// TestSceneSynthesizer_FromText_EmptyClipID_Skipped verifies that
// an empty-string clip ID in AcceptedClipIDs is skipped (the scene
// at that index gets nil binding).
func TestSceneSynthesizer_FromText_EmptyClipID_Skipped(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "First scene. Second scene."

	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"", "clip-b"},
		ClipNames:       map[string]string{"clip-b": "Second Clip"},
	}

	got := s.FromText(prose, 2, evidence)
	if len(got) != 2 {
		t.Fatalf("FromText returned %d scenes, want 2", len(got))
	}

	// Scene 0: empty clip ID → nil binding.
	if got[0].Bindings.Clip != nil {
		t.Errorf("scene[0].Bindings.Clip = %+v, want nil (empty clip ID → skipped)",
			got[0].Bindings.Clip)
	}

	// Scene 1: bound to clip-b.
	if got[1].Bindings.Clip == nil {
		t.Fatal("scene[1].Bindings.Clip is nil, want binding to clip-b")
	}
	if got[1].Bindings.Clip.ClipID != "clip-b" {
		t.Errorf("scene[1].Bindings.Clip.ClipID = %q, want %q", got[1].Bindings.Clip.ClipID, "clip-b")
	}
}

// TestSceneSynthesizer_FromText_NumClipsZero_ReturnsNil verifies
// the no-op contract: numClips <= 0 returns nil.
func TestSceneSynthesizer_FromText_NumClipsZero_ReturnsNil(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()
	const prose = "Some prose."

	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-a"},
	}

	if got := s.FromText(prose, 0, evidence); got != nil {
		t.Errorf("FromText(_, 0, _) = %v, want nil", got)
	}
	if got := s.FromText(prose, -1, evidence); got != nil {
		t.Errorf("FromText(_, -1, _) = %v, want nil", got)
	}
}

// TestSceneSynthesizer_FromText_EmptyText_ReturnsNil verifies the
// contract: empty text returns nil (FromProse returns nil).
func TestSceneSynthesizer_FromText_EmptyText_ReturnsNil(t *testing.T) {
	t.Parallel()
	s := scene.NewSceneSynthesizer()

	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-a"},
	}

	if got := s.FromText("", 3, evidence); got != nil {
		t.Errorf("FromText(\"\", 3, _) = %v, want nil", got)
	}
}
