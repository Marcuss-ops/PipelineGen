package scriptgeneration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func chapterBulletFixture() (*GenerateResult, []Scene, []ResolvedScene, []capabilityoverlay.SceneInput) {
	// No voiceover anywhere: chapter bullets are visual-only and must not
	// need TTS timing.
	s1Text := "Il caso riguarda la morte di Chiara Poggi, avvenuta il 13 agosto 2007 nella sua abitazione a Garlasco."
	s2Text := "Alberto Stasi e stato condannato in via definitiva a sedici anni di reclusione per lomicidio."
	result := &GenerateResult{
		SourceLanguage: "it",
		Output:         GenerateOutput{Text: s1Text + "\n\n" + s2Text},
		ChapterManifest: &scriptpkg.ChapterManifest{
			SchemaVersion: "chapter_manifest.v1",
			Chapters: []scriptpkg.ChapterManifestEntry{
				{Title: "Delitto", TitleSource: "scene_topic", StartSentence: 0, EndSentence: 1, Bullets: []scriptpkg.ChapterBullet{
					{StartSentence: 0, EndSentence: 1, Text: "Il caso riguarda la morte di Chiara Poggi, avvenuta il 13 agosto 2007 nella sua abitazione a Garlasco."},
				}},
				{Title: "Condanna", TitleSource: "scene_topic", StartSentence: 1, EndSentence: 2, Bullets: []scriptpkg.ChapterBullet{
					{StartSentence: 1, EndSentence: 2, Text: "Alberto Stasi e stato condannato in via definitiva a sedici anni di reclusione per lomicidio."},
				}},
			},
		},
	}
	scenes := []Scene{
		{ID: "s1", Index: 0, Text: map[Language]string{"it": s1Text}},
		{ID: "s2", Index: 1, Text: map[Language]string{"it": s2Text}},
	}
	resolved := []ResolvedScene{
		{ID: "s1", Index: 0, TimelineStartUS: 0, DurationUS: 6_000_000},
		{ID: "s2", Index: 1, TimelineStartUS: 6_000_000, DurationUS: 3_000_000},
	}
	return result, scenes, resolved, []capabilityoverlay.SceneInput{{ID: "s1"}, {ID: "s2"}}
}

func sceneBulletRole(phrases []capabilityoverlay.TimedAnnotation) *capabilityoverlay.TimedAnnotation {
	for i := range phrases {
		if phrases[i].PresentationRole == ChapterBulletRole {
			return &phrases[i]
		}
	}
	return nil
}

func TestProjectChapterBulletsWithoutVoiceover(t *testing.T) {
	result, scenes, resolved, inputs := chapterBulletFixture()
	got, covered := projectChapterBullets(result, "it", scenes, resolved, inputs)
	require.True(t, covered["s1"])
	require.True(t, covered["s2"])
	require.Len(t, got, 2)
	b1 := sceneBulletRole(got[0].Phrases)
	require.NotNil(t, b1, "scene s1 must carry its chapter bullet with no TTS")
	require.Equal(t, "Il caso riguarda la morte di Chiara Poggi, avvenuta il 13 agosto 2007 nella sua abitazione a Garlasco.", b1.Text)
	require.Equal(t, int64(0), b1.StartUS, "no phrases: card opens at the scene window start")
	require.Equal(t, int64(4_000_000), b1.DurationUS)
	b2 := sceneBulletRole(got[1].Phrases)
	require.NotNil(t, b2)
	require.Equal(t, int64(6_000_000), b2.StartUS)
	require.Equal(t, int64(3_000_000), b2.DurationUS, "card is clamped to a window shorter than the readable beat")
	require.Equal(t, int64(6000), b2.StartMs)
	require.Equal(t, int64(9000), b2.EndMs)
	for _, b := range []*capabilityoverlay.TimedAnnotation{b1, b2} {
		require.Greater(t, b.DurationUS, int64(0))
		require.GreaterOrEqual(t, b.StartUS, int64(0))
	}
}

