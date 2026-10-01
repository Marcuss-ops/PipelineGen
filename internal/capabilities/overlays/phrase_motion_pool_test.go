package overlays

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	// Explicit pools replace the generated 30-motion default; these entries
	// are certified but intentionally outside the Apple motion family.
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

func TestModernAppleFamilyExposesAllCatalogMotions(t *testing.T) {
	family := certifiedPhraseFamily("modern_apple")
	if len(family) != 60 {
		t.Fatalf("modern_apple family has %d motions, want all 60 registered modern Apple motions", len(family))
	}
	for _, id := range modernAppleMotionCandidates {
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

func TestGeneratedPhraseRotationCoversFifteenAndFits24FPSTiming(t *testing.T) {
	if len(generatedPhraseMotions) != 107 {
		t.Fatalf("generated phrase pool has %d entries, want all 107 registered phrase motions", len(generatedPhraseMotions))
	}
	sequence := defaultPhraseMotionSequence("rotation-107", "run")
	seenAll := make(map[string]bool, 107)
	familyCounts := map[string]int{}
	for i, id := range sequence {
		if seenAll[id] {
			t.Fatalf("default phrase sequence repeats %q before all catalog motions are covered", id)
		}
		seenAll[id] = true
		if i < 15 {
			switch {
			case containsString(classicAppleMotionCandidates, id):
				familyCounts["classic_apple"]++
			case containsString(modernAppleMotionCandidates, id):
				familyCounts["modern_apple"]++
			case containsString(typewriterMotionCandidates, id):
				familyCounts["typewriter"]++
			default:
				t.Fatalf("default phrase sequence selected unregistered motion %q", id)
			}
		}
	}
	if len(sequence) != 107 || len(seenAll) != 107 {
		t.Fatalf("default phrase sequence covers %d motions, want all 107", len(seenAll))
	}
	for family, want := range map[string]int{"classic_apple": 5, "modern_apple": 5, "typewriter": 5} {
		if familyCounts[family] != want {
			t.Fatalf("first 15 phrase motions contain %d %s entries, want %d", familyCounts[family], family, want)
		}
	}
	phrases := make([]TimedAnnotation, 15)
	for i := range phrases {
		phrases[i] = TimedAnnotation{
			Text:       fmt.Sprintf("distinct spoken phrase %02d", i),
			StartMs:    int64(i * 2200),
			EndMs:      int64(i*2200 + 2000),
			StartUS:    int64(i*2200) * 1000,
			DurationUS: 2_000_000,
			Score:      1 - float64(i)*0.01,
		}
	}
	input := PlanInput{
		PlanID: "phrase-rotation-15", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene-1", Phrases: phrases}},
	}
	cfg := AllCandidatesPlannerConfig(input.Scenes)
	cfg.MaxPhrases = 15
	plan, err := BuildPlan(input, cfg)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	seen := make(map[string]bool, 15)
	count := 0
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		count++
		if seen[item.MotionID] {
			t.Fatalf("motion %q repeated before the 15th phrase", item.MotionID)
		}
		seen[item.MotionID] = true
		if got := item.MotionParams["enter_frames"]; got != 24 {
			t.Fatalf("phrase %q enter_frames = %v, want 24 frames (half of 2 seconds at 24 fps)", item.Text, got)
		}
	}
	if count != MaxPhraseOverlaysPerRun {
		t.Fatalf("planned %d phrase items, want 15", count)
	}
}

func TestPhraseMotionEntranceUsesHalfPhraseDuration(t *testing.T) {
	params := phraseMotionParams(TimedAnnotation{StartMs: 0, EndMs: 800, DurationUS: 800_000}, 24, 1)
	if got := params["enter_frames"]; got != 10 {
		t.Fatalf("short phrase enter_frames = %v, want 10 frames (half of 800 ms at 24 fps)", got)
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
	want := renderSafeImageMotions
	got := CertifiedImageMotions()
	if len(got) != len(want) {
		t.Fatalf("CertifiedImageMotions has %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] || imageMotionCandidates[i] != want[i] {
			t.Fatalf("image motion %d = %q / %q, want catalog contract %q", i, got[i], imageMotionCandidates[i], want[i])
		}
	}
	selected := make(map[string]bool, len(got))
	for ordinal := range got {
		id := selectImageMotion("image-catalog-18", "run", ordinal, nil)
		if !containsString(got, id) || selected[id] {
			t.Fatalf("image selector repeated or emitted an unknown motion at %d: %q", ordinal, id)
		}
		selected[id] = true
	}
	// The PUBLIC selector is the generated-overlay authority: it rotates only
	// the centered subset, never the cross-canvas motions of the full catalog.
	selected = make(map[string]bool, len(centeredImageMotionCandidates))
	for ordinal := range centeredImageMotionCandidates {
		id := SelectImageMotionAt("image-catalog-18", "run", ordinal)
		if !containsString(centeredImageMotionCandidates, id) || selected[id] {
			t.Fatalf("public image selector escaped the centered pool at %d: %q", ordinal, id)
		}
		selected[id] = true
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
		if !containsString(got, item.MotionID) || item.PresetID == "" {
			t.Fatalf("image item %q must use a certified image motion and image preset, preset=%q motion=%q", item.ID, item.PresetID, item.MotionID)
		}
		imageCount++
	}
	if imageCount != 2 {
		t.Fatalf("planned %d images, want 2", imageCount)
	}
	input.ImageMotions = got[:2]
	plan, err = BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes))
	if err != nil {
		t.Fatalf("certified explicit image motion pool: %v", err)
	}
	for _, item := range plan.Items {
		if item.Kind == "image" && !containsString(input.ImageMotions, item.MotionID) {
			t.Fatalf("image motion %q escaped the explicit pool %v", item.MotionID, input.ImageMotions)
		}
	}
}

