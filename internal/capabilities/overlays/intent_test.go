package overlays

import (
	"testing"
)

func TestEntityOverlayPlanner_UsesCanonicalSceneEntity(t *testing.T) {
	registry := NewChrononOverlayRegistry()
	scenes := []SceneEntityInput{
		{
			SceneID:    "scene-01",
			SceneIndex: 0,
			Entities: []EntityOverlayInput{
				{Name: "Margot Robbie", Type: "PERSON", Confidence: 0.98},
				{Name: "Alexander Skarsgard", Type: "PERSON", Confidence: 0.96},
			},
		},
		{
			SceneID:    "scene-02",
			SceneIndex: 1,
			Entities: []EntityOverlayInput{
				{Name: "Warner Bros", Type: "ORG", Confidence: 0.91},
				{Name: "Hollywood", Type: "GPE", Confidence: 0.88},
			},
		},
	}
	intents := PlanOverlayIntents(scenes, registry)
	if len(intents) != 4 {
		t.Fatalf("expected 4 intents, got %d", len(intents))
	}

	// scene-01: two persons
	if intents[0].SceneID != "scene-01" {
		t.Errorf("intent[0] scene_id = %q, want scene-01", intents[0].SceneID)
	}
	if intents[0].Entity.CanonicalName != "Margot Robbie" {
		t.Errorf("intent[0] entity = %q, want Margot Robbie", intents[0].Entity.CanonicalName)
	}
	if intents[0].TemplateID != "person_default" {
		t.Errorf("intent[0] template = %q, want person_default", intents[0].TemplateID)
	}
	if intents[0].Kind != string(KindEntityCard) {
		t.Errorf("intent[0] kind = %q, want %q", intents[0].Kind, KindEntityCard)
	}
	if intents[0].TimingState != TimingStatePending {
		t.Errorf("intent[0] timing_state = %q, want PENDING", intents[0].TimingState)
	}

	// scene-02: org + location
	if intents[2].TemplateID != "org_default" {
		t.Errorf("intent[2] template = %q, want org_default", intents[2].TemplateID)
	}
	if intents[3].TemplateID != "gpe_default" {
		t.Errorf("intent[3] template = %q, want gpe_default", intents[3].TemplateID)
	}
}

func TestEntityOverlayPlanner_DeterministicTemplateResolution(t *testing.T) {
	registry := NewChrononOverlayRegistry()
	scenes := []SceneEntityInput{
		{
			SceneID:    "scene-01",
			SceneIndex: 0,
			Entities: []EntityOverlayInput{
				{Name: "Tom Hanks", Type: "PERSON", Confidence: 0.99},
			},
		},
	}

	intents1 := PlanOverlayIntents(scenes, registry)
	intents2 := PlanOverlayIntents(scenes, registry)

	if len(intents1) != len(intents2) {
		t.Fatalf("determinism broken: %d vs %d intents", len(intents1), len(intents2))
	}
	for i := range intents1 {
		if intents1[i].IntentID != intents2[i].IntentID {
			t.Errorf("intent[%d] id mismatch: %q vs %q", i, intents1[i].IntentID, intents2[i].IntentID)
		}
		if intents1[i].TemplateID != intents2[i].TemplateID {
			t.Errorf("intent[%d] template mismatch: %q vs %q", i, intents1[i].TemplateID, intents2[i].TemplateID)
		}
		if intents1[i].Fingerprint() != intents2[i].Fingerprint() {
			t.Errorf("intent[%d] fingerprint mismatch", i)
		}
	}
}

