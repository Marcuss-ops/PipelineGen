package scriptgeneration

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func editorialOverlayFixture() (*GenerateResult, []Scene, []ResolvedScene, []capabilityoverlay.SceneInput) {
	text := "Steel output is collapsing. Duisburg factory orders fell sharply during Tuesday afternoon."
	words := strings.Fields(text)
	boundaries := make([]capabilityaudio.SpeechWordTiming, len(words))
	for i, word := range words {
		boundaries[i] = capabilityaudio.SpeechWordTiming{Index: i, Text: word, StartUS: int64(i) * 100_000, EndUS: int64(i+1) * 100_000}
	}
	timing := capabilityaudio.SpeechTimingArtifact{Version: capabilityaudio.SpeechTimingVersion, Provider: "test", BoundaryMode: capabilityaudio.BoundaryWord, Language: "en", TextSHA256: "text", AudioSHA256: "audio", DurationUS: int64(len(words)) * 100_000, Words: boundaries}
	title := "Steel output is collapsing."
	transcript := "Steel output is collapsing.\n\nDuisburg factory orders fell sharply during Tuesday afternoon."
	manifest := &scriptpkg.EditorialManifest{SchemaVersion: scriptpkg.EditorialManifestVersion, SourceLanguage: "en", ProfileVersion: "highlights.v1", Certified: true, Fingerprint: strings.Repeat("a", 64), Scenes: []scriptpkg.EditorialScene{{
		SceneID: "s1", Title: &title, TitleStatus: "resolved", TitleSource: "topic_validated", GloballyIndexed: true,
		Bullets: []scriptpkg.EditorialSceneBullet{{SentenceStart: 1, SentenceEnd: 2, Text: "Duisburg factory orders fell sharply during Tuesday afternoon."}}, Highlights: []scriptpkg.EditorialHighlight{},
	}}}
	scene := Scene{ID: "s1", Index: 0, Text: map[Language]string{"en": text}, Voiceover: map[Language]AudioReference{"en": {ID: "vo-s1", Timing: &timing}}}
	result := &GenerateResult{SourceLanguage: "en", Output: GenerateOutput{Text: transcript}, Editorial: manifest, Scenes: []Scene{scene}}
	return result, []Scene{scene}, []ResolvedScene{{ID: "s1", Index: 0, TimelineStartUS: 2_000_000, DurationUS: 3_000_000}}, []capabilityoverlay.SceneInput{{ID: "s1"}}
}

func TestProjectEditorialOverlaysGroundedAndAudioTimed(t *testing.T) {
	result, scenes, resolved, inputs := editorialOverlayFixture()
	got := projectEditorialOverlays(result, "en", scenes, resolved, inputs, nil)
	require.Len(t, got, 1)
	require.Len(t, got[0].Phrases, 2)
	roles := map[string]capabilityoverlay.TimedAnnotation{}
	for _, phrase := range got[0].Phrases {
		roles[phrase.PresentationRole] = phrase
		require.GreaterOrEqual(t, phrase.StartUS, int64(2_000_000))
		require.LessOrEqual(t, phrase.StartUS+phrase.DurationUS, int64(5_000_000))
	}
	title := roles["scene_title"]
	require.Equal(t, "Steel output is collapsing.", title.Text)
	require.Equal(t, int64(2_000_000), title.StartUS)
	bullet := roles["scene_bullet"]
	require.Equal(t, "Duisburg factory orders fell sharply during Tuesday afternoon.", bullet.Text)
	require.Equal(t, title.StartUS+title.DurationUS, bullet.StartUS, "bullet follows the title at its spoken word boundary")
}

