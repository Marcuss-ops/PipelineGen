package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestClassifyDiscoveredEntityPortuguesePlacesAndOperations(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"São Paulo", "GPE"},
		{"Brasília", "GPE"},
		{"Operação Vectura Corrupta", "EVENT"},
		{"Operacao Decurio", "EVENT"},
		{"Milton Leite", "PERSON"},
	} {
		if got := classifyDiscoveredEntity(tc.name); got != tc.want {
			t.Errorf("classifyDiscoveredEntity(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSceneAnnotationsCorrectsKnownPortugueseEntityTypesFromNLP(t *testing.T) {
	text := "São Paulo recebeu a Operação Vectura Corrupta."
	ann := sceneAnnotations(text, "pt", scriptpkg.VidRushSegmentResult{SegmentID: "scene-1", Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
		{Value: "São Paulo", Type: "PERSON", Confidence: .9},
		{Value: "Operação Vectura Corrupta", Type: "PERSON", Confidence: .9},
	}}})
	kinds := map[string]string{}
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
		kinds[entity.CanonicalName] = entity.Type
	}
	if kinds["São Paulo"] != "GPE" || kinds["Operação Vectura Corrupta"] != "EVENT" {
		t.Fatalf("corrected NLP entity types = %#v", kinds)
	}
}

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

func TestSceneAnnotations_BrandUsesTextFallbackUntilVerifiedAsset(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "brand-scene",
		Insights:  scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{{Value: "OpenAI", Type: "BRAND", Confidence: 0.94}}},
	}
	withoutAsset := sceneAnnotations("OpenAI announced the research result.", "en", segment)
	if withoutAsset == nil || len(withoutAsset.PrimaryEntities) != 0 || len(withoutAsset.SecondaryEntities) != 1 {
		t.Fatalf("unverified brand classification = %+v, want secondary text fallback", withoutAsset)
	}
	if got := withoutAsset.SecondaryEntities[0].Type; got != "BRAND" {
		t.Fatalf("unverified brand type = %q, want BRAND", got)
	}

	segment.Assets.Candidates = []scriptpkg.SegmentAssetCandidate{{
		AssetID: "openai-logo", Provider: scriptpkg.VidRushProviderInternetImages,
		Entity: "OpenAI", DriveLink: "https://drive.google.com/file/d/logo/view", LegacyFileMD5: "logo-md5",
		AcquisitionStatus:  scriptpkg.VidRushStatusAcquired,
		VerificationStatus: scriptpkg.VidRushStatusVerified,
		PersistenceStatus:  scriptpkg.VidRushStatusPersisted,
		RightsStatus:       "unknown_allowed",
	}}
	withAsset := sceneAnnotations("OpenAI announced the research result.", "en", segment)
	if withAsset == nil || len(withAsset.PrimaryEntities) != 1 || withAsset.PrimaryEntities[0].Type != "LOGO" {
		t.Fatalf("verified brand classification = %+v, want primary LOGO", withAsset)
	}
	if withAsset.PrimaryEntities[0].CanonicalEntityID != "logo:openai" {
		t.Fatalf("verified logo id = %q", withAsset.PrimaryEntities[0].CanonicalEntityID)
	}
}

func TestSceneAnnotations_ProductAndLogoSurviveTaxonomy(t *testing.T) {
	// PRODUCT / LOGO must never collapse to CONCEPT: the batch merger keeps
	// them typed, places products in the primary imageable set, and stamps the
	// resolver's canonical id. An unverified logo is intentionally a BRAND text fallback.
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
	brand, ok := byType["BRAND"]
	if !ok {
		t.Fatalf("BRAND text fallback missing: %+v", ann.SecondaryEntities)
	}
	if brand.CanonicalEntityID != "logo:apple" {
		t.Fatalf("brand canonical id = %q", brand.CanonicalEntityID)
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
