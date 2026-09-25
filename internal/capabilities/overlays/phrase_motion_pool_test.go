package overlays

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCertifiedPhraseMotionsIsTheRotationAuthority pins the membership list a
// caller-supplied pool is validated against: it must be non-empty and contain
// every motion the semantic planner is allowed to select.
func TestCertifiedPhraseMotionsIsTheRotationAuthority(t *testing.T) {
	certified := CertifiedPhraseMotions()
	if len(certified) == 0 {
		t.Fatal("no certified phrase motions")
	}
	if len(certified) != len(phraseMotionCandidates) {
		t.Fatalf("CertifiedPhraseMotions() = %d entries, want the %d default candidates", len(certified), len(phraseMotionCandidates))
	}
	for i, id := range certified {
		if id != phraseMotionCandidates[i] {
			t.Fatalf("entry %d = %q, want %q", i, id, phraseMotionCandidates[i])
		}
	}
	// The copy must not alias the package storage.
	certified[0] = "mutated"
	if phraseMotionCandidates[0] == "mutated" {
		t.Fatal("CertifiedPhraseMotions returned an alias of the internal pool")
	}
}

// TestBuildPlanRejectsAnUncertifiedMotionPool is the fail-closed half: a pool
// naming an id the renderer cannot run (or repeating one) never reaches the
// rotation.
func TestBuildPlanRejectsAnUncertifiedMotionPool(t *testing.T) {
	base := func() PlanInput {
		return PlanInput{
			PlanID: "pool", VideoID: "video-pool", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
			Scenes: []SceneInput{{
				ID: "scene-1",
				Phrases: []TimedAnnotation{
					{Text: "a phrase that is spoken", StartMs: 100, EndMs: 600, StartUS: 100_000, DurationUS: 500_000, Score: 1},
				},
			}},
		}
	}
	cfg := AllCandidatesPlannerConfig(base().Scenes)

	unknown := base()
	unknown.PhraseMotions = []string{"not_a_motion"}
	if _, err := BuildPlan(unknown, cfg); err == nil {
		t.Fatal("an uncertified motion id must fail closed")
	}

	certified := CertifiedPhraseMotions()
	dup := base()
	dup.PhraseMotions = []string{certified[0], certified[0]}
	if _, err := BuildPlan(dup, cfg); err == nil {
		t.Fatal("a duplicated motion id must fail closed")
	}
}

// TestBuildPlanRotatesWithinTheChannelPool pins the behaviour a channel
// profile buys: every admitted phrase still gets a DISTINCT motion, drawn
// from the pool instead of the certified default list, and the selection
// stays deterministic across runs.
func TestBuildPlanRotatesWithinTheChannelPool(t *testing.T) {
	certified := CertifiedPhraseMotions()
	// Explicit pools replace the soft six-motion default; these entries are
	// certified but intentionally outside that default subset.
	pool := []string{certified[6], certified[7]}
	input := PlanInput{
		PlanID: "pool", VideoID: "video-pool", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		PhraseMotions: pool,
		Scenes: []SceneInput{{
			ID: "scene-1",
			Phrases: []TimedAnnotation{
				{Text: "first grounded phrase", StartMs: 100, EndMs: 600, StartUS: 100_000, DurationUS: 500_000, Score: 1},
				{Text: "second grounded phrase", StartMs: 700, EndMs: 1200, StartUS: 700_000, DurationUS: 500_000, Score: 0.9},
			},
		}},
	}
	cfg := AllCandidatesPlannerConfig(input.Scenes)

	plan, err := BuildPlan(input, cfg)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	seen := map[string]bool{}
	motions := 0
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		motions++
		if !containsString(pool, item.MotionID) {
			t.Fatalf("phrase motion %q is outside the channel pool %v", item.MotionID, pool)
		}
		if seen[item.MotionID] {
			t.Fatalf("motion %q was handed to two phrases: distinctness lost", item.MotionID)
		}
		seen[item.MotionID] = true
	}
	if motions == 0 {
		t.Fatal("no phrase items in the plan; the fixture stopped exercising the rotation")
	}

	again, err := BuildPlan(input, cfg)
	if err != nil {
		t.Fatalf("second BuildPlan: %v", err)
	}
	for i := range plan.Items {
		if plan.Items[i].MotionID != again.Items[i].MotionID {
			t.Fatalf("rotation is not deterministic: %q vs %q", plan.Items[i].MotionID, again.Items[i].MotionID)
		}
	}
}