func TestProjectChapterBulletsAnchorsToFirstPhrase(t *testing.T) {
	result, scenes, resolved, inputs := chapterBulletFixture()
	inputs[0].Phrases = []capabilityoverlay.TimedAnnotation{{
		Text: "Il caso riguarda", StartUS: 500_000, DurationUS: 1_500_000,
		StartMs: 500, EndMs: 2000, Score: 0.5,
	}}
	got, covered := projectChapterBullets(result, "it", scenes, resolved, inputs)
	require.True(t, covered["s1"])
	bullet := sceneBulletRole(got[0].Phrases)
	require.NotNil(t, bullet)
	require.Equal(t, int64(500_000), bullet.StartUS, "bullet links to the first phrase, it is not read")
	require.Equal(t, int64(500), bullet.StartMs)
}

func TestProjectChapterBulletsFailsClosed(t *testing.T) {
	t.Run("nil manifest", func(t *testing.T) {
		result, scenes, resolved, inputs := chapterBulletFixture()
		result.ChapterManifest = nil
		got, covered := projectChapterBullets(result, "it", scenes, resolved, inputs)
		require.Empty(t, covered)
		require.Empty(t, sceneBulletRole(got[0].Phrases))
	})
	t.Run("language mismatch", func(t *testing.T) {
		result, scenes, resolved, inputs := chapterBulletFixture()
		got, covered := projectChapterBullets(result, "en", scenes, resolved, inputs)
		require.Empty(t, covered)
		require.Empty(t, got[0].Phrases)
	})
	t.Run("ungrounded bullet never leaks", func(t *testing.T) {
		result, scenes, resolved, inputs := chapterBulletFixture()
		result.ChapterManifest.Chapters[0].Bullets[0].Text = "Not spoken anywhere in these scenes at all."
		got, covered := projectChapterBullets(result, "it", scenes, resolved, inputs)
		require.False(t, covered["s1"])
		require.Empty(t, got[0].Phrases)
	})
	t.Run("fragment bullet dropped", func(t *testing.T) {
		result, scenes, resolved, inputs := chapterBulletFixture()
		result.ChapterManifest.Chapters[1].Bullets[0].Text = "sedici anni"
		// "sedici anni" IS contained in s2, but it is a fragment, not a point.
		got, covered := projectChapterBullets(result, "it", scenes, resolved, inputs)
		require.False(t, covered["s2"])
		require.Empty(t, got[1].Phrases)
	})
	t.Run("one bullet renders once", func(t *testing.T) {
		result, scenes, resolved, inputs := chapterBulletFixture()
		shared := "Il caso riguarda la morte di Chiara Poggi, avvenuta il 13 agosto 2007 nella sua abitazione a Garlasco."
		result.ChapterManifest.Chapters[1].Bullets[0].Text = shared
		scenes[1].Text["it"] = scenes[1].Text["it"] + " " + shared
		result.Scenes = scenes
		got, covered := projectChapterBullets(result, "it", scenes, resolved, inputs)
		rendered := 0
		for i := range got {
			if sceneBulletRole(got[i].Phrases) != nil {
				rendered++
			}
		}
		require.Equal(t, 1, rendered, "a bullet shared by two scenes must render once")
		require.True(t, covered["s1"])
		require.False(t, covered["s2"])
	})
	t.Run("missing bounds", func(t *testing.T) {
		result, scenes, _, inputs := chapterBulletFixture()
		got, covered := projectChapterBullets(result, "it", scenes, nil, inputs)
		require.Empty(t, covered)
		for i := range got {
			require.Empty(t, got[i].Phrases)
		}
	})
}

