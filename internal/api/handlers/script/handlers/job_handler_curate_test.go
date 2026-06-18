package handlers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
)

// ─── Helpers ────────────────────────────────────────────────────────────────

func sampleCurateScenes() []scriptcore.ClipScene {
	return []scriptcore.ClipScene{
		{
			SceneIndex: 1,
			Text: "The annals of cinematic history are littered with legends. " +
				"Among these titans, one name shines with a particular kind of enduring brilliance: Jackie Chan. " +
				"He didn't just perform stunts; he turned the mundane into high art.",
		},
		{
			SceneIndex: 2,
			ClipID:     "1Ll1MvzRH0SBJgf6rh-OLa2Y3gma3RdK1",
			DriveLink:  "https://drive.google.com/file/d/clip-1",
			Text: "Brand new underwear — Jackie Chan explains how a stunt went hilariously wrong on set. " +
				"The crew couldn't keep a straight face as he improvised through the wardrobe malfunction.",
		},
		{
			SceneIndex: 3,
			ClipID:     "16iQatEJMPG7OHhV7QrjFqa3JLhxDCf1z",
			DriveLink:  "https://drive.google.com/file/d/clip-2",
			Text: "The director makes me old, Jackie jokes. The meta-commentary lands and the room laughs. " +
				"It is both humbling and hysterically relatable.",
		},
	}
}

// ─── Per-scene description template (drop emoji, add stats + preview) ──────

func TestBuildCurateDocContent_DropsViewClipEmojiDecoration(t *testing.T) {
	out := buildCurateDocContent("Jackie Chan Funniest Moments Compilation", sampleCurateScenes())

	// 1) Old "View clip" emoji + label is gone from the rendered doc.
	assert.NotContains(t, out, "🔗 View clip",
		"the 'View clip' emoji label must be removed (per June 2026 template)")
	assert.NotContains(t, out, "View clip",
		"no variant of the old label should survive")

	// 2) The drive link is still present, just without emoji decoration.
	assert.Contains(t, out, "https://drive.google.com/file/d/clip-1",
		"real drive link must still be in the doc")
	assert.Contains(t, out, "https://drive.google.com/file/d/clip-2")
	assert.Contains(t, out, ">Drive link</a>",
		"drive link should be rendered as plain 'Drive link' anchor text (no emoji)")

	// 3) Sanity: the doc wraps the drive link in an anchor tag.
	assert.Contains(t, out, `<a href="https://drive.google.com/file/d/clip-1">Drive link</a>`)
}

func TestBuildCurateDocContent_AddsWordCountAndReadTimePerScene(t *testing.T) {
	out := buildCurateDocContent("Test Compilation", sampleCurateScenes())

	// Each of the 3 scenes should carry a "~N words · ~Ns read" line.
	count := strings.Count(out, "words · ~")
	assert.GreaterOrEqual(t, count, 3,
		"each scene must carry a per-scene word count + read-time line; got %d", count)

	// Spot-check: scene 2 has a known narration body → expect ~50 words + ~20s read.
	// (Exact count depends on tokenization; just verify the line shape is present.)
	assert.Regexp(t, `~\d+ words · ~\d+s read`, out)
}

func TestBuildCurateDocContent_AddsFirstSentencePreview(t *testing.T) {
	out := buildCurateDocContent("Test", sampleCurateScenes())

	// Each scene should render its preview in a styled block.
	previewCount := strings.Count(out, `class="scene-preview"`)
	assert.GreaterOrEqual(t, previewCount, 3,
		"each scene should have a 'scene-preview' block describing what happens")

	// The preview for scene 2 should start with the first sentence from the narration.
	// Scene 2 text begins: "Brand new underwear — Jackie Chan explains how a stunt went..."
	assert.Contains(t, out, "Brand new underwear",
		"preview should preserve the leading narrative words so the reader knows what comes next")
}

