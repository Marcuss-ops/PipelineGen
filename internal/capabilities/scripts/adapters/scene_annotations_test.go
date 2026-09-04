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

func TestSceneAnnotations_ProductAndLogoSurviveTaxonomy(t *testing.T) {
	// PRODUCT / LOGO must never collapse to CONCEPT: the batch merger keeps
	// them typed, places them in the primary imageable set (the registry's
	// primary/media kinds), and stamps the resolver's canonical id.
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "Vision Pro", Type: "PRODUCT", Confidence: 0.95},
				{Value: "Apple", Type: "LOGO", Confidence: 0.97},
			},
			ImageEntityCanonicalIDs: map[string]string{
				"vision pro": "product:apple-vision-pro",
				"apple":      "logo:apple",
			},
		},
	}
	ann := sceneAnnotations("Apple unveiled the Vision Pro at the event.", "en", seg)
	if ann == nil {
		t.Fatal("annotations must not be nil")
	}
	// The rebase may also discover capitalized names from the text as
	// PERSONs (pre-existing discovery heuristic); what matters is that the
	// segment's PRODUCT and LOGO survive typed and stamped.
	byType := map[string]scriptpkg.AnnotatedEntity{}
	for _, e := range append(ann.PrimaryEntities, ann.SecondaryEntities...) {
		if _, exists := byType[e.Type]; !exists {
			byType[e.Type] = e
		}
	}
	product, ok := byType["PRODUCT"]
	if !ok {
		t.Fatalf("PRODUCT entity missing: %+v", ann.PrimaryEntities)
	}
	if product.CanonicalEntityID != "product:apple-vision-pro" {
		t.Fatalf("PRODUCT canonical id = %q", product.CanonicalEntityID)
	}
	logo, ok := byType["LOGO"]
	if !ok {
		t.Fatalf("LOGO entity missing: %+v", ann.PrimaryEntities)
	}
	if logo.CanonicalEntityID != "logo:apple" {
		t.Fatalf("LOGO canonical id = %q", logo.CanonicalEntityID)
	}
	foundProduct := false
	for _, e := range ann.PrimaryEntities {
		if e.Type == "PRODUCT" {
			foundProduct = true
		}
	}
	if !foundProduct {
		t.Fatal("PRODUCT must land in the primary imageable set, never secondary")
	}
}

func TestSceneAnnotations_StampsResolverCanonicalID(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{{Value: "Muhammad Ali", Type: "PERSON", Confidence: 0.98}},
			ImageEntityCanonicalIDs: map[string]string{
				"muhammad ali": "person:muhammad-ali",
			},
		},
	}
	ann := sceneAnnotations("L’ascesa di Muhammad Ali cambiò il pugilato.", "it", seg)
	if ann == nil || len(ann.PrimaryEntities) != 1 {
		t.Fatalf("annotations = %+v", ann)
	}
	if got := ann.PrimaryEntities[0].CanonicalEntityID; got != "person:muhammad-ali" {
		t.Fatalf("canonical_entity_id = %q, want person:muhammad-ali", got)
	}
}

func TestSceneAnnotations_SkipsKeywordAndVisualSubject(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "Apple", Type: "KEYWORD"},
				{Value: "Apple", Type: "VISUAL_SUBJECT"},
			},
		},
	}
	ann := sceneAnnotations("Apple changed everything.", "en", seg)
	// The rebase may still synthesize a fallback important phrase from the
	// text; the contract under test is that KEYWORD / VISUAL_SUBJECT never
	// become annotation entities.
	if ann != nil {
		if len(ann.PrimaryEntities)+len(ann.SecondaryEntities) != 0 {
			t.Fatalf("KEYWORD/VISUAL_SUBJECT became entities: %+v", ann)
		}
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