func TestOverlayPlanner_ProjectsSceneInsightsWithoutEntityDuplication(t *testing.T) {
	intents := PlanOverlayIntents([]SceneEntityInput{{
		SceneID: "scene-01", SceneIndex: 0,
		Entities: []EntityOverlayInput{{Name: "Tim Cook", Type: "PERSON"}},
		Phrases:  []OverlayAnnotationInput{{ID: "phrase-1", Text: "presented Apple's plans"}},
		Words: []OverlayAnnotationInput{
			{ID: "word-1", Text: "Cook"}, // entity name fragment: retained as a distinct word
			{ID: "word-2", Text: "California"},
		},
	}}, NewChrononOverlayRegistry())
	if len(intents) != 4 {
		t.Fatalf("intents = %d, want entity + phrase + 2 words", len(intents))
	}
	if intents[1].Source != IntentSourceImportantPhrase || intents[1].TemplateID != "IMPORTANT_PHRASE" {
		t.Fatalf("phrase intent = %+v", intents[1])
	}
	if intents[2].Source != IntentSourceImportantWord || intents[2].TemplateID != "IMPORTANT_WORD" {
		t.Fatalf("word intent = %+v", intents[2])
	}
	for _, intent := range intents {
		if intent.TimingState != TimingStatePending {
			t.Fatalf("intent %q was timed before canonical timing: %+v", intent.IntentID, intent)
		}
		if err := intent.Validate(); err != nil {
			t.Fatalf("intent %q invalid: %v", intent.IntentID, err)
		}
	}
}

func TestOverlayIntent_PerSceneNotAggregated(t *testing.T) {
	registry := NewChrononOverlayRegistry()
	scenes := []SceneEntityInput{
		{SceneID: "scene-01", SceneIndex: 0, Entities: []EntityOverlayInput{
			{Name: "Alice", Type: "PERSON", Confidence: 0.9},
		}},
		{SceneID: "scene-02", SceneIndex: 1, Entities: []EntityOverlayInput{
			{Name: "Bob", Type: "PERSON", Confidence: 0.9},
		}},
		{SceneID: "scene-03", SceneIndex: 2, Entities: []EntityOverlayInput{
			{Name: "Charlie", Type: "PERSON", Confidence: 0.9},
		}},
	}
	intents := PlanOverlayIntents(scenes, registry)
	if len(intents) != 3 {
		t.Fatalf("expected 3 per-scene intents, got %d", len(intents))
	}
	seen := map[string]bool{}
	for _, intent := range intents {
		if seen[intent.SceneID] {
			t.Errorf("duplicate scene_id %q in intents", intent.SceneID)
		}
		seen[intent.SceneID] = true
	}
}

func TestOverlayIntent_EmptyEntitiesProducesEmpty(t *testing.T) {
	registry := NewChrononOverlayRegistry()
	intents := PlanOverlayIntents(nil, registry)
	if len(intents) != 0 {
		t.Errorf("nil scenes should produce 0 intents, got %d", len(intents))
	}
	intents = PlanOverlayIntents([]SceneEntityInput{
		{SceneID: "scene-01", SceneIndex: 0, Entities: nil},
	}, registry)
	if len(intents) != 0 {
		t.Errorf("empty entities should produce 0 intents, got %d", len(intents))
	}
}

func TestOverlayIntent_NilRegistryProducesEmpty(t *testing.T) {
	scenes := []SceneEntityInput{
		{SceneID: "scene-01", SceneIndex: 0, Entities: []EntityOverlayInput{
			{Name: "Alice", Type: "PERSON"},
		}},
	}
	intents := PlanOverlayIntents(scenes, nil)
	if len(intents) != 0 {
		t.Errorf("nil registry should produce 0 intents, got %d", len(intents))
	}
}

func TestOverlayIntent_UnknownEntityTypeSkipped(t *testing.T) {
	registry := NewChrononOverlayRegistry()
	scenes := []SceneEntityInput{
		{SceneID: "scene-01", SceneIndex: 0, Entities: []EntityOverlayInput{
			{Name: "Alice", Type: "PERSON"},
			{Name: "Mystery", Type: "BANANA"}, // unknown type → should be mapped to concept
		}},
	}
	intents := PlanOverlayIntents(scenes, registry)
	// BANANA → default → KindConcept → "concept_default" (exists in registry)
	if len(intents) != 2 {
		t.Fatalf("expected 2 intents (unknown maps to concept), got %d", len(intents))
	}
	if intents[1].Kind != string(KindConcept) {
		t.Errorf("unknown type kind = %q, want %q", intents[1].Kind, KindConcept)
	}
	if intents[1].TemplateID != "concept_default" {
		t.Errorf("unknown type template = %q, want concept_default", intents[1].TemplateID)
	}
}