func TestEditorialBulletUsesFullExtractiveSentenceForLongMotionLane(t *testing.T) {
	result, scenes, resolved, inputs := editorialOverlayFixture()
	// The semantic highlighter returns a short phrase, but this bullet-only
	// mode deliberately shows the complete source sentence so 3D/camera
	// motions for long phrases can be applied without paraphrasing.
	result.Editorial.Scenes[0].Highlights = []scriptpkg.EditorialHighlight{{Text: "Duisburg factory", Score: 0.9, VisualEligible: true}}
	got := projectEditorialOverlays(result, "en", scenes, resolved, inputs, nil)
	require.Len(t, got, 1)
	var bullet *capabilityoverlay.TimedAnnotation
	for i := range got[0].Phrases {
		if got[0].Phrases[i].PresentationRole == "scene_bullet" {
			bullet = &got[0].Phrases[i]
		}
	}
	require.NotNil(t, bullet, "the certified two-word highlight must still render as a scene bullet")
	require.Equal(t, "Duisburg factory orders fell sharply during Tuesday afternoon.", bullet.Text)
	require.GreaterOrEqual(t, len(strings.Fields(bullet.Text)), 6)
	require.Equal(t, int64(2_400_000), bullet.StartUS, "bullet starts at the first spoken word")
	require.Greater(t, bullet.DurationUS, int64(0))
}

func TestShortPhraseSceneDoesNotBecomeFallbackBullet(t *testing.T) {
	result, scenes, resolved, inputs := editorialOverlayFixture()
	inputs[0].Phrases = []capabilityoverlay.TimedAnnotation{{
		Text: "The short phrase stays a phrase.", StartMs: 0, EndMs: 300,
		StartUS: 0, DurationUS: 300_000, Score: 1,
	}}
	got := projectEditorialOverlays(result, "en", scenes, resolved, inputs, nil, nil, map[string]bool{"s1": true})
	require.Len(t, got, 1)
	require.Len(t, got[0].Phrases, 1)
	require.Empty(t, got[0].Phrases[0].PresentationRole, "short phrase must not be reclassified as a fallback bullet")
}

func TestDisabledEditorialCategoriesDoNotEvictGroundedPhrase(t *testing.T) {
	result, scenes, resolved, inputs := editorialOverlayFixture()
	text := "Steel output is collapsing."
	inputs[0].Phrases = []capabilityoverlay.TimedAnnotation{{
		Text: text, StartMs: 0, EndMs: 300, StartUS: 0, DurationUS: 300_000, Score: 1,
	}}
	globalCategories := map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryTitles:  scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryBullets: scriptpkg.ToggleDisabled,
	}
	got := projectEditorialOverlays(result, "en", scenes, resolved, inputs, globalCategories)
	require.Len(t, got, 1)
	require.Len(t, got[0].Phrases, 1, "disabled title/bullet projections must not replace the normal phrase candidate")
	require.Equal(t, text, got[0].Phrases[0].Text)
	require.Empty(t, got[0].Phrases[0].PresentationRole)
}

func TestEditorialBulletLongPhraseReturnsExactExtractiveSpan(t *testing.T) {
	bullet := scriptpkg.EditorialSceneBullet{Text: "Garlasco, provincia di Pavia."}
	require.Equal(t, "Garlasco, provincia di Pavia.", editorialBulletLongPhrase(bullet))
}

