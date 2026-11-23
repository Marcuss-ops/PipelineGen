package scriptgeneration

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TICKET-49: dates and quantities belong to the numeric lane and a slug/UUID
// fragment is a tokenization artifact, not a canonical entity. None of them
// may author an entity intent; real people/orgs/places must still pass.
func TestSceneEntityInputDropsNumericAndSlugIntentSources(t *testing.T) {
	ann := &scriptpkg.SceneAnnotations{
		Version:  1,
		Language: "it",
		Status:   "completed",
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{CanonicalName: "Corrado Augias", Type: "PERSON"},
			{CanonicalName: "Rai", Type: "ORG"},
			{CanonicalName: "Roma", Type: "GPE"},
			{CanonicalName: "26 gennaio 1935", Type: "DATE"},
			{CanonicalName: "7", Type: "NUMBER"},
			{CanonicalName: "6.000 lettere", Type: "NUMBER"},
		},
		SecondaryEntities: []scriptpkg.AnnotatedEntity{
			{CanonicalName: "addio-a-corrado-augias_57daaef8-24d4-444a-913b-7088122eeb88", Type: "NUMBER"},
			{CanonicalName: "57daaef8-24d4-444a-913b-7088122eeb88", Type: "CONCEPT"},
			{CanonicalName: "2024", Type: "DATE"},
		},
	}

	input, ok := sceneEntityInput("scene-01", 0, ann)
	if !ok {
		t.Fatal("sceneEntityInput returned false for a non-nil annotation bundle")
	}
	var names []string
	for _, entity := range input.Entities {
		names = append(names, entity.Name)
	}
	want := []string{"Corrado Augias", "Rai", "Roma"}
	if len(names) != len(want) {
		t.Fatalf("entity intents = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entity intents = %v, want %v", names, want)
		}
	}
}

func TestEntityIntentEligibleRejectsNumericFamiliesAndSlugUUID(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   scriptpkg.AnnotatedEntity
		want bool
	}{
		{"person", scriptpkg.AnnotatedEntity{CanonicalName: "Vito Mancuso", Type: "PERSON"}, true},
		{"org alias", scriptpkg.AnnotatedEntity{CanonicalName: "La7", Type: "ORGANIZATION"}, true},
		{"place alias", scriptpkg.AnnotatedEntity{CanonicalName: "Campidoglio", Type: "FAC"}, true},
		{"date", scriptpkg.AnnotatedEntity{CanonicalName: "19 novembre", Type: "DATE"}, false},
		{"number", scriptpkg.AnnotatedEntity{CanonicalName: "91", Type: "CARDINAL"}, false},
		{"money", scriptpkg.AnnotatedEntity{CanonicalName: "$2 million", Type: "MONEY"}, false},
		{"percent", scriptpkg.AnnotatedEntity{CanonicalName: "25%", Type: "PERCENT"}, false},
		{"slug uuid", scriptpkg.AnnotatedEntity{CanonicalName: "addio-a-corrado_57daaef8-24d4-444a-913b-7088122eeb88", Type: "CONCEPT"}, false},
		{"blank", scriptpkg.AnnotatedEntity{CanonicalName: "  ", Type: "PERSON"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := entityIntentEligible(tc.in); got != tc.want {
				t.Fatalf("entityIntentEligible(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