func TestOverlayIntent_ValidatePass(t *testing.T) {
	intent := OverlayIntent{
		Version:     OverlayIntentVersion,
		IntentID:    "intent-scene-01-tom-hanks",
		SceneID:     "scene-01",
		SceneIndex:  0,
		Entity:      EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"},
		Kind:        string(KindEntityCard),
		TemplateID:  "person_default",
		Payload:     IntentPayload{Name: "Tom Hanks"},
		TimingState: TimingStatePending,
	}
	if err := intent.Validate(); err != nil {
		t.Errorf("valid intent should pass validation: %v", err)
	}
}

func TestOverlayIntent_ValidateRejectsEmptyID(t *testing.T) {
	intent := OverlayIntent{
		Version:     OverlayIntentVersion,
		IntentID:    "",
		SceneID:     "scene-01",
		Entity:      EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"},
		TemplateID:  "person_default",
		TimingState: TimingStatePending,
	}
	if err := intent.Validate(); err == nil {
		t.Error("empty intent_id should fail validation")
	}
}

func TestOverlayIntent_ValidateRejectsInvalidTimingState(t *testing.T) {
	intent := OverlayIntent{
		Version:     OverlayIntentVersion,
		IntentID:    "intent-001",
		SceneID:     "scene-01",
		Entity:      EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"},
		TemplateID:  "person_default",
		TimingState: TimingState("INVALID"),
	}
	if err := intent.Validate(); err == nil {
		t.Error("invalid timing_state should fail validation")
	}
}

func TestOverlayIntent_FingerprintDeterministic(t *testing.T) {
	a := OverlayIntent{
		Version:    OverlayIntentVersion,
		IntentID:   "intent-001",
		SceneID:    "scene-01",
		SceneIndex: 0,
		Entity:     EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"},
		TemplateID: "person_default",
		Payload:    IntentPayload{Name: "Tom Hanks"},
	}
	b := a
	b.TimingState = TimingStateFrozen // timing state excluded from fingerprint

	if a.Fingerprint() != b.Fingerprint() {
		t.Error("fingerprints should match regardless of timing_state")
	}
}

func TestOverlayIntent_SortedDeterministic(t *testing.T) {
	intents := []OverlayIntent{
		{IntentID: "intent-b", SceneIndex: 1},
		{IntentID: "intent-a", SceneIndex: 0},
		{IntentID: "intent-c", SceneIndex: 0},
	}
	SortIntents(intents)
	if intents[0].IntentID != "intent-a" {
		t.Errorf("sort[0] = %q, want intent-a", intents[0].IntentID)
	}
	if intents[1].IntentID != "intent-c" {
		t.Errorf("sort[1] = %q, want intent-c", intents[1].IntentID)
	}
	if intents[2].IntentID != "intent-b" {
		t.Errorf("sort[2] = %q, want intent-b", intents[2].IntentID)
	}
}

func TestTemplateResolver_ResolvesEntityTypeToTemplateID(t *testing.T) {
	resolver := NewTemplateResolver(NewChrononOverlayRegistry())
	cases := map[string]string{
		"PERSON":    "person_default",
		"ORG":       "org_default",
		"GPE":       "gpe_default",
		"LOCATION":  "gpe_default",
		"NUMBER":    "NUMBER",
		"CARDINAL":  "NUMBER", // same kind owner as the planner bucket
		"QUOTE":     "quote",
		"PRODUCT":   "PRODUCT",
		"LOGO":      "LOGO",
		"EVENT":     "concept_default",
		"something": "concept_default",
	}
	for entityType, want := range cases {
		got, err := resolver.Resolve(entityType)
		if err != nil {
			t.Errorf("Resolve(%q) error: %v", entityType, err)
			continue
		}
		if got != want {
			t.Errorf("Resolve(%q) = %q, want %q", entityType, got, want)
		}
	}
}

