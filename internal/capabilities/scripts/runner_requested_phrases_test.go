package scriptgeneration

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestComputeSegmentEntityAnnotationsPrioritizesRequestedExactPhrases(t *testing.T) {
	text := "A investigação continua aberta. a defesa nega responsabilidade pela morte."
	snapshot := []sceneTextSnapshot{{ID: "scene-1", Index: 0, Text: text}}
	segments := []scriptpkg.VidRushSegmentResult{{
		SceneID:  "scene-1",
		Insights: scriptpkg.SegmentInsights{ImportantPhrases: []string{"A investigação continua aberta."}},
	}}
	requested := []string{"A investigação continua aberta.", "A defesa nega responsabilidade pela morte."}
	annotations := computeSegmentEntityAnnotations(snapshot, "pt", segments, 1, true, requested)
	got := annotations[0].ImportantPhrases
	if len(got) != 2 {
		t.Fatalf("important phrases = %+v, want both caller-requested phrases despite per-scene NLP limit 1", got)
	}
	for _, phrase := range got {
		if phrase.Score != 1 || phrase.Kind != "key_statement" {
			t.Errorf("requested phrase not prioritized: %+v", phrase)
		}
	}
	if got[1].Text != requested[1] {
		t.Fatalf("phrase surface = %q, want caller's exact spelling %q", got[1].Text, requested[1])
	}
}