// TestRandomImageMotionOffsetRotatesThroughAllCertifiedIDs pins the rotation
// over the CENTERED pool: generated image overlays rotate only motions that
// keep the raster pinned to the canvas center (the map rule). The full 18-id
// certified catalog stays authoritative via CertifiedImageMotions for explicit
// editorial plans; the rotation the planner emits must never pick a motion
// that carries the card across the canvas.
func TestRandomImageMotionOffsetRotatesThroughAllCertifiedIDs(t *testing.T) {
	count := len(centeredImageMotionCandidates)
	if count != 3 {
		t.Fatalf("centered image motions = %d, want 3", count)
	}
	for attempt := 0; attempt < 4; attempt++ {
		offset, err := RandomImageMotionOffset()
		if err != nil {
			t.Fatalf("random image motion offset: %v", err)
		}
		if offset < 0 || offset >= count {
			t.Fatalf("random offset = %d, outside [0,%d)", offset, count)
		}
		seen := make(map[string]bool, count)
		for ordinal := 0; ordinal < count; ordinal++ {
			id := ImageMotionAtOffset(offset, ordinal)
			if !containsString(centeredImageMotionCandidates, id) || seen[id] {
				t.Fatalf("offset %d ordinal %d emitted motion outside the centered pool %q", offset, ordinal, id)
			}
			seen[id] = true
		}
		if len(seen) != count {
			t.Fatalf("offset %d covered %d motions, want %d", offset, len(seen), count)
		}
	}
}

func TestImageMotionPoolMatchesCanonicalChrononCatalog(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var catalogPath string
	for dir := root; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "RenderingGen", "renderinggen", "internal", "motion", "catalog", "chronontemplate_catalog.v1.json")
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
			Tracks   []struct {
				Property string `json:"property"`
			} `json:"tracks"`
		} `json:"motions"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var catalogIDs, selectedIDs, catalogPhraseIDs []string
	safeSet := make(map[string]bool, len(imageMotionCandidates))
	for _, id := range imageMotionCandidates {
		safeSet[id] = true
	}
	for _, definition := range document.Motions {
		if definition.Category == "image_25d_clean_v1" || definition.Category == "overlay_v3_image" {
			catalogIDs = append(catalogIDs, definition.ID)
			if safeSet[definition.ID] {
				selectedIDs = append(selectedIDs, definition.ID)
				for _, track := range definition.Tracks {
					switch track.Property {
					case "opacity", "scale", "position_x", "position_y", "position_z", "rotation_x", "rotation_y", "blur":
					default:
						t.Fatalf("certified image motion %q uses unsupported transform %q", definition.ID, track.Property)
					}
				}
			}
		}
		if definition.Category == "apple_v2" || definition.Category == "apple_v3" || definition.Category == "phrase_apple_clean_v1" || definition.Category == "apple_phrase_v1" || strings.HasPrefix(definition.ID, "typewriter_") {
			catalogPhraseIDs = append(catalogPhraseIDs, definition.ID)
		}
	}
	sort.Strings(catalogIDs)
	sort.Strings(selectedIDs)
	sort.Strings(catalogPhraseIDs)
	if len(catalogIDs) != 18 {
		t.Fatalf("catalog has %d image motions; want all 18", len(catalogIDs))
	}
	wantSelected := append([]string(nil), imageMotionCandidates...)
	sort.Strings(wantSelected)
	if len(selectedIDs) != len(wantSelected) {
		t.Fatalf("catalog matches %d selected motions, pool has %d", len(selectedIDs), len(wantSelected))
	}
	for i := range selectedIDs {
		if selectedIDs[i] != wantSelected[i] {
			t.Fatalf("selected image motion %d=%q, catalog=%q", i, selectedIDs[i], wantSelected[i])
		}
	}
	if len(catalogPhraseIDs) != 107 || len(phraseMotionCandidates) != len(catalogPhraseIDs) {
		t.Fatalf("catalog has %d phrase motions, callable pool has %d; want all 107", len(catalogPhraseIDs), len(phraseMotionCandidates))
	}
	callable := append([]string(nil), phraseMotionCandidates...)
	sort.Strings(callable)
	for i := range catalogPhraseIDs {
		if catalogPhraseIDs[i] != callable[i] {
			t.Fatalf("catalog phrase motion %d=%q, callable pool=%q", i, catalogPhraseIDs[i], callable[i])
		}
	}
}
