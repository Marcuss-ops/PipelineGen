package script

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestSegmentSemanticProfile_JSONRoundTrip certifies the canonical
// SegmentSemanticProfile contract: every section serializes with its
// canonical key and survives a marshal/unmarshal round-trip without
// losing fields.
func TestSegmentSemanticProfile_JSONRoundTrip(t *testing.T) {
	original := SegmentSemanticProfile{
		SegmentID:                 "segment-002",
		TextHash:                  "abc123",
		UnderstandingModelVersion: "gemma3:1b",
		PromptVersion:             "segment_semantics_v1",
		Topic:                     "origine dei primi trattori",
		Subtopics:                 []string{"macchine agricole", "motori a vapore"},
		Keywords: []WeightedKeyword{
			{Value: "primi trattori", Confidence: 0.92},
			{Value: "meccanizzazione", Confidence: 0.74},
		},
		VisualTerms: []WeightedKeyword{
			{Value: "early tractor", Confidence: 0.88},
		},
		Terms: []SemanticTerm{
			{Value: "tractor", Kind: TermKindSubject, Score: 0.95},
			{Value: "agriculture", Kind: TermKindContext, Score: 0.8},
			{Value: "vintage tractor", Kind: TermKindVisual, Score: 0.85},
			{Value: "1892", Kind: TermKindTemporal, Score: 0.9},
			{Value: "plowing", Kind: TermKindAction, Score: 0.7},
			{Value: "gasoline engine", Kind: TermKindTechnology, Score: 0.86},
		},
		ImportantPhrases: []string{"John Froelich early gasoline tractor"},
		Entities: []ExtractedEntity{
			{Value: "John Froelich", Type: "PERSON", Confidence: 0.99},
			{Value: "Iowa", Type: "PLACE", Confidence: 0.98},
			{Value: "1892", Type: "DATE", Confidence: 0.97},
		},
		Retrieval: &RetrievalIntent{
			YouTube: []string{"John Froelich first gasoline tractor 1892"},
			Artlist: []string{"vintage tractor farm field"},
			Images:  []string{"John Froelich 1892 gasoline tractor"},
		},
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	wire := string(raw)
	for _, key := range []string{
		`"segment_id"`, `"text_hash"`, `"understanding_model_version"`,
		`"prompt_version"`, `"topic"`, `"subtopics"`, `"keywords"`,
		`"visual_terms"`, `"terms"`, `"important_phrases"`, `"entities"`,
		`"retrieval"`, `"youtube"`, `"artlist"`, `"images"`,
		`"confidence"`, `"kind"`,
	} {
		if !strings.Contains(wire, key) {
			t.Fatalf("expected profile JSON key %s: %s", key, wire)
		}
	}
	if strings.Contains(wire, `"retrieval":{}`) {
		t.Fatalf("populated retrieval must not serialize as empty object: %s", wire)
	}

	var decoded SegmentSemanticProfile
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal profile: %v\nJSON: %s", err, raw)
	}
	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round-trip mismatch:\noriginal=%#v\ndecoded=%#v\njson=%s", original, decoded, raw)
	}
}

func TestSegmentSemanticProfile_ValidateRejectsInvalidConfidence(t *testing.T) {
	profile := SegmentSemanticProfile{
		SegmentID: "segment-001",
		TextHash:  "hash-1",
		Keywords:  []WeightedKeyword{{Value: "tractor", Confidence: 1.1}},
	}
	if err := profile.Validate(); err == nil {
		t.Fatal("expected invalid confidence error")
	}

	profile.Keywords[0].Confidence = 0.8
	if err := profile.Validate(); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}
}

func TestSegmentSemanticProfile_CloneIsIndependent(t *testing.T) {
	original := SegmentSemanticProfile{
		SegmentID: "segment-001", TextHash: "hash-1",
		Subtopics: []string{"farming"},
		Keywords:  []WeightedKeyword{{Value: "tractor", Confidence: 0.9}},
		Retrieval: &RetrievalIntent{YouTube: []string{"tractor history"}},
	}
	clone := original.Clone()
	clone.Subtopics[0] = "changed"
	clone.Keywords[0].Value = "other"
	clone.Retrieval.YouTube[0] = "other query"
	if original.Subtopics[0] != "farming" || original.Keywords[0].Value != "tractor" || original.Retrieval.YouTube[0] != "tractor history" {
		t.Fatal("clone mutation changed original profile")
	}
}