func TestBuildCurateDocContent_KeepsSceneLabelsAndClipIDMarkers(t *testing.T) {
	out := buildCurateDocContent("Test", sampleCurateScenes())

	// 🎬 Scene header is still emitted so the structure stays scannable.
	assert.Contains(t, out, "🎬 Scene 2")
	assert.Contains(t, out, "🎬 Scene 3")
	assert.Contains(t, out, "1Ll1MvzRH0SBJgf6rh-OLa2Y3gma3RdK1")
	assert.Contains(t, out, "16iQatEJMPG7OHhV7QrjFqa3JLhxDCf1z")

	// Narration-only scene gets the 📝 Scene marker (instead of 🎬).
	assert.Contains(t, out, "📝 Scene 1")
}

func TestBuildCurateDocContent_PreviewStripsClipAndNarrationMarkers(t *testing.T) {
	// Regression guard: a narration body that survives the LLM pass with the
	// [Clip: <id>] or [Narration: <role>] marker on the first line MUST be
	// stripped from the per-scene preview so the reader sees prose, not handles.
	scenes := []scriptcore.ClipScene{
		{
			SceneIndex: 1,
			ClipID:     "abc123",
			Text:       "[Clip: abc123] The footage shows a candid interview segment. The next beat is the punchline.",
		},
		{
			SceneIndex: 2,
			Text:       "[Narration: opening] Welcome to the compilation. Today we look back at iconic moments.",
		},
	}
	out := buildCurateDocContent("Test", scenes)

	// Scene-preview blocks must NOT carry the marker handles.
	previewBlocks := extractPreviewBlocks(out)
	assert.GreaterOrEqual(t, len(previewBlocks), 2)
	for i, blk := range previewBlocks {
		assert.NotContains(t, blk, "[Clip:",
			"scene preview #%d should not leak [Clip:] markers: %q", i, blk)
		assert.NotContains(t, blk, "[Narration:",
			"scene preview #%d should not leak [Narration:] markers: %q", i, blk)
	}
	// Sanity: actual narrative words DO survive.
	assert.Contains(t, out, "footage shows a candid interview segment",
		"first-sentence preview should preserve narrative content")
}

// extractPreviewBlocks pulls the inner text of every <p class="scene-preview">...</p>
// block in the rendered doc. Used for marker-leak regression assertions.
func extractPreviewBlocks(html string) []string {
	var out []string
	const openTag = `<p class="scene-preview">`
	const closeTag = `</p>`
	i := 0
	for {
		j := strings.Index(html[i:], openTag)
		if j < 0 {
			break
		}
		start := i + j + len(openTag)
		k := strings.Index(html[start:], closeTag)
		if k < 0 {
			break
		}
		out = append(out, html[start:start+k])
		i = start + k + len(closeTag)
	}
	return out
}

func TestBuildCurateDocContent_EmptyScenesList_StillRendersValidDoc(t *testing.T) {
	out := buildCurateDocContent("Empty Test", nil)

	// Even with zero scenes the doc must be a valid HTML skeleton: title
	// as <h1>, body present, no scene label leaked.
	assert.Contains(t, out, "<html>", "doc must wrap in <html>")
	assert.Contains(t, out, "<body>", "doc must include <body>")
	assert.Contains(t, out, "<h1>Empty Test</h1>", "title must render as <h1>")
	assert.NotContains(t, out, "🎬 Scene",
		"no 🎬 Scene label should be rendered for an empty scenes list")
	assert.NotContains(t, out, "📝 Scene",
		"no 📝 Scene label should be rendered for an empty scenes list")
	assert.NotContains(t, out, "Drive link",
		"no Drive link line should be rendered for an empty scenes list")
}

func TestBuildCurateDocContent_ClipSceneWithEmptyText_StillRendersLabelAndDriveLink(t *testing.T) {
	scenes := []scriptcore.ClipScene{
		{
			SceneIndex: 1,
			ClipID:     "silent_clip",
			DriveLink:  "https://drive.google.com/file/d/silent",
			Text:       "",
		},
	}
	out := buildCurateDocContent("Test", scenes)

	// The label + drive link must still emit even when Text is empty, because
	// the user might legit record a clip scene with just a visual beat.
	assert.Contains(t, out, "🎬 Scene 1", "label must render even when Text is empty")
	assert.Contains(t, out, "silent_clip",
		"clip id must render even when Text is empty")
	assert.Contains(t, out, ">Drive link</a>",
		"drive link must render even when Text is empty")
	// Preview block is omitted when there's no prose to preview.
	assert.NotContains(t, out, `class="scene-preview"`,
		"scene-preview block must be omitted for empty Text")
	// Word count line shows 0 — verify the line is still emitted (consistent shape)
	// so future readers / parsers always see the same line structure.
	assert.Contains(t, out, "~0 words",
		"word count meta line should still emit (with 0) for empty Text")
}

