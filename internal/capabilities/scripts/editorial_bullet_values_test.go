package scriptgeneration

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type stubBulletAnalyzer struct {
	candidates []BulletCandidate
	err        error
}

func (s stubBulletAnalyzer) BulletCandidates(context.Context, string, string, int) ([]BulletCandidate, error) {
	return s.candidates, s.err
}

func TestSelectVisualBulletSingleSentence(t *testing.T) {
	text := "Marco Rossi founded the studio. Revenue grew 25% in 2024."
	candidates := []BulletCandidate{{Text: "Marco Rossi founded the studio.", SentenceIndex: 0, Start: 0, End: 32}}
	span, ok := selectVisualBullet(candidates, text)
	if !ok || len(span) != 1 {
		t.Fatalf("expected one bullet, got %v ok=%v", span, ok)
	}
	if span[0].SentenceStart != 0 || span[0].SentenceEnd != 1 {
		t.Fatalf("unexpected span %+v", span[0])
	}
	editorialSpan := scriptpkg.EditorialSceneBullet{SentenceStart: span[0].SentenceStart, SentenceEnd: span[0].SentenceEnd, Text: span[0].Text}
	scene := scriptpkg.EditorialScene{SceneID: "s", TitleStatus: "unavailable", Bullets: []scriptpkg.EditorialSceneBullet{editorialSpan}, GloballyIndexed: false}
	if !validEditorialBullet(scene, editorialSpan, text, text) {
		t.Fatalf("mapped bullet fails validEditorialBullet")
	}
}

func TestSelectVisualBulletSkipsDecimalSplitDrift(t *testing.T) {
	// Rust keeps "3.5" in one sentence; the Go splitter breaks on every '.',
	// so a 3.5 bullet must not map. The second candidate must win.
	text := "Value 3.5 percent today. Marco Rossi spoke."
	candidates := []BulletCandidate{
		{Text: "Value 3.5 percent today.", SentenceIndex: 0, Start: 0, End: 24},
		{Text: "Marco Rossi spoke.", SentenceIndex: 1, Start: 25, End: 43},
	}
	span, ok := selectVisualBullet(candidates, text)
	if !ok || len(span) != 1 {
		t.Fatalf("expected fallback bullet, got %v ok=%v", span, ok)
	}
	if span[0].Text != "Marco Rossi spoke." {
		t.Fatalf("expected second candidate, got %q", span[0].Text)
	}
}

func TestAttachVisualBulletsKeepsOnePerScene(t *testing.T) {
	r := &Runner{}
	r.SetSceneBulletAnalyzer(stubBulletAnalyzer{candidates: []BulletCandidate{
		{Text: "Marco Rossi founded the studio.", SentenceIndex: 0, Start: 0, End: 32},
		{Text: "Revenue grew 25% in 2024.", SentenceIndex: 1, Start: 33, End: 58},
	}})
	text := "Marco Rossi founded the studio. Revenue grew 25% in 2024."
	inputs := []scriptpkg.SceneAnalysisInput{{SceneID: "s1", Text: text}}
	highlights := []scriptpkg.SceneHighlight{{
		SceneID: "s1",
		Bullets: []scriptpkg.SceneBulletSpan{{SentenceStart: 1, SentenceEnd: 2, Text: "Revenue grew 25% in 2024."}},
	}}
	out := r.attachVisualBullets(context.Background(), "en", inputs, highlights)
	if len(out) != 1 || len(out[0].Bullets) != 1 {
		t.Fatalf("expected exactly one bullet, got %+v", out)
	}
	if out[0].Bullets[0].Text != "Marco Rossi founded the studio." {
		t.Fatalf("expected first VisualNER bullet, got %q", out[0].Bullets[0].Text)
	}
	if out[0].GloballyIndexed {
		t.Fatalf("replaced bullets must be scene-local")
	}
}

func TestAttachVisualBulletsKeepsExistingWhenNoCandidate(t *testing.T) {
	r := &Runner{}
	r.SetSceneBulletAnalyzer(stubBulletAnalyzer{})
	inputs := []scriptpkg.SceneAnalysisInput{{SceneID: "s1", Text: "Hello world. Second sentence."}}
	existing := []scriptpkg.SceneBulletSpan{{SentenceStart: 0, SentenceEnd: 1, Text: "Hello world."}}
	highlights := []scriptpkg.SceneHighlight{{SceneID: "s1", Bullets: existing}}
	out := r.attachVisualBullets(context.Background(), "en", inputs, highlights)
	if len(out[0].Bullets) != 1 || out[0].Bullets[0].Text != "Hello world." {
		t.Fatalf("expected existing bullets kept, got %+v", out[0].Bullets)
	}
}
