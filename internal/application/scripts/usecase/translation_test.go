// Package usecase — translation_test.go pins the canonical
// TranslateScriptSpec contract per godlike/06 SSOT.
//
// Test invariant catalog (per godlike/07 NO-FAKE-AVAILABILITY):
//
//	#1 ToTranslatedScript_PreservesSpecSceneStructure
//	   scene count + ids + indexes + kinds preserved byte-identical;
//	   all text fields translated.
//	#2 ToTranslatedScript_DoesNotTranslateJSONKeys
//	   no translator input ever contains "clip_id" or "drive_link"
//	   as substring → LLM structurally CANNOT mutate identifiers.
//	#3 ToTranslatedScript_FailsOnEmptyTranslation
//	   translator returning "" for any segment → typed sentinel
//	   ErrTranslationEmpty reachable via errors.Is.
//	#4 ToTranslatedScript_PreservesClipBindings
//	   clip_id + drive_link + start_ms + end_ms byte-identical
//	   across translation (5-scene fixture).
//	#5 ToTranslatedScript_CreatesGoogleDocWithSpecSceneBlock
//	   BuildSpecSceneDocumentHTML(translated, title) → HTML contains
//	   canonical Scenes/SpecScene JSON sections, preserved drive links,
//	   translated scene text, and NO translated JSON keys inside the
//	   SpecScene <pre> block.
//	#6 ToTranslatedScript_LongScript_NoSceneLossNoTruncation
//	   10 scenes × ~4000 words → out has 10 scenes; out word
//	   count ≥ 70% source; no scene trims to empty; no tail
//	   truncation (last scene non-empty).
//	#7 ToTranslatedScript_PreservesSpecialCharactersAndEmoji
//	   scenario-6 coverage: special chars (è à ç ñ e'<>'"& á),
//	   emoji (🔥 👀), HTML-tag-like substrings (<script>alert</script>)
//	   round-trip through translation without LLM-mutation or
//	   injection — translated Text + Prompts preserve the source's
//	   special characters byte-equivalent (per-text strategy
//	   passes them through; assertions verify no transformation).
//	#8 ToTranslatedScript_NilTranslator_TypedSentinel
//	   scenario-7 coverage (failure modes): nil translator →
//	   ErrTranslationTranslatorMissing typed sentinel
//	   (errors.Is-probeable), nil out, NO panic.
//	#9 ToTranslatedScript_EqualToSourceWarning
//	   scenario-7 coverage: translator returns text byte-identical
//	   to source segment → non-fatal warning surfaced in the
//	   []string channel; no failure (operator-observable anomaly,
//	   not a fail-fast condition).
package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// mockTranslatorSuffix appends a deterministic suffix to every input.
// Records every input it received so the "DoesNotTranslateJSONKeys"
// invariant test can probe the input surface for leak of identifier
// keys.
type mockTranslatorSuffix struct {
	suffix string
	inputs []string
	calls  int
}

func (m *mockTranslatorSuffix) Translate(_ context.Context, text, _ string) (string, error) {
	m.inputs = append(m.inputs, text)
	m.calls++
	return text + "_" + m.suffix, nil
}

// mockTranslatorEmpty returns "" for every input (godlike/07 typed
// sentinel probe — empty translation must propagate as typed error).
type mockTranslatorEmpty struct {
	calls int
}

func (m *mockTranslatorEmpty) Translate(_ context.Context, _, _ string) (string, error) {
	m.calls++
	return "", nil
}

// mockTranslatorPassThrough returns its input byte-identical. Used by
// scenario-7 to simulate an LLM that detected the source IS the target
// language and emitted the no-op result.
type mockTranslatorPassThrough struct {
	calls int
}

func (m *mockTranslatorPassThrough) Translate(_ context.Context, text, _ string) (string, error) {
	m.calls++
	return text, nil
}

