package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestRebaseDoesNotReextractEntitiesFromText(t *testing.T) {
	ann := rebaseSceneAnnotations(&scriptpkg.SceneAnnotations{}, "NASA e ESA hanno annunciato un progetto a Roma.")
	if len(ann.PrimaryEntities)+len(ann.SecondaryEntities) != 0 {
		t.Fatalf("rebasing synthesized entities outside BrainRust: %+v", ann)
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