// TestSegmentSemanticProfile_EmptySectionsStayOmitted certifies the wire
// contract stays compact: unset optional sections (retrieval, terms,
// entities) are omitted rather than emitted as empty blocks.
func TestSegmentSemanticProfile_EmptySectionsStayOmitted(t *testing.T) {
	profile := SegmentSemanticProfile{SegmentID: "segment-001", TextHash: "h"}

	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	wire := string(raw)
	for _, key := range []string{`"retrieval"`, `"terms"`, `"entities"`, `"keywords"`} {
		if strings.Contains(wire, key) {
			t.Fatalf("empty section %s must be omitted: %s", key, wire)
		}
	}
}

// TestBuildSegmentSemanticProfile_MapsExtractionBuckets certifies the single
// canonical derivation point: EntityResult buckets become the profile's
// ExtractedEntity stream with the legacy kind defaults (PERSON / LOCATION /
// CONCEPT) when the extractor left the type empty.
func TestBuildSegmentSemanticProfile_MapsExtractionBuckets(t *testing.T) {
	profile := BuildSegmentSemanticProfile(
		CanonicalSegment{ID: "segment-002", TextHash: "hash-2"},
		EntityResult{
			Persons:  []Entity{{Value: " John Froelich ", Type: "PERSON", Score: 0.98}},
			Places:   []Entity{{Value: "Iowa", Type: "", Score: 0.97}},
			Concepts: []Entity{{Value: "tractor", Type: "KEYWORD", Score: 0.9}, {Value: "gasoline engine", Type: "", Score: 0.85}},
		},
		"gemma3:1b", "segment_semantics_v1",
	)
	if profile.SegmentID != "segment-002" || profile.TextHash != "hash-2" {
		t.Fatalf("identity = %q/%q", profile.SegmentID, profile.TextHash)
	}
	if profile.UnderstandingModelVersion != "gemma3:1b" || profile.PromptVersion != "segment_semantics_v1" {
		t.Fatalf("versions = %q/%q", profile.UnderstandingModelVersion, profile.PromptVersion)
	}
	// Score is float32 on the extraction surface; the profile widens it to
	// float64 with the same conversion the legacy projection used, so the
	// expected values go through the identical widening.
	f32 := func(v float32) float64 { return float64(v) }
	want := []ExtractedEntity{
		{Value: "John Froelich", Type: "PERSON", Confidence: f32(0.98)},
		{Value: "Iowa", Type: "LOCATION", Confidence: f32(0.97)},
		{Value: "tractor", Type: "KEYWORD", Confidence: f32(0.9)},
		{Value: "gasoline engine", Type: "CONCEPT", Confidence: f32(0.85)},
	}
	if !reflect.DeepEqual(profile.Entities, want) {
		t.Fatalf("entities = %#v, want %#v", profile.Entities, want)
	}
}

// TestBuildSegmentSemanticProfile_WeightsKeywordsByOrder certifies that the
// extractor's importance order becomes a deterministic descending confidence:
// the first ImportantWord/ArtlistPhrase carries the highest score, exactly
// like the scene-annotation important-word projection.
func TestBuildSegmentSemanticProfile_WeightsKeywordsByOrder(t *testing.T) {
	profile := BuildSegmentSemanticProfile(
		CanonicalSegment{ID: "segment-002", TextHash: "hash-2"},
		EntityResult{
			ImportantWords:   []string{"tractor", "agriculture", "steam engine"},
			ArtlistPhrases:   []string{"horse drawn farming", "vintage tractor field"},
			ImportantPhrases: []string{"John Froelich early gasoline tractor"},
		},
		"", "",
	)
	if len(profile.Keywords) != 3 {
		t.Fatalf("keywords = %v, want 3", profile.Keywords)
	}
	if profile.Keywords[0].Value != "tractor" || profile.Keywords[0].Confidence != 1.0 {
		t.Fatalf("first keyword = %#v, want tractor/1.0", profile.Keywords[0])
	}
	if profile.Keywords[2].Value != "steam engine" || profile.Keywords[2].Confidence != 1.0/3.0 {
		t.Fatalf("last keyword = %#v, want steam engine/%.4f", profile.Keywords[2], 1.0/3.0)
	}
	if len(profile.VisualTerms) != 2 || profile.VisualTerms[0].Value != "horse drawn farming" || profile.VisualTerms[0].Confidence != 1.0 {
		t.Fatalf("visual terms = %#v, want ordered weighted artlist phrases", profile.VisualTerms)
	}
	if !reflect.DeepEqual(profile.ImportantPhrases, []string{"John Froelich early gasoline tractor"}) {
		t.Fatalf("important phrases = %v", profile.ImportantPhrases)
	}
}