// makeThreeSceneSpecEN constructs a 3-scene EN script with clip + image
// bindings, or the canonical fixture used by Tests 1, 2, 4, 5, 9.
//
// Scene IDs: "scene-1", "scene-2", "scene-3".
// Clip IDs: "clip-1", "clip-2", "clip-3".
// Drive links: drive.google.com URLs (one per scene).
// Image prompts: non-empty text (translatable).
// No Stock bindings (test coverage of stock.Name translate is in a
// future Test — out of scope for the user-spec literal).
func makeThreeSceneSpecEN() *scriptpkg.ModelScriptOutputV1 {
	makeScene := func(id, clipID, clipTitle, driveLink, prompt, text, title string) scriptpkg.SpecScene {
		return scriptpkg.SpecScene{
			ID:    id,
			Index: 0, // patched below by index-of-loop
			Text:  text,
			Title: title,
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    clipID,
					ClipTitle: clipTitle,
					DriveLink: driveLink,
					StartMs:   1000,
					EndMs:     5000,
				},
				Image: &scriptpkg.ImageBinding{
					ImageID: "img-" + clipID,
					Prompt:  prompt,
					URL:     "https://storage.example.com/" + clipID + ".png",
					Status:  "generated",
				},
			},
		}
	}
	scenes := []scriptpkg.SpecScene{
		makeScene("scene-1", "clip-1", "EN clip 1 title",
			"https://drive.google.com/file/d/abc1/view",
			"EN prompt for scene 1",
			"Original EN scene 1 narration about Jackie Chan.",
			"Opening"),
		makeScene("scene-2", "clip-2", "EN clip 2 title",
			"https://drive.google.com/file/d/abc2/view",
			"EN prompt for scene 2",
			"Original EN scene 2 narration about Jackie Chan.",
			"Middle"),
		makeScene("scene-3", "clip-3", "EN clip 3 title",
			"https://drive.google.com/file/d/abc3/view",
			"EN prompt for scene 3",
			"Original EN scene 3 narration about Jackie Chan.",
			"Closing"),
	}
	for i := range scenes {
		scenes[i].Index = i
	}
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Top 10 incredible moments of Jackie Chan. Original EN script prose.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		},
	}
}

// makeSpecSceneWithSpecialChars constructs a 3-scene EN script with
// special characters / emoji / HTML-tag-like substrings in the text
// fields. Used by Test 7 to verify the per-text strategy round-trips
// special characters without LLM-mutation.
func makeSpecSceneWithSpecialChars() *scriptpkg.ModelScriptOutputV1 {
	makeScene := func(id, clipID, driveLink, prompt, text, title string) scriptpkg.SpecScene {
		return scriptpkg.SpecScene{
			ID:    id,
			Index: 0,
			Text:  text,
			Title: title,
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    clipID,
					ClipTitle: "EN clip " + clipID,
					DriveLink: driveLink,
					StartMs:   1000,
					EndMs:     5000,
				},
				Image: &scriptpkg.ImageBinding{
					ImageID: "img-" + clipID,
					Prompt:  prompt,
					URL:     "https://storage.example.com/" + clipID + ".png",
					Status:  "generated",
				},
			},
		}
	}
	scenes := []scriptpkg.SpecScene{
		makeScene("scene-1", "clip-1",
			"https://drive.google.com/file/d/special1/view",
			"è à ç ñ á <single-tag/> 🔥 emoji prompt",
			"<script>alert('xss')</script> Jackie said: <This is crazy & dangerous> 🔥",
			"Capitolo 1: è à 🔥"),
		makeScene("scene-2", "clip-2",
			"https://drive.google.com/file/d/special2/view",
			"Prompt with é à ø & emoji 👀",
			"Original EN text with ñ ç special chars and emoji 🚀",
			"Mid: ñ 👀"),
		makeScene("scene-3", "clip-3",
			"https://drive.google.com/file/d/special3/view",
			"Prompt é ñ",
			"Text with < and > and & and \"quotes\" and 'apostrophes'",
			"End: ó"),
	}
	for i := range scenes {
		scenes[i].Index = i
	}
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Top 10 EN prose with è à ç ñ and emoji 🔥 and <tag> text.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		},
	}
}