func TestBuildCurateDocContent_NoDriveLinkForIntroScene(t *testing.T) {
	out := buildCurateDocContent("Test", sampleCurateScenes())

	// The intro scene (SceneIndex=1, ClipID="") must NOT carry a drive link line.
	// Locate the intro section by splitting at the first "📝 Scene" block.
	introStart := strings.Index(out, "📝 Scene 1")
	assert.GreaterOrEqual(t, introStart, 0)
	nextSceneStart := strings.Index(out[introStart+1:], "🎬 Scene 2")
	assert.GreaterOrEqual(t, nextSceneStart, 0)

	introBlock := out[introStart : introStart+nextSceneStart+1]
	assert.NotContains(t, introBlock, "Drive link",
		"intro scene (no clip_id) must not emit a Drive link line")
	assert.NotContains(t, introBlock, `class="drive-link"`)
}

func TestBuildCurateDocContent_LastNarrationSceneIsLabeledOutro(t *testing.T) {
	scenes := []scriptcore.ClipScene{
		{SceneIndex: 1, Text: "Hook line. This is the opening narration."},
		{SceneIndex: 2, ClipID: "c1", DriveLink: "https://drive.google.com/file/d/c1",
			Text: "First clip narration."},
		{SceneIndex: 3, Text: "Closing reflection. This is the outro narration."},
	}
	out := buildCurateDocContent("Test", scenes)

	// Locate the section after the LAST clip-scene header so we are not
	// accidentally validating the intro block (which also starts with 📝).
	lastClipSceneIdx := strings.LastIndex(out, "🎬 Scene")
	assert.GreaterOrEqual(t, lastClipSceneIdx, 0, "doc should render at least one 🎬 Scene")
	outroBlock := out[lastClipSceneIdx:]
	assert.Contains(t, outroBlock, "Outro",
		"last narration-only scene should be labeled 'Outro'")
}

// ─── Helpers: pure logic ───────────────────────────────────────────────────

func TestCountWords(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"   \t\n", 0},
		{"hello", 1},
		{"hello world", 2},
		{"  multiple   spaces   between  words  ", 4},
		{"line\nbreak\nwords", 3},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, countWords(tc.in),
			"countWords(%q) = %d, want %d", tc.in, countWords(tc.in), tc.want)
	}
}

func TestApproxReadingSeconds(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, 0},
		{1, 1}, // 1 word → 1s floor
		{50, 20},
		{150, 60},
		{300, 120},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, approxReadingSeconds(tc.in),
			"approxReadingSeconds(%d) = %d, want %d",
			tc.in, approxReadingSeconds(tc.in), tc.want)
	}
}

func TestFirstSentencePreview(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantHas string // substring that must appear
		wantNot string // substring that must NOT appear
	}{
		{
			name:    "strips narration marker",
			in:      "[Narration: opening] Hello and welcome. This is the prologue.",
			wantHas: "Hello and welcome",
			wantNot: "[Narration:",
		},
		{
			name:    "captures first sentence only",
			in:      "First sentence here. Second sentence that should be excluded.",
			wantHas: "First sentence here",
		},
		{
			name:    "ellipsizes long text",
			in:      strings.Repeat("verylongwithoutspaces ", 30),
			wantHas: "...",
		},
		{
			name: "empty input → empty output",
			in:   "",
		},
		{
			name: "whitespace-only input → empty output",
			in:   "   \n\t",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := firstSentencePreview(tc.in, 140)
			if tc.wantHas != "" {
				assert.Contains(t, got, tc.wantHas)
			}
			if tc.wantNot != "" {
				assert.NotContains(t, got, tc.wantNot)
			}
			if strings.TrimSpace(tc.in) == "" {
				assert.Equal(t, "", got)
			}
		})
	}
}