func TestTemplateResolver_DeterministicAndRegistryOwned(t *testing.T) {
	a := NewTemplateResolver(NewChrononOverlayRegistry())
	b := NewTemplateResolver(nil) // nil falls back to the default registry
	for _, entityType := range []string{"PERSON", "ORG", "GPE", "NUMBER", "EVENT"} {
		tmplA, errA := a.Resolve(entityType)
		tmplB, errB := b.Resolve(entityType)
		if errA != nil || errB != nil {
			t.Fatalf("Resolve(%q) errors: %v / %v", entityType, errA, errB)
		}
		if tmplA != tmplB {
			t.Errorf("Resolve(%q) drifted across registries: %q vs %q", entityType, tmplA, tmplB)
		}
		// The template must come from the registry, never a switch.
		kind := EntityTypeToKind(entityType)
		want, err := DefaultChrononOverlayRegistry.ResolveTemplate(string(kind))
		if err != nil {
			t.Fatalf("registry ResolveTemplate(%q): %v", kind, err)
		}
		if tmplA != want {
			t.Errorf("Resolve(%q) = %q, registry says %q", entityType, tmplA, want)
		}
	}
}

func TestEntityOverlayPlanner_PlansViaTemplateResolver(t *testing.T) {
	planner := NewEntityOverlayPlanner(NewTemplateResolver(NewChrononOverlayRegistry()))
	scenes := []SceneEntityInput{
		{SceneID: "scene-01", SceneIndex: 0, Entities: []EntityOverlayInput{
			{Name: "Margot Robbie", Type: "PERSON", Confidence: 0.98},
		}},
		{SceneID: "scene-02", SceneIndex: 1, Entities: []EntityOverlayInput{
			{Name: "Warner Bros", Type: "ORG", Confidence: 0.91},
			{Name: "Hollywood", Type: "GPE", Confidence: 0.88},
		}},
	}
	intents := planner.Plan(scenes)
	if len(intents) != 3 {
		t.Fatalf("planned %d intents, want 3", len(intents))
	}
	want := []struct {
		scene, template string
	}{
		{"scene-01", "person_default"},
		{"scene-02", "org_default"},
		{"scene-02", "gpe_default"},
	}
	for i, w := range want {
		if intents[i].SceneID != w.scene || intents[i].TemplateID != w.template {
			t.Errorf("intent[%d] = scene=%q template=%q, want scene=%q template=%q", i, intents[i].SceneID, intents[i].TemplateID, w.scene, w.template)
		}
		if intents[i].TimingState != TimingStatePending {
			t.Errorf("intent[%d] timing_state = %q, want PENDING", i, intents[i].TimingState)
		}
		if err := intents[i].Validate(); err != nil {
			t.Errorf("intent[%d] invalid: %v", i, err)
		}
	}
	if got := planner.Plan(scenes); len(got) != 3 {
		t.Errorf("planner must be deterministic: second Plan returned %d intents", len(got))
	}
}

func TestOverlayIntent_AllEntityTypesResolved(t *testing.T) {
	registry := NewChrononOverlayRegistry()
	scenes := []SceneEntityInput{
		{SceneID: "scene-01", SceneIndex: 0, Entities: []EntityOverlayInput{
			{Name: "Alice", Type: "PERSON"},
			{Name: "Acme Corp", Type: "ORG"},
			{Name: "New York", Type: "GPE"},
			{Name: "42", Type: "NUMBER"},
			{Name: "Famous Quote", Type: "QUOTE"},
			{Name: "Widget", Type: "PRODUCT"},
			{Name: "Corp Logo", Type: "LOGO"},
			{Name: "Some Concept", Type: "EVENT"},
		}},
	}
	intents := PlanOverlayIntents(scenes, registry)
	if len(intents) != 8 {
		t.Fatalf("expected 8 intents for all entity types, got %d", len(intents))
	}

	expected := map[string]string{
		"Alice":        "person_default",
		"Acme Corp":    "org_default",
		"New York":     "gpe_default",
		"42":           "NUMBER",
		"Famous Quote": "quote",
		"Widget":       "PRODUCT",
		"Corp Logo":    "LOGO",
		"Some Concept": "concept_default",
	}
	for _, intent := range intents {
		want, ok := expected[intent.Entity.CanonicalName]
		if !ok {
			t.Errorf("unexpected entity %q", intent.Entity.CanonicalName)
			continue
		}
		if intent.TemplateID != want {
			t.Errorf("entity %q template = %q, want %q", intent.Entity.CanonicalName, intent.TemplateID, want)
		}
	}
}
