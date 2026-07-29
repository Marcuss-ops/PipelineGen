package soundcues

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestPlannerDefaultsToEnhanceOnlyAndKeepsTiming(t *testing.T) {
	cues, err := NewPlanner().Plan([]asset.VisualEvent{{StartMs: 5200, EndMs: 8400, Text: "The boxer punches the heavy bag."}}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 1 || cues[0].TriggerMs != 5200 || !cues[0].PreserveNativeAudio {
		t.Fatalf("unexpected cues: %#v", cues)
	}
}

func TestPlannerNeverInventsCueForUnknownAction(t *testing.T) {
	cues, err := NewPlanner().Plan([]asset.VisualEvent{{StartMs: 0, EndMs: 1000, Text: "The athlete rests."}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 0 {
		t.Fatalf("unexpected cue: %#v", cues)
	}
}
