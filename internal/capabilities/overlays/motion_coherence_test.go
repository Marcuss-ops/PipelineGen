package overlays

import (
	"encoding/json"
	"fmt"
	"testing"
)

// The dominant direction is a pure function of the seed: the same seed always
// yields the same side, an empty seed disables coherence, and both sides occur
// across seeds so the choice is genuinely varied.
func TestCoherenceDominantDirectionIsDeterministicPerSeed(t *testing.T) {
	if got := CoherenceDominantDirection(""); got != "" {
		t.Fatalf("empty seed dominant = %q, want disabled", got)
	}
	seen := map[string]bool{}
	for _, seed := range []string{"run-a", "run-b", "run-c", "run-d", "run-e", "run-f", "run-g", "run-h"} {
		first := CoherenceDominantDirection(seed)
		if first != CoherenceDominantDirection(seed) {
			t.Fatalf("seed %q dominant changed between calls", seed)
		}
		seen[first] = true
	}
	if !seen[CoherenceDirectionLeft] || !seen[CoherenceDirectionRight] {
		t.Fatalf("dominant directions across seeds = %v, want both left and right", seen)
	}
}

// Direction is read from whole underscore tokens, so both entering-from and
// sliding motions classify correctly, and a motion naming both sides is neutral.
func TestMotionHorizontalDirectionClassifiesWholeTokens(t *testing.T) {
	cases := map[string]string{
		"phrase_apple_clean_10_slide_from_right_apple": CoherenceDirectionRight,
		"phrase_apple_clean_11_slide_left_ease":        CoherenceDirectionLeft,
		"image_slide_left_reveal":                      CoherenceDirectionLeft,
		"image_slide_right_reveal":                     CoherenceDirectionRight,
		"image_fade_reveal":                            "",
		"image_left_right_split":                       "",
	}
	for id, want := range cases {
		if got := motionHorizontalDirection(id); got != want {
			t.Fatalf("motionHorizontalDirection(%q) = %q, want %q", id, got, want)
		}
	}
}

// Only dominant-direction and neutral motions survive; the opposite side is
// excluded entirely while the pool keeps every other entry.
func TestCoherentMotionPoolKeepsDominantAndNeutralOnly(t *testing.T) {
	pool := []string{"image_slide_left_reveal", "image_slide_right_reveal", "image_fade_reveal", "image_scale_reveal"}
	for _, seed := range []string{"run-a", "run-b"} {
		dominant := CoherenceDominantDirection(seed)
		opposite := oppositeDirection(dominant)
		got := coherentMotionPool(pool, seed)
		for _, id := range got {
			if motionHorizontalDirection(id) == opposite {
				t.Fatalf("seed %q (dominant %s) kept opposite motion %q", seed, dominant, id)
			}
		}
		if len(got) != 3 {
			t.Fatalf("seed %q coherent pool = %v, want dominant+2 neutral (3)", seed, got)
		}
	}
}

// Fail-open: with no directional motion on the dominant side, or with coherence
// disabled, the pool is returned unchanged so no lane is ever empty.
func TestCoherentMotionPoolFailsOpen(t *testing.T) {
	neutral := []string{"image_fade_reveal", "image_scale_reveal"}
	if got := coherentMotionPool(neutral, "run-a"); len(got) != len(neutral) {
		t.Fatalf("neutral-only pool filtered to %v", got)
	}
	mixed := []string{"image_slide_left_reveal", "image_slide_right_reveal"}
	if got := coherentMotionPool(mixed, ""); len(got) != len(mixed) {
		t.Fatalf("disabled coherence filtered pool to %v", got)
	}
}

// Retries of the same run produce bit-identical coherent selections.
func TestCoherentImageSelectionIsBitIdenticalAcrossRetries(t *testing.T) {
	const seed = "retry-run"
	for ordinal := 0; ordinal < 12; ordinal++ {
		first := selectImageMotion(seed, "scene", ordinal, nil)
		second := selectImageMotion(seed, "scene", ordinal, nil)
		if first != second {
			t.Fatalf("ordinal %d retry changed %q -> %q", ordinal, first, second)
		}
		if motionHorizontalDirection(first) == oppositeDirection(CoherenceDominantDirection(seed)) {
			t.Fatalf("ordinal %d selected opposite-direction motion %q", ordinal, first)
		}
	}
}

// An explicit coherence seed decides the dominant direction on its own: the
// composed seed resolves to the same side as the coherence seed alone, and an
// empty coherence seed leaves the run seed untouched (default byte-identity).
func TestExplicitCoherenceSeedDecidesDominantDirection(t *testing.T) {
	if got := WithCoherenceSeed("run-a", ""); got != "run-a" {
		t.Fatalf("empty coherence seed changed run seed to %q", got)
	}
	for _, coherence := range []string{"c-1", "c-2", "c-3", "c-4", "c-5", "c-6"} {
		composed := WithCoherenceSeed("run-a", coherence)
		if CoherenceDominantDirection(composed) != CoherenceDominantDirection(coherence) {
			t.Fatalf("coherence %q: composed dominant %q != standalone %q",
				coherence, CoherenceDominantDirection(composed), CoherenceDominantDirection(coherence))
		}
	}
}

// The explicit seed reaches the compiled plan: every image motion the planner
// emits avoids the opposite side of the coherence seed's dominant direction,
// and recompiling with the same seeds is bit-identical.
func TestExplicitCoherenceSeedGovernsCompiledPlanMotions(t *testing.T) {
	images := make([]ImageCandidate, 6)
	for i := range images {
		start := int64(i * 2_000)
		images[i] = ImageCandidate{AssetID: fmt.Sprintf("asset-%d", i), URL: fmt.Sprintf("https://example.test/%d.jpg", i), SHA256: fmt.Sprintf("%064x", i+1), MediaType: "image/jpeg", StartMs: start, EndMs: start + 1_500, Score: 1 - float64(i)*.01}
	}
	build := func() OverlayPlan {
		input := PlanInput{PlanID: "coherence-plan", VideoID: "coherence-plan", Width: 1280, Height: 720, FPSNum: 24, FPSDen: 1,
			MotionSeed: "run-coherence", CoherenceSeed: "explicit-dominance",
			Scenes: []SceneInput{{ID: "scene", Images: images}}}
		plan, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes))
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	dominant := CoherenceDominantDirection("explicit-dominance")
	opposite := oppositeDirection(dominant)
	plan := build()
	emitted := 0
	for _, item := range plan.Items {
		if item.Kind != "image" || item.MotionID == "" {
			continue
		}
		emitted++
		if motionHorizontalDirection(item.MotionID) == opposite {
			t.Fatalf("image %q used opposite-direction motion %q (dominant %s)", item.ID, item.MotionID, dominant)
		}
	}
	if emitted == 0 {
		t.Fatal("plan emitted no image motions; the coherence check was not exercised")
	}
	first, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(build())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("recompiling with the same coherence seed changed the plan")
	}
}
