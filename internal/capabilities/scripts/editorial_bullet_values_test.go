package scriptgeneration

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type stubSemanticAnalyzer struct {
	bullets []BulletCandidate
	err     error
}

func (s stubSemanticAnalyzer) SemanticCandidates(context.Context, string, string, int) (*SemanticCandidateBundle, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &SemanticCandidateBundle{Bullets: s.bullets}, nil
}

func TestAttachVisualSemanticsPreservesPhraseImpactEditorialBullet(t *testing.T) {
	r := &Runner{}
	r.SetSceneSemanticAnalyzer(stubSemanticAnalyzer{bullets: []BulletCandidate{
		{Text: "Marco Rossi founded the studio.", SentenceIndex: 0, Start: 0, End: 32},
		{Text: "Revenue grew 25% in 2024.", SentenceIndex: 1, Start: 33, End: 58},
	}})
	text := "Marco Rossi founded the studio. Revenue grew 25% in 2024."
	inputs := []scriptpkg.SceneAnalysisInput{{SceneID: "s1", Text: text}}
	highlights := []scriptpkg.SceneHighlight{{
		SceneID:         "s1",
		Bullets:         []scriptpkg.SceneBulletSpan{{SentenceStart: 1, SentenceEnd: 2, Text: "Revenue grew 25% in 2024."}},
		GloballyIndexed: true,
	}}
	out := r.attachVisualSemantics(context.Background(), "en", inputs, highlights, false)
	if len(out) != 1 || len(out[0].Bullets) != 1 {
		t.Fatalf("expected exactly one bullet, got %+v", out)
	}
	if out[0].Bullets[0].Text != "Revenue grew 25% in 2024." {
		t.Fatalf("expected Phrase Impact's selected bullet, got %q", out[0].Bullets[0].Text)
	}
	if !out[0].GloballyIndexed {
		t.Fatalf("preserving the Phrase Impact bullet must preserve its global indexing")
	}
}

func TestAttachVisualSemanticsKeepsExistingWhenNoCandidate(t *testing.T) {
	r := &Runner{}
	r.SetSceneSemanticAnalyzer(stubSemanticAnalyzer{})
	inputs := []scriptpkg.SceneAnalysisInput{{SceneID: "s1", Text: "Hello world. Second sentence."}}
	existing := []scriptpkg.SceneBulletSpan{{SentenceStart: 0, SentenceEnd: 1, Text: "Hello world."}}
	highlights := []scriptpkg.SceneHighlight{{SceneID: "s1", Bullets: existing}}
	out := r.attachVisualSemantics(context.Background(), "en", inputs, highlights, false)
	if len(out[0].Bullets) != 1 || out[0].Bullets[0].Text != "Hello world." {
		t.Fatalf("expected existing bullets kept, got %+v", out[0].Bullets)
	}
}