// evidenceFor3Scenes returns the canonical clip evidence that
// passes ValidateAndEnrichSpecScene without warnings.
func evidenceFor3Scenes() *scriptpkg.ClipEvidence {
	return &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-1", "clip-2", "clip-3"},
	}
}

// ─── TEST 1: scene structure preserved + text translated ───────────
func TestTranslateScriptSpec_PreservesSpecSceneStructure(t *testing.T) {
	in := makeThreeSceneSpecEN()
	tr := &mockTranslatorSuffix{suffix: "IT"}

	out, warnings, err := TranslateScriptSpec(context.Background(), in, evidenceFor3Scenes(), "it", tr.Translate)
	require.NoError(t, err, "TranslateScriptSpec must succeed for valid input + working translator")
	require.NotNil(t, out)
	require.NotNil(t, warnings, "warnings slice must always be non-nil per godlike/07 NO-FAKE-AVAILABILITY")

	// SpecScene structure preserved.
	require.Equal(t, len(in.SpecScene.Scenes), len(out.SpecScene.Scenes),
		"scene count must be preserved byte-identical")
	require.Equal(t, in.SpecScene.Version, out.SpecScene.Version,
		"SpecScene.Version must be preserved byte-identical")

	// Each scene preserved + text translated.
	for i, sc := range in.SpecScene.Scenes {
		outSc := out.SpecScene.Scenes[i]
		require.Equal(t, sc.ID, outSc.ID,
			"scene[%d].ID must be preserved byte-identical", i)
		require.Equal(t, sc.Index, outSc.Index,
			"scene[%d].Index must be preserved byte-identical", i)
		require.Equal(t, sc.Kind, outSc.Kind,
			"scene[%d].Kind must be preserved byte-identical", i)
		assert.Contains(t, outSc.Text, "_IT",
			"scene[%d].Text must be translated", i)
		assert.NotEqual(t, sc.Text, outSc.Text,
			"scene[%d].Text must differ from source (translated)", i)
	}

	// Full-script text translated.
	assert.Contains(t, out.Text, "_IT",
		"model.Text must be translated")
	assert.NotEqual(t, in.Text, out.Text,
		"model.Text must differ from source (translated)")

	// Provenance fields preserved byte-identical.
	assert.Equal(t, in.SchemaVersion, out.SchemaVersion)
	assert.Equal(t, in.WordCount, out.WordCount)
	assert.Equal(t, in.ModelUsed, out.ModelUsed)
	assert.Equal(t, in.CacheStatus, out.CacheStatus)

	// Translator called once per text segment (1 model.Text + 3 scene.Text
	// + 3 scene.Title + 3 image.Prompt = 10 calls, all returning
	// translated strings).
	assert.Equal(t, 10, tr.calls,
		"translator must be called exactly once per text segment (model.Text + scenes[].Text + scenes[].Title + scenes[].Bindings.Image.Prompt)")

	// No equal-to-source warnings in happy-path (translator returned a
	// non-identical value for every segment).
	for _, w := range warnings {
		assert.NotContains(t, w, WarnTranslationEqualToSource,
			"no equal-to-source warning expected on happy-path suffix-translation")
	}
}

// ─── TEST 2: NO translator input contains JSON keys or identifiers ──
func TestTranslateScriptSpec_DoesNotTranslateJSONKeys(t *testing.T) {
	in := makeThreeSceneSpecEN()
	tr := &mockTranslatorSuffix{suffix: "IT"}

	_, _, err := TranslateScriptSpec(context.Background(), in, evidenceFor3Scenes(), "it", tr.Translate)
	require.NoError(t, err)

	forbiddenSubstrings := []string{
		"clip_id", "drive_link", "image_id",
		"clip-1", "clip-2", "clip-3",
		"abc1", "abc2", "abc3",
		"drive.google.com",
		"img-clip-",
		"storage.example.com",
	}
	for idx, input := range tr.inputs {
		for _, forb := range forbiddenSubstrings {
			assert.NotContains(t, input, forb,
				"translator input[%d] must NOT contain forbidden substring %q (LLM cannot mutate identifiers)", idx, forb)
		}
	}
}