func TestCompileBulletOnlyPlayfairCameraPushAcceptance(t *testing.T) {
	texts := []string{
		"La chiusura delle indagini non equivale a una condanna.",
		"Le parti possono contestare gli accertamenti con consulenze tecniche.",
		"Ogni reperto va valutato nel contesto e con verifiche incrociate.",
		"Le relazioni di stampa vanno distinte dagli atti ufficiali depositati.",
		"La traccia controversa resta oggetto di valutazioni tecniche contrapposte.",
		"Nessun singolo reperto dimostra da solo una responsabilità.",
		"Le nuove ipotesi investigative restano da verificare nel contraddittorio.",
		"Le sentenze definitive vanno separate dalle indagini ancora aperte.",
		"La procura può valutare processo, archiviazione o ulteriori integrazioni.",
		"Ogni consulenza va letta insieme ai metodi e ai suoi limiti.",
		"Le memorie difensive aprono una fase, non anticipano una decisione.",
		"La presunzione di innocenza accompagna ogni indagato fino alla sentenza definitiva.",
		"La memoria di Chiara Poggi merita un racconto rispettoso e sobrio.",
		"Fonti processuali e cronologia aiutano a distinguere fatti e interpretazioni.",
		"Ogni affermazione del copione deve poggiare su fonti verificabili.",
	}
	result := &GenerateResult{SourceLanguage: "it"}
	var transcript strings.Builder
	result.Scenes = make([]Scene, 0, len(texts))
	result.ResolvedScenes = make([]ResolvedScene, 0, len(texts))
	result.Editorial = &scriptpkg.EditorialManifest{
		SchemaVersion: scriptpkg.EditorialManifestVersion, SourceLanguage: "it", ProfileVersion: "highlights.v1",
		Certified: true, Fingerprint: strings.Repeat("a", 64), Scenes: make([]scriptpkg.EditorialScene, 0, len(texts)),
	}
	for i, text := range texts {
		if i > 0 {
			transcript.WriteString("\n\n")
		}
		transcript.WriteString(text)
		words := strings.Fields(text)
		timings := make([]capabilityaudio.SpeechWordTiming, len(words))
		for wordIndex, word := range words {
			timings[wordIndex] = capabilityaudio.SpeechWordTiming{Index: wordIndex, Text: word, StartUS: int64(wordIndex) * 400_000, EndUS: int64(wordIndex+1) * 400_000}
		}
		timing := capabilityaudio.SpeechTimingArtifact{Version: capabilityaudio.SpeechTimingVersion, Provider: "test", BoundaryMode: capabilityaudio.BoundaryWord, Language: "it", TextSHA256: "text", AudioSHA256: "audio", DurationUS: int64(len(words)) * 400_000, Words: timings}
		sceneID := fmt.Sprintf("important-phrase-%02d", i+1)
		result.Scenes = append(result.Scenes, Scene{ID: sceneID, Index: i, Text: map[Language]string{"it": text}, Voiceover: map[Language]AudioReference{"it": {ID: "vo-" + sceneID, Timing: &timing}}})
		result.ResolvedScenes = append(result.ResolvedScenes, ResolvedScene{ID: sceneID, Index: i, TimelineStartUS: int64(i) * 6_000_000, DurationUS: 6_000_000})
		title := text
		result.Editorial.Scenes = append(result.Editorial.Scenes, scriptpkg.EditorialScene{
			SceneID: sceneID, Title: &title, TitleStatus: "resolved", TitleSource: "topic_validated",
			Bullets:    []scriptpkg.EditorialSceneBullet{{SentenceStart: 0, SentenceEnd: 1, Text: text}},
			Highlights: []scriptpkg.EditorialHighlight{},
		})
	}
	result.Output.Text = transcript.String()

	glow := 10.0
	fontSize := 96.0
	canvas := GoldenOverlayCanvas
	canvas.MaxPhraseOverlays = len(texts)
	canvas.MaxOverlaysPerScene = 2
	canvas.AnimationCounts = map[string]int{"important_phrase": len(texts), "bullets": len(texts), "chain_disabled": 1}
	canvas.PhraseMotionFamily = "modern_product_v1"
	canvas.PhraseMotions = []string{"cinematic_camera_push"}
	canvas.OverlayCategories = map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryPhrases:   scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryImages:    scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryMaps:      scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryNumbers:   scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryQuotes:    scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryBrandText: scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryCharts:    scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryRecaps:    scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryTitles:    scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryBullets:   scriptpkg.ToggleEnabled,
	}
	canvas.Style = &scriptpkg.OverlayStyleSpec{
		FontFamily: "playfair_display_italic", Color: []float64{1, 1, 1, 1},
		Size: &scriptpkg.OverlaySizeSpec{FontSize: &fontSize}, GlowSize: &glow, GlowColor: "#FFFFFF",
	}
	canvas.Background = &capabilityoverlay.OverlayBackground{Kind: "color", Color: []float64{0, 0, 0, 1}, Opacity: func() *float64 { value := 1.0; return &value }()}
	plan, err := CompileOverlayPlan(result, "it", canvas, "bullet-acceptance", "video-bullet-acceptance", "garlasco")
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.Len(t, plan.Items, len(texts), "every source scene must materialize exactly one bullet and no other overlay")
	require.Equal(t, "color", plan.Background.Kind)
	require.Equal(t, []float64{0, 0, 0, 1}, plan.Background.Color)
	require.NotNil(t, plan.Background.Opacity)
	require.Equal(t, 1.0, *plan.Background.Opacity)
	for i, item := range plan.Items {
		require.Equal(t, "text_phrase", item.Kind)
		require.Equal(t, "scene_bullet", item.Params["editorial_role"])
		require.Equal(t, texts[i], item.Text, "bullet text must remain the complete grounded sentence")
		require.Equal(t, "cinematic_camera_push", item.MotionID, "explicit singleton motion pool must survive the uniqueness guard")
		require.Equal(t, "playfair_display_italic", item.Params["font_family"])
		require.Equal(t, []float64{1, 1, 1, 1}, item.Params["color"])
		require.Equal(t, 96.0, item.Params["font_size_px"])
		require.Equal(t, 10.0, item.Params["glow_size"])
		require.Equal(t, "#FFFFFF", item.Params["glow_color"])
	}
}