func TestProjectChapterBulletsMergedSpanOpensChapter(t *testing.T) {
	result, scenes, resolved, inputs := chapterBulletFixture()
	// Rust merges contiguous spans: one bullet quoting all three scenes.
	merged := strings.TrimSpace(scenes[0].Text["it"]) + " " + strings.TrimSpace(scenes[1].Text["it"])
	result.ChapterManifest.Chapters = []scriptpkg.ChapterManifestEntry{{
		Title: "Capitolo", TitleSource: "scene_topic", StartSentence: 0, EndSentence: 2,
		Bullets: []scriptpkg.ChapterBullet{{StartSentence: 0, EndSentence: 2, Text: merged}},
	}}
	got, covered := projectChapterBullets(result, "it", scenes, resolved, inputs)
	require.True(t, covered["s1"], "merged bullet opens the chapter on its first scene")
	require.False(t, covered["s2"])
	bullet := sceneBulletRole(got[0].Phrases)
	require.NotNil(t, bullet)
	require.Equal(t, merged, bullet.Text)
	require.Equal(t, int64(0), bullet.StartUS)
}

func TestChapterBulletRoleMapsToBulletsCategory(t *testing.T) {
	item := capabilityoverlay.OverlayItem{ID: "c1", SceneID: "s1", Kind: "text_phrase",
		TemplateID: "IMPORTANT_PHRASE", Params: map[string]any{"editorial_role": ChapterBulletRole}}
	category, ok := overlayItemCategory(item)
	require.True(t, ok, "chapter role must categorize")
	require.Equal(t, scriptpkg.OverlayCategoryBullets, category)
}

func TestChapterBulletFontBumpOverPhraseSize(t *testing.T) {
	fontSize := 96.0
	canvas := OverlayCanvasSpec{Style: &scriptpkg.OverlayStyleSpec{
		Size: &scriptpkg.OverlaySizeSpec{FontSize: &fontSize},
	}}
	items := []capabilityoverlay.OverlayItem{
		{ID: "c1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Chapter card",
			Params: map[string]any{"editorial_role": ChapterBulletRole}},
		{ID: "p1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Ordinary phrase",
			Params: map[string]any{}},
	}
	applyOverlayStyleToItems(items, canvas)
	chapterFont, ok := items[0].Params["font_size_px"].(float64)
	require.True(t, ok, "chapter card must carry a font size")
	require.Equal(t, 96.0+float64(capabilityoverlay.ChapterTextFontIncreasePX), chapterFont)
	phraseFont, ok := items[1].Params["font_size_px"].(float64)
	require.True(t, ok)
	require.Equal(t, 96.0, phraseFont, "ordinary phrases keep the run size")
	style, _ := items[0].Params["style"].(map[string]any)
	require.Equal(t, chapterFont, style["font_size"])
}

func TestChapterBulletCoveredSkipsEditorialBulletKeepsTitle(t *testing.T) {
	result, scenes, resolved, inputs := editorialOverlayFixture()
	// Wire a chapter manifest grounded in the same scene text.
	result.ChapterManifest = &scriptpkg.ChapterManifest{
		SchemaVersion: "chapter_manifest.v1",
		Chapters: []scriptpkg.ChapterManifestEntry{{
			Title: "Steel", TitleSource: "scene_topic", StartSentence: 0, EndSentence: 2,
			Bullets: []scriptpkg.ChapterBullet{{
				StartSentence: 1, EndSentence: 2,
				Text: "Duisburg factory orders fell sharply during Tuesday afternoon.",
			}},
		}},
	}
	result.SourceLanguage = "en"
	afterChapter, covered := projectChapterBullets(result, "en", scenes, resolved, inputs)
	require.True(t, covered["s1"])
	got := projectEditorialOverlays(result, "en", scenes, resolved, afterChapter, covered)
	require.Len(t, got, 1)
	require.Len(t, got[0].Phrases, 2, "chapter bullet + editorial title, no duplicated bullet")
	roles := map[string]string{}
	for _, phrase := range got[0].Phrases {
		roles[phrase.PresentationRole] = phrase.Text
	}
	require.Equal(t, "Steel output is collapsing.", roles["scene_title"])
	require.Equal(t, "Duisburg factory orders fell sharply during Tuesday afternoon.", roles[ChapterBulletRole])
	require.True(t, strings.Contains(roles[ChapterBulletRole], "Duisburg"))
}
