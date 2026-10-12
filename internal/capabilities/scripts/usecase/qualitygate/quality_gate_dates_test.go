package qualitygate

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSourceDatePreservationCheckerRejectsChangedYear(t *testing.T) {
	in := qualityGateInput{
		result: &scriptpkg.GenerationResult{Output: scriptpkg.ScriptOutput{
			Text: "Il tredici agosto del duemiladue è una data ricordata a Garlasco.",
			SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
				ID: "scene-1", SegmentID: "delitto-e-luogo",
				Text: "Il tredici agosto del duemiladue è una data ricordata a Garlasco.",
			}}},
		}},
		plan: scriptpkg.ResolvedGenerationPlan{Segments: []scriptpkg.ScriptSegment{{
			ID:         "delitto-e-luogo",
			SourceText: "Presenta Chiara Poggi uccisa a Garlasco il 13 agosto 2007.",
		}}},
	}

	reasons := (sourceDatePreservationChecker{}).Check(in)
	if len(reasons) != 1 || !strings.Contains(reasons[0], "13-08-2007") {
		t.Fatalf("date mutation should fail with the expected source date, got %v", reasons)
	}
}

func TestSourceDatePreservationCheckerAcceptsNumericDateAndEquivalentFormat(t *testing.T) {
	for _, narration := range []string{
		"Il 13 agosto 2007 è un riferimento temporale del caso.",
		"La data, 13 agosto del 2007, è riportata nella fonte.",
		"Il 13/08/2007 è un riferimento temporale del caso.",
	} {
		in := qualityGateInput{
			result: &scriptpkg.GenerationResult{Output: scriptpkg.ScriptOutput{
				Text: narration,
				SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
					ID: "scene-1", SegmentID: "delitto-e-luogo", Text: narration,
				}}},
			}},
			plan: scriptpkg.ResolvedGenerationPlan{Segments: []scriptpkg.ScriptSegment{{
				ID: "delitto-e-luogo", SourceText: "Il fatto risale al 13 agosto 2007.",
			}}},
		}
		if reasons := (sourceDatePreservationChecker{}).Check(in); len(reasons) != 0 {
			t.Errorf("narration %q should preserve the source date, got %v", narration, reasons)
		}
	}
}

func TestExplicitCalendarDatesNormalizesDifferentNumericSeparators(t *testing.T) {
	want := calendarDate{day: 13, month: 8, year: 2007}
	for _, text := range []string{"13/08/2007", "13-08-2007", "13.08.2007", "13 agosto 2007"} {
		got := explicitCalendarDates(text)
		if len(got) != 1 || got[0] != want {
			t.Errorf("explicitCalendarDates(%q) = %v, want [%v]", text, got, want)
		}
	}
}