func TestExpandEditorialPhraseToMinimumWords(t *testing.T) {
	tests := []struct {
		name      string
		language  string
		sceneText string
		phrase    string
		want      string
	}{
		{"grows two words past dangling conjunction and auxiliary", "it", "sono svolti eventi che hanno segnato", "svolti eventi", "svolti eventi che hanno segnato"},
		{"never ends on a preposition", "it", "si è subito focalizzata sulla casa stessa", "subito focalizzata", "subito focalizzata sulla casa"},
		{"never ends on a conjunction", "it", "con sentenze emesse e successive revisioni", "sentenze emesse", "sentenze emesse e successive"},
		{"fails closed when only function words follow", "it", "svolti eventi e", "svolti eventi", "svolti eventi"},
		{"grows past a hanging comma", "it", "inquirenti raccolgono impronte, testimonianze e reperti", "inquirenti raccolgono", "inquirenti raccolgono impronte, testimonianze"},
		{"keeps three words", "it", "sono svolti eventi che hanno segnato", "svolti eventi che", "svolti eventi che"},
		{"keeps longer phrases", "en", "Alpha bravo charlie delta", "bravo charlie delta", "bravo charlie delta"},
		{"fails closed when ungrounded", "it", "sono svolti eventi", "missing phrase", "missing phrase"},
		{"fails closed at the end of a short scene", "it", "unico frammento", "unico frammento", "unico frammento"},
		{"matches case insensitively", "it", "Svolti Eventi Che Hanno Segnato", "svolti eventi", "Svolti Eventi Che Hanno Segnato"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, expandEditorialPhraseToMinimumWords(tc.language, tc.sceneText, tc.phrase))
		})
	}
}

func TestProjectEditorialOverlaysFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GenerateResult, []Scene)
	}{
		{"uncertified", func(r *GenerateResult, _ []Scene) { r.Editorial.Certified = false }},
		{"bad fingerprint", func(r *GenerateResult, _ []Scene) { r.Editorial.Fingerprint = "" }},
		{"language mismatch", func(r *GenerateResult, _ []Scene) { r.Editorial.SourceLanguage = "it" }},
		{"unmatched scene", func(r *GenerateResult, _ []Scene) { r.Editorial.Scenes[0].SceneID = "other" }},
		{"ungrounded title", func(r *GenerateResult, _ []Scene) { bad := "invented title"; r.Editorial.Scenes[0].Title = &bad }},
		{"source language mismatch", func(r *GenerateResult, _ []Scene) { r.SourceLanguage = "it" }},
		{"unsupported title source", func(r *GenerateResult, _ []Scene) { r.Editorial.Scenes[0].TitleSource = "model_generated" }},
		{"ungrounded bullet", func(r *GenerateResult, _ []Scene) { r.Editorial.Scenes[0].Bullets[0].Text = "Not spoken." }},
		{"invalid bullet span", func(r *GenerateResult, _ []Scene) { r.Editorial.Scenes[0].Bullets[0].SentenceEnd = 0 }},
		{"missing timing", func(r *GenerateResult, scenes []Scene) { scenes[0].Voiceover = nil; r.Scenes = scenes }},
		{"missing scene bounds", func(_ *GenerateResult, _ []Scene) {}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, scenes, resolved, inputs := editorialOverlayFixture()
			tc.mutate(result, scenes)
			if tc.name == "missing scene bounds" {
				resolved = nil
			}
			got := projectEditorialOverlays(result, "en", scenes, resolved, inputs, nil)
			switch tc.name {
			case "ungrounded title", "unsupported title source":
				require.Len(t, got[0].Phrases, 1, "the independently grounded bullet may render, but the invalid title must not leak")
				require.Equal(t, "scene_bullet", got[0].Phrases[0].PresentationRole)
			case "ungrounded bullet":
				require.Len(t, got[0].Phrases, 1, "the independently grounded title may render, but the invalid bullet must not leak")
				require.Equal(t, "scene_title", got[0].Phrases[0].PresentationRole)
			case "invalid bullet span":
				require.Len(t, got[0].Phrases, 1, "the valid title survives an invalid bullet span")
				require.Equal(t, "scene_title", got[0].Phrases[0].PresentationRole)
			default:
				require.Empty(t, got[0].Phrases, "invalid editorial input cannot leak into visual candidates")
			}
		})
	}
}

func TestEditorialChartValuesMustBeGroundedStrictLiterals(t *testing.T) {
	transcript := "The metric increased from 42% to €18.5, while thirteen jobs changed."
	title := "metric increased"
	base := scriptpkg.EditorialScene{
		SceneID: "scene-1", Title: &title, TitleStatus: "resolved", TitleSource: "topic_validated",
		Bullets: []scriptpkg.EditorialSceneBullet{}, Highlights: []scriptpkg.EditorialHighlight{},
		ChartValues: []string{"42%", "€18.5"},
	}
	text := "The metric increased from 42% to €18.5, while thirteen jobs changed."
	require.True(t, validEditorialManifestScene(base, text, transcript))
	base.ChartValues = []string{"42%", "thirteen"}
	require.False(t, validEditorialManifestScene(base, text, transcript), "number words must not authorize charts")
	base.ChartValues = []string{"42%", "999%"}
	require.False(t, validEditorialManifestScene(base, text, transcript), "invented values must not authorize charts")
}

func TestProjectEditorialOverlaysRequiresExactVoiceoverLanguageAndSceneText(t *testing.T) {
	result, scenes, resolved, inputs := editorialOverlayFixture()
	result.Editorial.SourceLanguage = "it"
	got := projectEditorialOverlays(result, "en", scenes, resolved, inputs, nil)
	require.Empty(t, got[0].Phrases)

	result, scenes, resolved, inputs = editorialOverlayFixture()
	scenes[0].Text["en"] = "Translated steel output is collapsing. Duisburg factory orders fell sharply."
	timing := *scenes[0].Voiceover["en"].Timing
	timing.Words[0].Text = "Translated"
	scenes[0].Voiceover["en"] = AudioReference{ID: "vo-s1", Timing: &timing}
	result.Scenes = scenes
	got = projectEditorialOverlays(result, "en", scenes, resolved, inputs, nil)
	require.Empty(t, got[0].Phrases, "both title/bullet must ground in the actual scene and timing")
}