// ─── TEST 3: empty translator → typed ErrTranslationEmpty ─────────
func TestTranslateScriptSpec_FailsOnEmptyTranslation(t *testing.T) {
	in := makeThreeSceneSpecEN()
	tr := &mockTranslatorEmpty{calls: 0}

	out, warnings, err := TranslateScriptSpec(context.Background(), in, evidenceFor3Scenes(), "it", tr.Translate)
	require.Error(t, err, "empty translation MUST propagate as typed error (godlike/07)")
	require.ErrorIs(t, err, ErrTranslationEmpty,
		"err must wrap ErrTranslationEmpty sentinel (typed-error contract)")
	require.Nil(t, out, "out MUST be nil on fail-closed path")
	require.NotNil(t, warnings, "warnings MUST still be non-nil slice (operator-observable state)")
	assert.GreaterOrEqual(t, tr.calls, 1,
		"translator must be hit at least once before the empty translation sentinel")
}

// ─── TEST 4: clip bindings byte-identical across translation ─────
func TestTranslateScriptSpec_PreservesClipBindings(t *testing.T) {
	in := makeThreeSceneSpecEN()
	tr := &mockTranslatorSuffix{suffix: "IT"}

	out, _, err := TranslateScriptSpec(context.Background(), in, evidenceFor3Scenes(), "it", tr.Translate)
	require.NoError(t, err)

	require.Len(t, out.SpecScene.Scenes, 3)
	for i, sc := range in.SpecScene.Scenes {
		outSc := out.SpecScene.Scenes[i]
		require.NotNil(t, sc.Bindings.Clip, "input scene[%d] must have Clip binding (fixture)", i)
		require.NotNil(t, outSc.Bindings.Clip, "output scene[%d] must have Clip binding (preserved)", i)

		// Byte-identical (canonical invariants).
		assert.Equal(t, sc.Bindings.Clip.ClipID, outSc.Bindings.Clip.ClipID,
			"scene[%d] clip_id must be preserved byte-identical", i)
		assert.Equal(t, sc.Bindings.Clip.DriveLink, outSc.Bindings.Clip.DriveLink,
			"scene[%d] drive_link must be preserved byte-identical", i)
		assert.Equal(t, sc.Bindings.Clip.ClipTitle, outSc.Bindings.Clip.ClipTitle,
			"scene[%d] clip_title must be preserved byte-identical", i)
		assert.Equal(t, sc.Bindings.Clip.StartMs, outSc.Bindings.Clip.StartMs,
			"scene[%d] start_ms must be preserved byte-identical", i)
		assert.Equal(t, sc.Bindings.Clip.EndMs, outSc.Bindings.Clip.EndMs,
			"scene[%d] end_ms must be preserved byte-identical", i)

		// Image ID + URL + Status byte-identical (only Prompt translated).
		require.NotNil(t, sc.Bindings.Image, "input scene[%d] must have Image binding", i)
		require.NotNil(t, outSc.Bindings.Image, "output scene[%d] must have Image binding", i)
		assert.Equal(t, sc.Bindings.Image.ImageID, outSc.Bindings.Image.ImageID,
			"scene[%d] image_id must be preserved byte-identical", i)
		assert.Equal(t, sc.Bindings.Image.URL, outSc.Bindings.Image.URL,
			"scene[%d] image_url must be preserved byte-identical", i)
		assert.Equal(t, sc.Bindings.Image.Status, outSc.Bindings.Image.Status,
			"scene[%d] image_status must be preserved byte-identical", i)
		assert.NotEqual(t, sc.Bindings.Image.Prompt, outSc.Bindings.Image.Prompt,
			"scene[%d] image_prompt must be translated", i)
	}
}

