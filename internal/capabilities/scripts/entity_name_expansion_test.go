// Package scriptgeneration — entity_name_expansion_test.go pins the
// person-name normalization contract that keeps repeated mentions of the same
// person from being split into several identities.
//
// Defect closed here (found by the Goal 4 corpus): expandPersonName could never
// join a surname to its first name, because it flushed its candidate buffer on
// every space. A scene whose raw extractor output carried both "Elon Musk" and
// a later "Musk" therefore produced TWO entities ("Elon Musk" and "Musk") with
// two different StableEntityIDs — the same person duplicated under a slightly
// different name, and an extra overlay for a name the narration never spoke on
// its own.
package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestExpandPersonName_SurnameAndFirstNameJoin(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		value string
		want  string
	}{
		{"surname expands to the spoken full name", "Elon Musk founded Tesla.", "Musk", "Elon Musk"},
		{"first name expands to the spoken full name", "Elon Musk founded Tesla.", "Elon", "Elon Musk"},
		{"surname after a full mention still resolves", "Elon Musk founded Tesla. Later Musk sold shares.", "Musk", "Elon Musk"},
		{"ambiguous surname picks its own full name", "Michael Jordan played for the Chicago Bulls.", "Jordan", "Michael Jordan"},
		{"accented surname joins", "Jürgen Stock led Interpol.", "Stock", "Jürgen Stock"},
		{"full multi-token value is untouched", "Elon Musk founded Tesla.", "Elon Musk", "Elon Musk"},
		{"mononym with no multi-token run is untouched", "Beyoncé performed in Houston.", "Beyoncé", "Beyoncé"},
		{"lowercase particle breaks the run safely", "Ursula von der Leyen leads the Union.", "Leyen", "Leyen"},
		{"a value outside any run is untouched", "Elon Musk founded Tesla.", "Bezos", "Bezos"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, expandPersonName(tc.text, tc.value))
		})
	}
}

// TestProjectEntityAnnotations_CollapsesRepeatedPersonSurfaceForms certifies
// the observable consequence: several surface forms of one person in a single
// scene collapse onto ONE grounded primary entity with ONE canonical name.
func TestProjectEntityAnnotations_CollapsesRepeatedPersonSurfaceForms(t *testing.T) {
	text := "Elon Musk founded Tesla. Later Musk sold shares and Elon Musk invested again."
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-0",
		Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
			{Value: "Elon Musk", Type: "PERSON", Confidence: 0.98},
			{Value: "Musk", Type: "PERSON", Confidence: 0.93},
			{Value: "Elon", Type: "PERSON", Confidence: 0.80},
		}},
	}

	ann := projectEntityAnnotations(text, "en", seg)
	require.NotNil(t, ann)
	require.Len(t, ann.PrimaryEntities, 1, "one person must yield exactly one primary entity")
	require.Equal(t, "Elon Musk", ann.PrimaryEntities[0].CanonicalName)
	require.Equal(t, "PERSON", ann.PrimaryEntities[0].Type)
	require.NotEmpty(t, ann.PrimaryEntities[0].Mentions, "the collapsed entity keeps its grounded mention spans")
}
