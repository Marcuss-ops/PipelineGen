package overlays

import (
	"encoding/json"
	"fmt"
	"testing"
)

// TestBuildPlanProducesVariedPhraseAndImageMotions covers the actual semantic
// planning boundary: multiple grounded phrases and generated images must get
// distinct catalog motions before the plan reaches RenderingGen.
func TestBuildPlanProducesVariedPhraseAndImageMotions(t *testing.T) {
	phrases := make([]TimedAnnotation, 4)
	images := make([]ImageCandidate, 4)
	for i := 0; i < 4; i++ {
		start := int64(i * 2_000)
		phrases[i] = TimedAnnotation{
			Text:       fmt.Sprintf("Grounded phrase number %d", i+1),
			StartMs:    start,
			EndMs:      start + 1_800,
			StartUS:    start * 1_000,
			DurationUS: 1_800_000,
			Score:      1 - float64(i)*0.01,
		}
		imageStart := start + 1_000
		images[i] = ImageCandidate{
			AssetID:    fmt.Sprintf("image-%d", i+1),
			URL:        fmt.Sprintf("https://example.test/image-%d.jpg", i+1),
			SHA256:     fmt.Sprintf("%064x", i+1),
			MediaType:  "image/jpeg",
			StartMs:    imageStart,
			EndMs:      imageStart + 900,
			StartUS:    imageStart * 1_000,
			DurationUS: 900_000,
			Score:      1 - float64(i)*0.01,
		}
	}
	input := PlanInput{
		PlanID: "motion-diversity-e2e", VideoID: "motion-diversity-e2e",
		Width: 1280, Height: 720, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene-1", Phrases: phrases, Images: images}},
	}
	plan, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes))
	if err != nil {
		t.Fatalf("BuildPlan mixed motion plan: %v", err)
	}

	phraseIDs := make(map[string]bool)
	imageIDs := make(map[string]bool)
	phraseCount, imageCount := 0, 0
	for _, item := range plan.Items {
		switch item.Kind {
		case "text_phrase":
			phraseCount++
			if item.MotionID == "" || phraseIDs[item.MotionID] {
				t.Errorf("phrase %q has empty or repeated motion %q", item.Text, item.MotionID)
			}
			phraseIDs[item.MotionID] = true
		case "image":
			imageCount++
			if item.MotionID == "" || imageIDs[item.MotionID] {
				t.Errorf("image %q has empty or repeated motion %q", item.ID, item.MotionID)
			}
			imageIDs[item.MotionID] = true
		}
	}
	if phraseCount != len(phrases) || imageCount != len(images) {
		t.Fatalf("plan has %d phrase and %d image overlays, want %d and %d", phraseCount, imageCount, len(phrases), len(images))
	}

	wire, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal semantic plan: %v", err)
	}
	var contract struct {
		Items []struct {
			Kind   string `json:"kind"`
			Motion string `json:"motion_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(wire, &contract); err != nil {
		t.Fatalf("decode semantic plan contract: %v", err)
	}
	if len(contract.Items) != phraseCount+imageCount {
		t.Fatalf("serialized plan has %d items, want %d", len(contract.Items), phraseCount+imageCount)
	}
	for _, item := range contract.Items {
		if item.Motion == "" {
			t.Errorf("serialized %s item lost its motion_id", item.Kind)
		}
	}
}
