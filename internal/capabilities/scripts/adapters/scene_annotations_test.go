package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSceneAnnotationsOnePhraseAndRuneOffsets(t *testing.T) {
	text := "L’ascesa di Muhammad Ali cambiò il pugilato."
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights: scriptpkg.SegmentInsights{
			ImportantPhrases: []string{"L’ascesa di Muhammad Ali cambiò il pugilato."},
			ImportantWords:   []string{"ascesa", "pugilato"},
			Entities:         []scriptpkg.ExtractedEntity{{Value: "Muhammad Ali", Type: "PERSON", Confidence: .98}},
		},
	}
	ann := sceneAnnotations(text, "it", seg)
	if ann.Version != 1 || ann.Language != "it" {
		t.Fatalf("header = %+v", ann)
	}
	if len(ann.ImportantPhrases) != 1 {
		t.Fatalf("phrases = %+v", ann.ImportantPhrases)
	}
	entity := ann.PrimaryEntities[0]
	span := entity.Mentions[0]
	if got := []rune(text)[span.StartRune:span.EndRune]; string(got) != "Muhammad Ali" {
		t.Fatalf("rune span = %q, want Muhammad Ali", string(got))
	}
}

func TestSceneAnnotationsDropsMissingPhrase(t *testing.T) {
	ann := sceneAnnotations("Mike Tyson allenava potenza.", "it", scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights:  scriptpkg.SegmentInsights{ImportantPhrases: []string{"Muhammad Ali vinse tutto"}},
	})
	if len(ann.ImportantPhrases) != 1 || ann.ImportantPhrases[0].Text != "Mike Tyson allenava potenza." {
		t.Fatalf("final-text phrase was not selected: %+v", ann.ImportantPhrases)
	}
}

func TestRebaseSceneAnnotationsGroundsAndDeduplicates(t *testing.T) {
	ann := &scriptpkg.SceneAnnotations{
		ImportantPhrases: []scriptpkg.AnnotationSpan{{Text: "Potenza esplosiva"}},
		PrimaryEntities:  []scriptpkg.AnnotatedEntity{{Text: "Mike Tyson", Type: "PERSON"}},
		ImportantWords:   []scriptpkg.AnnotationSpan{{Text: "esplosiva"}, {Text: "velocità"}},
	}
	got := rebaseSceneAnnotations(ann, "Mike Tyson mostrava una potenza esplosiva e una velocità sorprendente.")
	if len(got.ImportantPhrases) != 1 || got.ImportantPhrases[0].StartRune != 24 {
		t.Fatalf("phrase was not rebased: %+v", got.ImportantPhrases)
	}
	if len(got.PrimaryEntities) != 1 || got.PrimaryEntities[0].Mentions[0].StartRune != 0 {
		t.Fatalf("entity was not rebased: %+v", got.PrimaryEntities)
	}
	if len(got.ImportantWords) != 1 || got.ImportantWords[0].Text != "velocità" {
		t.Fatalf("overlapping keyword was not removed: %+v", got.ImportantWords)
	}
}