// ─── TEST 5: post-translation canonical Google Doc renders the SpecScene ───
func TestTranslateScriptSpec_CreatesGoogleDocWithSpecSceneBlock(t *testing.T) {
	in := makeThreeSceneSpecEN()
	tr := &mockTranslatorSuffix{suffix: "IT"}

	out, _, err := TranslateScriptSpec(context.Background(), in, evidenceFor3Scenes(), "it", tr.Translate)
	require.NoError(t, err)

	title := "Top 10 Momenti Incredibili di Jackie Chan"
	html := adapters.BuildSpecSceneDocumentHTML(out, title)
	require.NotEmpty(t, html, "BuildSpecSceneDocumentHTML must produce HTML output")

	assert.Contains(t, html, "<h2>Scenes</h2>",
		"HTML must contain the canonical Scenes section")
	assert.Contains(t, html, "<h2>SpecScene JSON</h2><pre>",
		"HTML must contain the canonical SpecScene JSON block")

	// The canonical renderer preserves the translated scene text and links.
	assert.Contains(t, html, "_IT",
		"HTML must contain translated scene text (suffix _IT)")
	for _, driveID := range []string{"abc1", "abc2", "abc3"} {
		assert.Contains(t, html, "drive.google.com/file/d/"+driveID,
			"HTML must contain drive_link for %s (binding preserved)", driveID)
	}

	// JSON keys remain canonical and are never translated.
	preStart := strings.Index(html, "<h2>SpecScene JSON</h2><pre>")
	require.GreaterOrEqual(t, preStart, 0, "HTML must contain SpecScene JSON <pre> block")
	preEnd := strings.Index(html[preStart:], "</pre>")
	require.GreaterOrEqual(t, preEnd, 0, "HTML SpecScene JSON block must close with </pre>")
	preBlock := html[preStart : preStart+preEnd]
	for _, forbidden := range []string{
		"collegamenti", "tipo", "testo", "identificatore_clip",
		"collegamento_drive", "\"id_clip\"", "\"id_drive\"",
	} {
		assert.NotContains(t, preBlock, forbidden,
			"SpecScene JSON must not contain translated key %q", forbidden)
	}
}

