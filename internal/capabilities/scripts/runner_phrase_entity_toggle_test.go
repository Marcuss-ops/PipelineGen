package scriptgeneration

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Entity-free catalog runs set extract_entities=disabled to skip NER, but
// their operator-supplied phrase hints must still reach the overlay plan:
// important phrases ground by verbatim substring + certified timing, never
// by entities. Only entity/segment paths stay behind the toggle.
func TestComputeSegmentEntityAnnotationsPhrasesSurviveDisabledEntities(t *testing.T) {
	text := "La giovane vittima viene ritrovata senza vita nella sua abitazione in un pomeriggio d'estate."
	snapshot := []sceneTextSnapshot{{
		ID: "scene-1", Index: 0, Text: text,
		ExtractEntities: scriptpkg.ToggleDisabled,
	}}
	requested := []string{text}
	annotations := computeSegmentEntityAnnotations(snapshot, "it", nil, 1, true, requested)
	ann := annotations[0]
	if ann == nil {
		t.Fatal("disabled entity extraction dropped the phrase annotation surface")
	}
	if len(ann.ImportantPhrases) != 1 || ann.ImportantPhrases[0].Text != text {
		t.Fatalf("important phrases = %+v, want the single requested hint verbatim", ann.ImportantPhrases)
	}
	if len(ann.PrimaryEntities)+len(ann.SecondaryEntities) != 0 {
		t.Fatalf("entities leaked despite disabled toggle: %+v", ann)
	}
}

// The projection must strip the entity surface on disabled scenes while
// keeping the grounded phrases: nil-ing the whole annotation would drop
// catalog phrases that need no NER.
func TestApplyPrepareProjectionsKeepsPhrasesOnDisabledScenes(t *testing.T) {
	text := "La giovane vittima viene ritrovata senza vita nella sua abitazione in un pomeriggio d'estate."
	result := &GenerateResult{Scenes: []Scene{{ID: "scene-1", Index: 0}}}
	prepared := vidRushPrepareResult{
		annotations: map[int]*scriptpkg.SceneAnnotations{0: {
			Version: 1, Language: "it", Status: "completed",
			ImportantPhrases: []scriptpkg.AnnotationSpan{{Text: text, Score: 1, Kind: "key_statement"}},
			PrimaryEntities:  []scriptpkg.AnnotatedEntity{{Text: "vittima"}},
		}},
		disabledScenes: map[int]bool{0: true},
	}
	applyVidRushPrepareProjections(result, prepared)
	ann := result.Scenes[0].Annotations
	if ann == nil {
		t.Fatal("disabled scene lost its phrase annotations")
	}
	if len(ann.ImportantPhrases) != 1 || ann.ImportantPhrases[0].Text != text {
		t.Fatalf("important phrases = %+v, want the grounded phrase kept", ann.ImportantPhrases)
	}
	if len(ann.PrimaryEntities)+len(ann.SecondaryEntities) != 0 {
		t.Fatalf("entity surface survived the disabled toggle: %+v", ann)
	}
	if result.Scenes[0].EntityOverlayRequired {
		t.Fatal("EntityOverlayRequired must stay false on disabled scenes")
	}
}
