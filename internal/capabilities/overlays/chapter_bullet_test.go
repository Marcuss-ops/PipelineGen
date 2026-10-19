package overlays

import (
	"strings"
	"testing"
)

func chapterTestPhrase(text string) TimedAnnotation {
	return TimedAnnotation{
		Text: text, StartMs: 0, EndMs: 4000, StartUS: 0, DurationUS: 4_000_000,
		Score: 0.98, PresentationRole: ChapterBulletRole,
	}
}

func in3DPool(id string) bool {
	for _, candidate := range text3DMotionCandidates {
		if candidate == id {
			return true
		}
	}
	return false
}

func TestChapterBulletGets3DMotion(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "chapter-3d", VideoID: "video-chapter-3d",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "s1", Phrases: []TimedAnnotation{
			chapterTestPhrase("Il caso riguarda la morte di Chiara Poggi a Garlasco nel duemilasette"),
		}}},
	}, PlannerConfig{MaxPhrases: 3})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("plan items = %d, want 1", len(plan.Items))
	}
	item := plan.Items[0]
	if item.Params["editorial_role"] != ChapterBulletRole {
		t.Fatalf("editorial_role = %v, want chapter_bullet", item.Params["editorial_role"])
	}
	if !in3DPool(item.MotionID) {
		t.Fatalf("chapter motion = %q, want text_3d family %v", item.MotionID, text3DMotionCandidates)
	}
	if item.Params["position"] != "center" {
		t.Fatalf("position = %v, want center", item.Params["position"])
	}
}

func TestChapterBulletsRotateDistinct3DMotions(t *testing.T) {
	scenes := make([]SceneInput, 0, 3)
	for i, text := range []string{
		"Prima scena del capitolo con testo lungo abbastanza per il conteggio",
		"Seconda scena del capitolo con testo lungo abbastanza per il conteggio",
		"Terza scena del capitolo con testo lungo abbastanza per il conteggio",
	} {
		scenes = append(scenes, SceneInput{ID: string(rune('a' + i)), Phrases: []TimedAnnotation{chapterTestPhrase(text)}})
	}
	plan, err := BuildPlan(PlanInput{
		PlanID: "chapter-rotate", VideoID: "video-rotate",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Scenes: scenes,
	}, PlannerConfig{MaxPhrases: 3})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	seen := map[string]struct{}{}
	for _, item := range plan.Items {
		if item.Params["editorial_role"] != ChapterBulletRole {
			continue
		}
		if !in3DPool(item.MotionID) {
			t.Fatalf("chapter motion = %q, want text_3d family", item.MotionID)
		}
		if _, dup := seen[item.MotionID]; dup {
			t.Fatalf("duplicate chapter motion %q: chapters must not share entrances", item.MotionID)
		}
		seen[item.MotionID] = struct{}{}
	}
	if len(seen) != 3 {
		t.Fatalf("chapter motions = %d, want 3 distinct", len(seen))
	}
}

func TestChapterBulletShortCopyStaysIn3DFamily(t *testing.T) {
	// Four words would take the short-phrase catalog as an ordinary phrase;
	// as a chapter card it must keep the 3D family and the long section.
	short := chapterTestPhrase("Quattro parole capitolo qui")
	item := OverlayItem{Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: short.Text,
		Params: map[string]any{"editorial_role": ChapterBulletRole}}
	if got := EditorialSectionForItem(item); got != EditorialSectionImportantPhrase {
		t.Fatalf("chapter section = %v, want ImportantPhrase", got)
	}
	candidates := phraseMotionCandidatesForItem(item, PlanInput{}, "seed", 0, 0)
	if len(candidates) == 0 {
		t.Fatal("no chapter candidates")
	}
	for _, id := range candidates {
		if !in3DPool(id) {
			t.Fatalf("chapter candidate %q escapes the 3D family", id)
		}
		if strings.Contains(id, "short") || IsShortPhraseRecipe(id) {
			t.Fatalf("chapter candidate %q leaks the short catalog", id)
		}
	}
}

func TestChapterBulletPriorityOverSceneBullet(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "chapter-prio", VideoID: "video-prio",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "s1", Phrases: []TimedAnnotation{
			{Text: "Ordinary extracted highlight phrase here", StartMs: 0, EndMs: 2000, StartUS: 0, DurationUS: 2_000_000, Score: 0.5},
			chapterTestPhrase("Chapter opening sentence with enough words inside"),
		}}},
	}, PlannerConfig{MaxPhrases: 1})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("plan items = %d, want 1", len(plan.Items))
	}
	if plan.Items[0].Params["editorial_role"] != ChapterBulletRole {
		t.Fatalf("reserved slot = %v, want chapter_bullet", plan.Items[0].Params["editorial_role"])
	}
}