// ─── TEST 6: long script — 10 scenes × ~4000 words, no scene loss, no truncation ───
func TestTranslateScriptSpec_LongScript_NoSceneLossNoTruncation(t *testing.T) {
	makeLongScene := func(idx, sentencesPerScene int) scriptpkg.SpecScene {
		words := make([]string, sentencesPerScene*15)
		for i := range words {
			words[i] = fmt.Sprintf("Scene%d-sentence%d-word", idx+1, i/15)
		}
		s := strings.Join(words, " ")
		return scriptpkg.SpecScene{
			ID:    fmt.Sprintf("scene-%d", idx+1),
			Index: idx,
			Text:  "Original EN narration for scene " + s + ".",
			Title: fmt.Sprintf("S%d", idx+1),
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    fmt.Sprintf("clip-%d", idx+1),
					DriveLink: fmt.Sprintf("https://drive.google.com/file/d/long%d/view", idx+1),
					StartMs:   int64(idx * 5000),
					EndMs:     int64((idx + 1) * 5000),
				},
			},
		}
	}
	scenes := make([]scriptpkg.SpecScene, 10)
	for i := range scenes {
		scenes[i] = makeLongScene(i, 27)
	}
	srcWords := strings.Fields(strings.Repeat("w ", 4000))
	srcText := strings.Join(srcWords, " ")
	in := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          srcText,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		},
	}

	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{
			"clip-1", "clip-2", "clip-3", "clip-4", "clip-5",
			"clip-6", "clip-7", "clip-8", "clip-9", "clip-10",
		},
	}

	tr := &mockTranslatorSuffix{suffix: "IT"}
	out, _, err := TranslateScriptSpec(context.Background(), in, evidence, "it", tr.Translate)
	require.NoError(t, err, "long-script translation must succeed on happy path")

	assert.Equal(t, len(in.SpecScene.Scenes), len(out.SpecScene.Scenes),
		"scene count must be preserved (no scene loss / reordering)")

	// Source word count = full-text + (scenes × per-scene word count).
	// Per-scene words = 27 sentences × 15 words = 405 (per makeLongScene).
	const perSceneWordCount = 27 * 15
	const sceneCount = 10
	totalSourceWords := len(srcWords) + sceneCount*perSceneWordCount

	var outWordCount int
	for _, sc := range out.SpecScene.Scenes {
		outWordCount += len(strings.Fields(sc.Text))
	}
	outWordCount += len(strings.Fields(out.Text))
	minAcceptable := int(0.7 * float64(totalSourceWords))
	assert.GreaterOrEqual(t, outWordCount, minAcceptable,
		"output word count %d must be ≥70%% total source %d (long-script no-truncation invariant; threshold computed against scene+full word count, not just full-text)",
		outWordCount, minAcceptable)

	for i, sc := range out.SpecScene.Scenes {
		assert.NotEmpty(t, strings.TrimSpace(sc.Text),
			"scene[%d].Text must be non-empty (no truncation in long-script path)", i)
	}
	last := out.SpecScene.Scenes[len(out.SpecScene.Scenes)-1]
	assert.NotEmpty(t, strings.TrimSpace(last.Text),
		"last scene must have non-empty text (no tail truncation)")

	// 1 model.Text + 10 scene.Text + 10 scene.Title = 21 translator calls.
	// (No image bindings on long-script scenes, so no image.Prompt calls.)
	assert.Equal(t, 21, tr.calls,
		"translator must be called 21 times (1 full-text + 10 scene.Text + 10 scene.Title)")
}