func TestModernAppleFamilyIncludesCertifiedMotionsOutsideSoftDefault(t *testing.T) {
	family := certifiedPhraseFamily("modern_apple")
	if len(family) <= len(generatedPhraseMotions) {
		t.Fatalf("modern_apple family has %d motions, want more than soft default %d", len(family), len(generatedPhraseMotions))
	}
	for _, id := range generatedPhraseMotions {
		if !containsString(family, id) {
			t.Fatalf("modern_apple family omitted default motion %q", id)
		}
	}
	for _, id := range []string{"phrase_apple_clean_07_slide_up_soft", "phrase_apple_clean_25_opacity_soft_reveal"} {
		if !containsString(family, id) {
			t.Fatalf("modern_apple family omitted certified motion %q", id)
		}
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestCertifiedImageMotionPoolAndPlannerAssignment(t *testing.T) {
	want := []string{"image_25d_depth_float_in", "image_25d_yaw_flip_in", "image_25d_pitch_lift", "image_25d_pop_z_bounce", "image_25d_swipe_3d", "image_25d_card_swing", "image_25d_blur_focus_in", "image_25d_blur_scale_in"}
	got := CertifiedImageMotions()
	if len(got) != len(want) {
		t.Fatalf("CertifiedImageMotions has %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] || imageMotionCandidates[i] != want[i] {
			t.Fatalf("image motion %d = %q / %q, want catalog contract %q", i, got[i], imageMotionCandidates[i], want[i])
		}
	}
	input := PlanInput{PlanID: "image-motion-pool", VideoID: "v", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{ID: "s", Images: []ImageCandidate{
			{AssetID: "a", StartMs: 0, EndMs: 3000, StartUS: 0, DurationUS: 3_000_000, Score: 1},
			{AssetID: "b", StartMs: 4000, EndMs: 7000, StartUS: 4_000_000, DurationUS: 3_000_000, Score: .9},
		}}},
	}
	plan, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes))
	if err != nil {
		t.Fatal(err)
	}
	imageCount := 0
	for _, item := range plan.Items {
		if item.Kind != "image" {
			continue
		}
		if item.MotionID != "" || item.PresetID == "" {
			t.Fatalf("image item %q must use its 2D preset animation, preset=%q motion=%q", item.ID, item.PresetID, item.MotionID)
		}
		imageCount++
	}
	if imageCount != 2 {
		t.Fatalf("planned %d images, want 2", imageCount)
	}
	input.ImageMotions = got[:2]
	if _, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes)); err == nil || !strings.Contains(err.Error(), "deprecated and unsupported") {
		t.Fatalf("non-empty image motion pool must be rejected clearly, got %v", err)
	}
}

func TestImageMotionPoolMatchesCanonicalChrononCatalog(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var catalogPath string
	for dir := root; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "ChrononTemplate", "catalog", "motion_catalog.v1.json")
		if _, err := os.Stat(candidate); err == nil {
			catalogPath = candidate
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	if catalogPath == "" {
		t.Fatal("could not locate canonical ChrononTemplate motion catalog")
	}
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Motions []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
		} `json:"motions"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var catalogIDs []string
	for _, definition := range document.Motions {
		if definition.Category == "image_25d_clean_v1" {
			catalogIDs = append(catalogIDs, definition.ID)
		}
	}
	if len(catalogIDs) != len(imageMotionCandidates) {
		t.Fatalf("catalog has %d clean image motions, pool has %d", len(catalogIDs), len(imageMotionCandidates))
	}
	for i := range catalogIDs {
		if catalogIDs[i] != imageMotionCandidates[i] {
			t.Fatalf("catalog motion %d=%q, pool=%q", i, catalogIDs[i], imageMotionCandidates[i])
		}
	}
}