// TestBuildSegmentSemanticProfile_EmptyResultNeverInventsEntities certifies
// the strict authority rule: an extraction result without named entities must
// produce a profile with NO entities — the small LLM never invents them.
func TestBuildSegmentSemanticProfile_EmptyResultNeverInventsEntities(t *testing.T) {
	profile := BuildSegmentSemanticProfile(
		CanonicalSegment{ID: "segment-001", TextHash: "hash-1"},
		EntityResult{Persons: []Entity{}, Places: []Entity{}, Concepts: []Entity{}},
		"", "",
	)
	if len(profile.Entities) != 0 || profile.Entities != nil {
		t.Fatalf("entities = %#v, want nil", profile.Entities)
	}
	if len(profile.Keywords) != 0 || len(profile.VisualTerms) != 0 {
		t.Fatalf("empty extraction must not invent keywords/visual terms: %#v %#v", profile.Keywords, profile.VisualTerms)
	}
}

// TestTermKindConstantsAreCanonical certifies the closed set of TermKind
// values: subject, context, visual, temporal, action, technology.
func TestBuildSegmentSemanticProfile_PreservesTemporalEntitiesAndClassifiesTerms(t *testing.T) {
	profile := BuildSegmentSemanticProfile(
		CanonicalSegment{ID: "segment-003", TextHash: "hash-3", Text: "In 1892 John Froelich developed a gasoline tractor in Iowa."},
		EntityResult{
			Persons:  []Entity{{Value: "John Froelich", Type: "PERSON", Score: 0.98}},
			Places:   []Entity{{Value: "Iowa", Type: "GPE", Score: 0.97}},
			Concepts: []Entity{{Value: "1892", Type: "DATE", Score: 0.99}},
			ImportantWords: []string{
				"tractor", "agriculture", "plowing",
			},
			ArtlistPhrases: []string{"vintage tractor field"},
		},
		"gemma3:1b", "v1",
	)

	var kinds = map[string]TermKind{}
	for _, term := range profile.Terms {
		kinds[term.Value] = term.Kind
	}
	if kinds["1892"] != TermKindTemporal {
		t.Fatalf("temporal entity kind = %q, want %q; terms=%#v", kinds["1892"], TermKindTemporal, profile.Terms)
	}
	if kinds["tractor"] != TermKindTechnology {
		t.Fatalf("technology keyword kind = %q, want %q", kinds["tractor"], TermKindTechnology)
	}
	if kinds["agriculture"] != TermKindContext {
		t.Fatalf("context keyword kind = %q, want %q", kinds["agriculture"], TermKindContext)
	}
	if kinds["plowing"] != TermKindAction {
		t.Fatalf("action keyword kind = %q, want %q", kinds["plowing"], TermKindAction)
	}
	if kinds["vintage tractor field"] != TermKindVisual {
		t.Fatalf("visual term kind = %q, want %q", kinds["vintage tractor field"], TermKindVisual)
	}
}

func TestTermKindConstantsAreCanonical(t *testing.T) {
	want := map[TermKind]bool{
		TermKindSubject:    true,
		TermKindContext:    true,
		TermKindVisual:     true,
		TermKindTemporal:   true,
		TermKindAction:     true,
		TermKindTechnology: true,
	}
	for _, kind := range []TermKind{
		TermKindSubject, TermKindContext, TermKindVisual,
		TermKindTemporal, TermKindAction, TermKindTechnology,
	} {
		if !want[kind] {
			t.Fatalf("unexpected TermKind %q", kind)
		}
		if string(kind) == "" {
			t.Fatalf("TermKind %q must serialize to a non-empty string", kind)
		}
	}
}
