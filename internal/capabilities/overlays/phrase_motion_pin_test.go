package overlays

import (
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func phraseMotionPinScenes() []SceneInput {
	return []SceneInput{{ID: "scene-pin", Phrases: []TimedAnnotation{
		{Text: "tre parole molto brevi", StartMs: 0, EndMs: 1500, Score: 0.99},
		{Text: "un'altra frase breve qui", StartMs: 1600, EndMs: 3100, Score: 0.98},
		{Text: "questa è una frase lunga che supera ampiamente le sei parole richieste", StartMs: 3200, EndMs: 5200, Score: 0.97},
		{Text: "un'altra frase lunga con molte parole per verificare il pin", StartMs: 5300, EndMs: 7300, Score: 0.96},
	}}}
}

func phraseMotionPinInput(scenes []SceneInput) PlanInput {
	return PlanInput{
		PlanID: "phrase-motion-pin", VideoID: "video-pin",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: scenes,
	}
}

func distinctLongPhrasePin(shortPin string) string {
	for _, id := range LongPhraseMotionCandidates() {
		if id != shortPin {
			return id
		}
	}
	return ""
}

func TestDocumentedPhraseMotionPinsAreCertified(t *testing.T) {
	const shortPin = "phrase_apple_clean_01_blur_soft_reveal"
	const longPin = "phrase_apple_clean_26_opacity_depth_push"
	if !containsMotion(ShortPhraseVisibleMotionCandidates(), shortPin) {
		t.Fatalf("documented short pin %q is not a general short-phrase entrance", shortPin)
	}
	if !containsMotion(LongPhraseMotionCandidates(), longPin) {
		t.Fatalf("documented long pin %q is not a long-phrase motion", longPin)
	}
}

func TestPinnedPhraseMotionsApplyToEveryLaneItem(t *testing.T) {
	shortPin := ShortPhraseVisibleMotionCandidates()[0]
	longPin := distinctLongPhrasePin(shortPin)
	if shortPin == "" || longPin == "" {
		t.Fatal("phrase catalogs must expose a short and a distinct long entrance")
	}
	scenes := phraseMotionPinScenes()
	input := phraseMotionPinInput(scenes)
	input.ShortPhraseMotion = shortPin
	input.LongPhraseMotion = longPin
	input.PhraseMotions = []string{CertifiedPhraseMotions()[0]}

	plan, err := BuildPlan(input, AllCandidatesPlannerConfig(scenes))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	items := PrepareCompiledAnimationLayout(plan.Items, input, 24, 1)
	shortCount, longCount := 0, 0
	for _, item := range items {
		if item.Kind != "text_phrase" {
			continue
		}
		if len(strings.Fields(item.Text)) >= 6 {
			longCount++
			if item.MotionID != longPin {
				t.Fatalf("long phrase motion = %q, want pin %q", item.MotionID, longPin)
			}
			continue
		}
		shortCount++
		if item.MotionID != shortPin {
			t.Fatalf("short phrase motion = %q, want pin %q", item.MotionID, shortPin)
		}
	}
	if shortCount != 2 || longCount != 2 {
		t.Fatalf("pin coverage = short %d long %d, want 2/2", shortCount, longCount)
	}
}

func TestPinnedPhraseMotionsFailClosed(t *testing.T) {
	scenes := phraseMotionPinScenes()
	base := phraseMotionPinInput(scenes)
	base.ShortPhraseMotion = "not_a_motion"
	if _, err := BuildPlan(base, AllCandidatesPlannerConfig(scenes)); err == nil {
		t.Fatal("unknown short pin must fail closed")
	}

	base = phraseMotionPinInput(scenes)
	base.LongPhraseMotion = "not_a_motion"
	if _, err := BuildPlan(base, AllCandidatesPlannerConfig(scenes)); err == nil {
		t.Fatal("unknown long pin must fail closed")
	}

	recipes := ShortPhraseMotionsForWordCount(3)
	if len(recipes) == 0 {
		t.Fatal("short-phrase recipe catalog is empty")
	}
	base = phraseMotionPinInput(scenes)
	base.ShortPhraseMotion = recipes[0]
	if _, err := BuildPlan(base, AllCandidatesPlannerConfig(scenes)); err == nil {
		t.Fatal("a word-count-specific short recipe must not be pinned across a mixed short lane")
	}

	uniform := []SceneInput{{ID: "uniform-short", Phrases: []TimedAnnotation{
		{Text: "uno due tre", StartMs: 0, EndMs: 1500, Score: 0.99},
		{Text: "quattro cinque sei", StartMs: 1600, EndMs: 3100, Score: 0.98},
	}}}
	base = phraseMotionPinInput(uniform)
	base.ShortPhraseMotion = recipes[0]
	plan, err := BuildPlan(base, AllCandidatesPlannerConfig(uniform))
	if err != nil {
		t.Fatalf("uniform word-count short recipe pin: %v", err)
	}
	for _, item := range plan.Items {
		if item.Kind == "text_phrase" && item.MotionID != recipes[0] {
			t.Fatalf("uniform short recipe pin = %q, want %q", item.MotionID, recipes[0])
		}
	}
}

func TestPinnedPhraseMotionsSurviveDuplicateRotation(t *testing.T) {
	shortPin := ShortPhraseVisibleMotionCandidates()[0]
	longPin := distinctLongPhrasePin(shortPin)
	items := []capmodel.OverlayItem{
		{ID: "s1", Kind: "text_phrase", Text: "tre parole molto brevi", MotionID: shortPin},
		{ID: "s2", Kind: "text_phrase", Text: "un'altra frase breve qui", MotionID: shortPin},
		{ID: "l1", Kind: "text_phrase", Text: "questa è una frase lunga che supera le sei parole", MotionID: longPin},
		{ID: "l2", Kind: "text_phrase", Text: "un'altra frase lunga con molte parole per il test", MotionID: longPin},
	}
	ApplyAnimationConsistency(items, PlanInput{
		PlanID: "pin-layout", VideoID: "pin-layout",
		ShortPhraseMotion: shortPin, LongPhraseMotion: longPin,
		PhraseMotions: []string{CertifiedPhraseMotions()[0]},
	})
	for i, item := range items {
		want := shortPin
		if len(strings.Fields(item.Text)) >= 6 {
			want = longPin
		}
		if item.MotionID != want {
			t.Fatalf("item %d motion = %q, want pin %q", i, item.MotionID, want)
		}
	}
}