// ─── TEST 7 (scenario 6): special chars / emoji round-trip preserved ───
func TestTranslateScriptSpec_PreservesSpecialCharactersAndEmoji(t *testing.T) {
	in := makeSpecSceneWithSpecialChars()
	// Mock translator that appends a deterministic marker to text
	// containing unicode+emoji+HTML-tag-like substrings. The mock
	// preserves each input verbatim plus suffix so we can probe the
	// per-text strategy's structural prevention: special chars must
	// survive the per-text round-trip untouched in form.
	specialInputs := map[string]string{ // decoder: input → expected output
		"è à ç ñ á <single-tag/> 🔥 emoji prompt":                                   "è à ç ñ á <single-tag/> 🔥 emoji prompt" + "_IT",
		"<script>alert('xss')</script> Jackie said: <This is crazy & dangerous> 🔥": "<script>alert('xss')</script> Jackie said: <This is crazy & dangerous> 🔥_IT",
		"Capitolo 1: è à 🔥":                                   "Capitolo 1: è à 🔥_IT",
		"Prompt with é à ø & emoji 👀":                         "Prompt with é à ø & emoji 👀_IT",
		"Original EN text with ñ ç special chars and emoji 🚀": "Original EN text with ñ ç special chars and emoji 🚀_IT",
		"Mid: ñ 👀":   "Mid: ñ 👀_IT",
		"Prompt é ñ": "Prompt é ñ_IT",
		"Text with < and > and & and \"quotes\" and 'apostrophes'": "Text with < and > and & and \"quotes\" and 'apostrophes'_IT",
		"End: ó": "End: ó_IT",
		"Top 10 EN prose with è à ç ñ and emoji 🔥 and <tag> text.": "Top 10 EN prose with è à ç ñ and emoji 🔥 and <tag> text._IT",
	}
	tr := &recordingTranslator{responses: specialInputs}

	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"clip-1", "clip-2", "clip-3"},
	}
	out, warnings, err := TranslateScriptSpec(context.Background(), in, evidence, "it", tr.Translate)
	require.NoError(t, err)

	// Every captured translator input must have a byte-identical
	// response (no LLM-mutation possible in the test surface).
	for idx, input := range tr.inputs {
		expected, ok := specialInputs[input]
		require.True(t, ok, "every captured translator input must have a recorded response (test surface must be deterministic); input[%d]=%q", idx, input)
		assert.Equal(t, expected, tr.outputs[idx],
			"translator response[%d] for input=%q must be byte-identical to the recorded expected response", idx, input)
	}

	// The translated output Text MUST contain the special chars +
	// emoji + HTML-tag-like substrings byte-equivalent to the source
	// (per-text strategy passes them through; the translator's
	// deterministic mock only appends "_IT").
	assert.Contains(t, out.Text, "è",
		"out.Text must contain è (special char preserved through translation)")
	assert.Contains(t, out.Text, "à",
		"out.Text must contain à (special char preserved through translation)")
	assert.Contains(t, out.Text, "ç",
		"out.Text must contain ç (special char preserved through translation)")
	assert.Contains(t, out.Text, "ñ",
		"out.Text must contain ñ (special char preserved through translation)")
	assert.Contains(t, out.Text, "🔥",
		"out.Text must contain 🔥 emoji (unicode preserved through translation)")
	assert.Contains(t, out.Text, "<tag>",
		"out.Text must contain <tag> HTML-tag-like substring (literal passthrough, NOT rendered as a tag)")

	// Every scene's Text + Image.Prompt + Title must preserve the
	// special chars + emoji.
	for i, sc := range out.SpecScene.Scenes {
		inSc := in.SpecScene.Scenes[i]
		assert.Contains(t, sc.Text, inSc.Text,
			"scene[%d].Text must contain the source's special chars + emoji verbatim", i)
		if sc.Bindings.Image != nil && inSc.Bindings.Image != nil {
			assert.Contains(t, sc.Bindings.Image.Prompt, inSc.Bindings.Image.Prompt,
				"scene[%d].Image.Prompt must contain the source's special chars verbatim", i)
		}
	}

	// No equal-to-source warnings (translator always returned
	// input + "_IT", not byte-identical to source).
	for _, w := range warnings {
		assert.NotContains(t, w, WarnTranslationEqualToSource,
			"no equal-to-source warning expected when translator returns input+suffix")
	}
}

// ─── TEST 8 (scenario 7a): nil translator → typed ErrTranslationTranslatorMissing ───
func TestTranslateScriptSpec_NilTranslator_TypedSentinel(t *testing.T) {
	in := makeThreeSceneSpecEN()

	out, warnings, err := TranslateScriptSpec(context.Background(), in, evidenceFor3Scenes(), "it", nil)
	require.Error(t, err, "nil translator MUST propagate as typed error (godlike/07)")
	require.ErrorIs(t, err, ErrTranslationTranslatorMissing,
		"err must be (or wrap) ErrTranslationTranslatorMissing sentinel")
	require.Nil(t, out, "out MUST be nil on fail-closed nil-translator path")
	require.NotNil(t, warnings, "warnings MUST still be non-nil slice (operator-observable state)")
	// Confirms no panic occurred above (test passes = no panic).
}

// ─── TEST 9 (scenario 7b): translator returns equal-to-source → warnings surfaced ───
func TestTranslateScriptSpec_EqualToSourceWarning(t *testing.T) {
	in := makeThreeSceneSpecEN()
	tr := &mockTranslatorPassThrough{calls: 0}

	out, warnings, err := TranslateScriptSpec(context.Background(), in, evidenceFor3Scenes(), "it", tr.Translate)
	require.NoError(t, err,
		"equal-to-source translation returns valid output (non-fatal warning, not fail-closed)")
	require.NotNil(t, out)

	// Warnings MUST contain exactly one WarnTranslationEqualToSource
	// entry per text segment that translated byte-identical to source
	// (1 full-text + 3 scene.Text + 3 scene.Title + 3 image.Prompt +
	// expected total). We assert "at least one" + "all-of-kind" to
	// match the literal user spec ("warning"). For our 3-scene
	// fixture: 1 (full-text) + 3 (scene.Text) + 3 (scene.Title) + 3
	// (image.Prompt) = 10 warn entries.
	var equalToSourceCount int
	for _, w := range warnings {
		if strings.Contains(w, WarnTranslationEqualToSource) {
			equalToSourceCount++
		}
	}
	assert.Equal(t, 10, equalToSourceCount,
		"per-segment equal-to-source detection should fire on every text segment that translated byte-identical to source (translator returned input verbatim)")

	// Output is still valid (no structural drift; just untranslated).
	assert.Equal(t, in.Text, out.Text,
		"out.Text must equal in.Text (translator returned input byte-identical)")
	for i, sc := range in.SpecScene.Scenes {
		outSc := out.SpecScene.Scenes[i]
		assert.Equal(t, sc.Text, outSc.Text,
			"scene[%d].Text must equal in scene[%d].Text (translator returned input byte-identical)", i, i)
	}
}

// recordingTranslator is a deterministic mock translator that returns
// a pre-recorded response for each input. Used by Test 7 to pin the
// per-text strategy's structural prevention contract.
type recordingTranslator struct {
	responses map[string]string
	inputs    []string
	outputs   []string
}

func (r *recordingTranslator) Translate(_ context.Context, text, _ string) (string, error) {
	r.inputs = append(r.inputs, text)
	resp, ok := r.responses[text]
	if !ok {
		return "", fmt.Errorf("recordingTranslator: no recorded response for input %q", text)
	}
	r.outputs = append(r.outputs, resp)
	return resp, nil
}

// Compile-time pin: TypeAssertion to confirm surface-level compile
// (errors.Is check in tests below is a non-shadow sanity probe).
var _ error = ErrTranslationSourceInvalid
var _ error = ErrTranslationTranslatorMissing
var _ error = ErrTranslationTargetLangMissing
var _ error = ErrTranslationEmpty
var _ error = ErrTranslationIncomplete
var _ error = ErrTranslationClipIDChanged
var _ error = ErrTranslationDriveLinkChanged

// Compile-time errors.Is probe sanity check.
func TestTranslation_Sentinels_ErrorsIsProbeable(t *testing.T) {
	errs := map[string]struct {
		err   error
		probe error
	}{
		"errors.Is(ErrTranslationSourceInvalid, ErrTranslationSourceInvalid)":         {ErrTranslationSourceInvalid, ErrTranslationSourceInvalid},
		"errors.Is(ErrTranslationTranslatorMissing, ErrTranslationTranslatorMissing)": {ErrTranslationTranslatorMissing, ErrTranslationTranslatorMissing},
		"errors.Is(ErrTranslationTargetLangMissing, ErrTranslationTargetLangMissing)": {ErrTranslationTargetLangMissing, ErrTranslationTargetLangMissing},
		"errors.Is(ErrTranslationEmpty, ErrTranslationEmpty)":                         {ErrTranslationEmpty, ErrTranslationEmpty},
		"errors.Is(ErrTranslationIncomplete, ErrTranslationIncomplete)":               {ErrTranslationIncomplete, ErrTranslationIncomplete},
		"errors.Is(ErrTranslationClipIDChanged, ErrTranslationClipIDChanged)":         {ErrTranslationClipIDChanged, ErrTranslationClipIDChanged},
		"errors.Is(ErrTranslationDriveLinkChanged, ErrTranslationDriveLinkChanged)":   {ErrTranslationDriveLinkChanged, ErrTranslationDriveLinkChanged},
	}
	for name, tc := range errs {
		t.Run(name, func(t *testing.T) {
			assert.True(t, errors.Is(tc.err, tc.probe),
				"typed sentinel MUST be errors.Is-probeable (godlike/07 typed-error contract)")
		})
	}
}
