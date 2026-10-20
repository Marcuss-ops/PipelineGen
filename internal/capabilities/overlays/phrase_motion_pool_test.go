package overlays

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	// Explicit pools replace the generated 107-motion default; these entries
	// are certified but intentionally outside the Apple motion family.
	pool := []string{certified[6], certified[7]}
	input := PlanInput{
		PlanID: "pool", VideoID: "video-pool", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		PhraseMotions: pool,
		Scenes: []SceneInput{{
			ID: "scene-1",
			Phrases: []TimedAnnotation{
				{Text: "the first grounded phrase stays on the important phrase lane", StartMs: 100, EndMs: 600, StartUS: 100_000, DurationUS: 500_000, Score: 1},
				{Text: "the second grounded phrase stays on the important phrase lane", StartMs: 700, EndMs: 1200, StartUS: 700_000, DurationUS: 500_000, Score: 0.9},
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

// TestBuildPlanLongPhrasesRotateWithinTheChannelPool is the regression gate for
// the production defect where every long IMPORTANT_PHRASE rendered the same
// animation: the crime channel's three-motion pool intersected the long-phrase
// editorial list in exactly one id, so all fifteen long phrases of a run
// received `phrase_apple_clean_07_slide_up_soft` (confirmed in the production
// render jobs). A caller pool must rotate across long cards too — the pool is
// the channel's explicit vocabulary.
func TestBuildPlanLongPhrasesRotateWithinTheChannelPool(t *testing.T) {
	pool := []string{
		"phrase_apple_clean_07_slide_up_soft",
		"phrase_apple_clean_08_slide_up_spring",
		"phrase_apple_clean_13_scale_soft_pop",
	}
	for _, id := range pool {
		if !containsString(CertifiedPhraseMotions(), id) {
			t.Fatalf("fixture motion %q is not certified", id)
		}
	}
	longPhrases := make([]TimedAnnotation, 15)
	for i := range longPhrases {
		longPhrases[i] = TimedAnnotation{
			Text:       fmt.Sprintf("uma frase longa com muitas palavras escritas para exercitar a entrada de bloco numero %02d", i),
			StartMs:    int64(i * 2200),
			EndMs:      int64(i*2200 + 2000),
			StartUS:    int64(i*2200) * 1000,
			DurationUS: 2_000_000,
			Score:      1 - float64(i)*0.01,
		}
		if words := len(strings.Fields(longPhrases[i].Text)); words < 6 {
			t.Fatalf("fixture phrase %d has %d words; the long-phrase path needs >= 6", i, words)
		}
	}
	input := PlanInput{
		PlanID: "long-phrase-pool", VideoID: "video-pool", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		PhraseMotions: pool,
		Scenes:        []SceneInput{{ID: "scene-1", Phrases: longPhrases}},
	}
	cfg := AllCandidatesPlannerConfig(input.Scenes)
	cfg.MaxPhrases = 15
	// The runtime raises the run-level phrase ceiling from the request
	// (max_phrase_overlays); 15 is the production value the collapse was
	// observed at.
	cfg.RunLevelPhraseOverlayLimit = 15

	plan, err := BuildPlan(input, cfg)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	seen := map[string]bool{}
	phrases := 0
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		phrases++
		if !containsString(pool, item.MotionID) {
			t.Fatalf("long phrase motion %q is outside the channel pool %v", item.MotionID, pool)
		}
		seen[item.MotionID] = true
	}
	if phrases != 15 {
		t.Fatalf("planned %d long phrases, want 15", phrases)
	}
	if len(seen) != len(pool) {
		t.Fatalf("long phrases covered %d motions, want the whole %d-motion pool: %v", len(seen), len(pool), seen)
	}

	again, err := BuildPlan(input, cfg)
	if err != nil {
		t.Fatalf("second BuildPlan: %v", err)
	}
	for i := range plan.Items {
		if plan.Items[i].MotionID != again.Items[i].MotionID {
			t.Fatalf("long-phrase rotation is not deterministic: %q vs %q", plan.Items[i].MotionID, again.Items[i].MotionID)
		}
	}
}

// TestLongLaneIncludesFixed3DMotions pins that the text_3d_v1 family is back
// in the long lane after the renderer fix was frame-verified (2026-10-15): a
// text_3d_camera_push phrase on a white canvas renders full-frame white
// corners (mean 255.0, no black clear showing) with visible animated text on
// the software backend, every text_3d motion settles its rotations to 0 from
// at most ±14° (so no authored frame can cross the ±90° mirror condition),
// and projected 2D layers compile DoubleSided. The background plate stays in
// screen space instead of entering the camera/depth bin with the text.
func TestLongLaneIncludesFixed3DMotions(t *testing.T) {
	want := map[string]bool{
		"text_3d_camera_push": false, "text_3d_perspective_drop": false,
		"text_3d_roll_depth": false, "text_3d_tilt_rise": false,
		"text_3d_word_cascade": false, "text_3d_yaw_flip_in": false,
		"text_3d_pitch_lift": false, "text_3d_yaw_sweep": false,
		"text_3d_double_axis_reveal": false, "text_3d_orbit_lock": false,
	}
	for _, id := range longPhraseMotionCandidates {
		if _, ok := want[id]; ok {
			want[id] = true
		}
		if strings.HasPrefix(id, "text_3d_") {
			if _, known := want[id]; !known {
				t.Fatalf("long lane carries unknown 3D motion %q", id)
			}
		}
	}
	for id, seen := range want {
		if !seen {
			t.Fatalf("long lane is missing fixed 3D motion %q", id)
		}
	}
	if len(longPhraseMotionCandidates) == 0 {
		t.Fatal("long lane is empty")
	}
}

func TestBuildPlanDefaultLongPhrasesRotateWithoutRepeating(t *testing.T) {
	longPhrases := make([]TimedAnnotation, 15)
	for i := range longPhrases {
		longPhrases[i] = TimedAnnotation{
			Text:       fmt.Sprintf("uma frase longa com varias palavras para provar que cada cartao recebe animacao diferente numero %02d", i),
			StartMs:    int64(i * 2200),
			EndMs:      int64(i*2200 + 2000),
			StartUS:    int64(i*2200) * 1000,
			DurationUS: 2_000_000,
			Score:      1 - float64(i)*0.01,
		}
	}
	input := PlanInput{
		PlanID: "default-long-phrase-rotation", VideoID: "video-long", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene-long", Phrases: longPhrases}},
	}
	config := AllCandidatesPlannerConfig(input.Scenes)
	config.MaxPhrases = len(longPhrases)
	config.RunLevelPhraseOverlayLimit = len(longPhrases)

	first, err := BuildPlan(input, config)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	second, err := BuildPlan(input, config)
	if err != nil {
		t.Fatalf("second BuildPlan: %v", err)
	}
	seen := make(map[string]bool, len(longPhrases))
	count := 0
	for index, item := range first.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		count++
		if !containsString(longPhraseMotionCandidates, item.MotionID) {
			t.Fatalf("long phrase motion %q is outside the default block-level pool", item.MotionID)
		}
		if seen[item.MotionID] && len(seen) < 5 {
			t.Fatalf("default long-phrase selector repeated %q before its pool was exhausted", item.MotionID)
		}
		seen[item.MotionID] = true
		if second.Items[index].MotionID != item.MotionID {
			t.Fatalf("default long-phrase rotation changed on retry: %q vs %q", item.MotionID, second.Items[index].MotionID)
		}
	}
	if count != len(longPhrases) {
		t.Fatalf("planned %d long phrases, want %d", count, len(longPhrases))
	}
	if len(seen) != min(len(longPhrases), len(longPhraseMotionCandidates)) {
		t.Fatalf("default long-phrase pool used %d styles, want up to the complete long-phrase rotation", len(seen))
	}
}

func TestModernAppleFamilyExposesAllCatalogMotions(t *testing.T) {
	family := certifiedPhraseFamily("modern_apple")
	if len(family) == 0 {
		t.Fatal("modern_apple family has no active registry motions")
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
	// No pinned count: the callable pool is derived from the animation
	// registry, so the test asserts the PROPERTIES that must always hold —
	// the pool equals the active phrase.all registry group, carries no
	// retired id, and every member is registry-selectable. Removing a
	// legitimated motion never breaks this test; count drift is the
	// registry's concern, not a second vocabulary here.
	callableMotions := excludeRemovedGeneratedPhraseMotions(generatedPhraseMotions)
	activeGroup := AnimationCandidates("phrase.all")
	if len(activeGroup) == 0 || len(callableMotions) == 0 {
		t.Fatal("phrase registry produced no runnable motions")
	}
	if len(callableMotions) != len(activeGroup) {
		t.Fatalf("callable phrase pool (%d) diverges from the active phrase.all registry group (%d)", len(callableMotions), len(activeGroup))
	}
	callableSet := make(map[string]bool, len(callableMotions))
	for _, id := range callableMotions {
		if AnimationIsRetired(id) {
			t.Fatalf("retired motion %q survived in the callable phrase pool", id)
		}
		if !AnimationSelectable(id, "phrase.all") {
			t.Fatalf("callable phrase motion %q is not registry-selectable for phrase.all", id)
		}
		callableSet[id] = true
	}
	for _, id := range activeGroup {
		if !callableSet[id] {
			t.Fatalf("active registry motion %q is missing from the callable phrase pool", id)
		}
	}
	callablePhraseMotionCount := len(callableMotions)
	sequence := defaultPhraseMotionSequence("rotation-159", "run")
	seenAll := make(map[string]bool, callablePhraseMotionCount)
	familyCounts := map[string]int{}
	for ordinal, want := range sequence {
		if got := selectPhraseMotion("rotation-159", "run", ordinal, nil); got != want {
			t.Fatalf("default phrase selector at ordinal %d = %q, want catalog rotation entry %q", ordinal, got, want)
		}
	}
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
			case containsString(text3DMotionCandidates, id):
				familyCounts["text_3d_v1"]++
			case containsString(brushPhraseMotionCandidates, id):
				familyCounts["brush_v1"]++
			case containsString(documentaryHighlightMotionCandidates, id):
				familyCounts["documentary_highlighting_v1"]++
			default:
				t.Fatalf("default phrase sequence selected unregistered motion %q", id)
			}
		}
	}
	if len(sequence) != callablePhraseMotionCount || len(seenAll) != callablePhraseMotionCount {
		t.Fatalf("default phrase sequence covers %d motions, want all %d", len(seenAll), callablePhraseMotionCount)
	}
	for _, family := range []string{"classic_apple", "modern_apple", "text_3d_v1", "brush_v1", "documentary_highlighting_v1"} {
		if familyCounts[family] == 0 {
			t.Fatalf("first 15 phrase motions do not include family %s", family)
		}
	}
	// The phrases deliberately vary in length: a short phrase only draws from
	// the exact-cardinality group its word count declares, and the smallest
	// groups hold a handful of recipes, so a run of fifteen four-word phrases
	// could not stay distinct. This fixture keeps every group inside its depth
	// while the no-repeat assertion below still demands fifteen distinct
	// motions from the fifteen phrases.
	rotationWordCounts := []int{1, 2, 3, 4, 1, 2, 3, 4, 3, 4, 1, 2, 3, 4, 1}
	phrases := make([]TimedAnnotation, len(rotationWordCounts))
	for i, words := range rotationWordCounts {
		phrases[i] = TimedAnnotation{
			Text:       fmt.Sprintf("%sphrase %02d", strings.Repeat("word ", words-1), i),
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
		if got := item.MotionParams["enter_frames"]; got != 16 {
			t.Fatalf("phrase %q enter_frames = %v, want 16 frames (one third of the 2 s window at 24 fps)", item.Text, got)
		}
	}
	if count != MaxPhraseOverlaysPerRun {
		t.Fatalf("planned %d phrase items, want 15", count)
	}
}

func TestPhraseMotionEntranceUsesOneThirdPhraseDuration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		phrase     TimedAnnotation
		fpsNum     int
		fpsDen     int
		wantFrames int
	}{
		{name: "microsecond timing rounds up", phrase: TimedAnnotation{StartMs: 0, EndMs: 800, DurationUS: 800_000}, fpsNum: 24, fpsDen: 1, wantFrames: 7},
		{name: "millisecond fallback", phrase: TimedAnnotation{StartMs: 100, EndMs: 3100}, fpsNum: 30, fpsDen: 1, wantFrames: 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := phraseMotionParams(tc.phrase, tc.fpsNum, tc.fpsDen)
			if got := params["enter_frames"]; got != tc.wantFrames {
				t.Fatalf("enter_frames = %v, want %d frames (one third of phrase duration)", got, tc.wantFrames)
			}
		})
	}
}

// TestPhraseExitIsDisabled pins the shared phrase exit: every run sends an
// explicit zero so RenderingGen skips the mirrored exit tracks entirely.
// Any positive window drove the settled phrase back through the authoring
// gray on the way out; the phrase must hold settled and cut hard instead.
func TestPhraseExitIsDisabled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fpsNum int
		fpsDen int
	}{
		{name: "24 fps", fpsNum: 24, fpsDen: 1},
		{name: "25 fps", fpsNum: 25, fpsDen: 1},
		{name: "30 fps", fpsNum: 30, fpsDen: 1},
		{name: "29.97 fps", fpsNum: 30000, fpsDen: 1001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := phraseMotionParams(TimedAnnotation{StartMs: 0, EndMs: 3000, DurationUS: 3_000_000}, tc.fpsNum, tc.fpsDen)
			if got := params["exit_frames"]; got != 0 {
				t.Fatalf("exit_frames = %v, want explicit 0 (disabled exit) at %d/%d fps", got, tc.fpsNum, tc.fpsDen)
			}
		})
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
		if got[i] != want[i] || renderSafeImageMotions[i] != want[i] {
			t.Fatalf("image motion %d = %q / %q, want catalog contract %q", i, got[i], renderSafeImageMotions[i], want[i])
		}
	}
	// The default selector is directionally coherent: it walks the dominant-
	// direction and neutral motions of the run, never the opposite side. Its
	// coverage must be complete over that coherent pool, not the raw catalog.
	const runSeed = "image-catalog-32"
	coherent := coherentMotionPool(renderSafeImageMotions, runSeed)
	selected := make(map[string]bool, len(coherent))
	for ordinal := range coherent {
		id := selectImageMotion(runSeed, "run", ordinal, nil)
		if !containsString(coherent, id) || selected[id] {
			t.Fatalf("single-image selector repeated or emitted a motion outside the coherent pool at %d: %q", ordinal, id)
		}
		selected[id] = true
	}
	if len(selected) != len(coherent) {
		t.Fatalf("single-image selector covered %d motions, want all %d coherent motions", len(selected), len(coherent))
	}
	opposite := oppositeDirection(CoherenceDominantDirection(runSeed))
	for id := range selected {
		if motionHorizontalDirection(id) == opposite {
			t.Fatalf("coherent selector emitted opposite-direction motion %q", id)
		}
	}
	for _, id := range renderSafeImageMotions {
		if !containsString(renderSafeImageMotions, id) && selected[id] {
			t.Fatalf("single-image default still selects old motion %q", id)
		}
	}
	// SelectImageMotionAt is the map-compatible selector: it rotates only the
	// centered subset, never the cross-canvas motions of the full catalog.
	// Generated entity images use the separately curated restrained subset.
	selected = make(map[string]bool, len(centeredImageMotionCandidates))
	for ordinal := range centeredImageMotionCandidates {
		id := SelectImageMotionAt("image-catalog-18", "run", ordinal)
		if !containsString(centeredImageMotionCandidates, id) || selected[id] {
			t.Fatalf("public image selector escaped the centered pool at %d: %q", ordinal, id)
		}
		selected[id] = true
	}
	selected = make(map[string]bool, len(generatedEntityImageMotionCandidates))
	for ordinal := range generatedEntityImageMotionCandidates {
		id := SelectEntityImageMotionAt("image-catalog-32", "run", ordinal)
		if !containsString(generatedEntityImageMotionCandidates, id) || selected[id] {
			t.Fatalf("generated-image selector repeated or emitted an unknown motion at %d: %q", ordinal, id)
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
		if !containsString(renderSafeImageMotions, item.MotionID) || item.PresetID == "" {
			t.Fatalf("image item %q must use a new single-image motion and image preset, preset=%q motion=%q", item.ID, item.PresetID, item.MotionID)
		}
		imageCount++
	}
	if imageCount != 2 {
		t.Fatalf("planned %d images, want 2", imageCount)
	}
	input.ImageMotions = renderSafeImageMotions[:2]
	plan, err = BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes))
	if err != nil {
		t.Fatalf("certified explicit image motion pool: %v", err)
	}
	for _, item := range plan.Items {
		if item.Kind == "image" && !containsString(input.ImageMotions, item.MotionID) {
			t.Fatalf("image motion %q escaped the explicit pool %v", item.MotionID, input.ImageMotions)
		}
	}
	input.ImageMotions = []string{"image_focus_in"}
	if _, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes)); err == nil {
		t.Fatal("explicit legacy image motion should be rejected for a single-image plan")
	}
}

// TestRandomImageMotionOffsetRotatesThroughAllCertifiedIDs pins map rotation
// over the centered pool: the raster remains registered with projected pins.
// The plan's random offset may start anywhere in the largest generated pool's
// span; the centered pool reduces it modulo its own size and must cover every id.
func TestRandomImageMotionOffsetRotatesThroughAllCertifiedIDs(t *testing.T) {
	count := len(centeredImageMotionCandidates)
	if count != 4 {
		t.Fatalf("centered map-safe motions = %d, want 4", count)
	}
	span := max(len(generatedEntityImageMotionCandidates), len(generatedEntityCaptionMotionCandidates), len(centeredImageMotionCandidates))
	for attempt := 0; attempt < 4; attempt++ {
		offset, err := RandomImageMotionOffset()
		if err != nil {
			t.Fatalf("random image motion offset: %v", err)
		}
		if offset < 0 || offset >= span {
			t.Fatalf("random offset = %d, outside [0,%d)", offset, span)
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

// TestEntityImageMotionRotationCoversThePhotoSafeCatalog keeps generated
// portraits varied while excluding image recipes that can introduce collage
// fragments or perspective artifacts.
func TestEntityImageMotionRotationCoversThePhotoSafeCatalog(t *testing.T) {
	pool := CertifiedEntityImageMotions()
	if len(pool) == 0 {
		t.Fatal("generated entity-image rotation pool is empty")
	}
	for i, id := range pool {
		if !containsString(renderSafeImageMotions, id) {
			t.Fatalf("pool entry %d = %q, not in the certified image catalog", i, id)
		}
	}
	// The copy must not alias the package storage.
	pool[0] = "mutated"
	if generatedEntityImageMotionCandidates[0] == "mutated" {
		t.Fatal("CertifiedEntityImageMotions returned an alias of the internal pool")
	}

	for attempt := 0; attempt < 4; attempt++ {
		offset, err := RandomImageMotionOffset()
		if err != nil {
			t.Fatalf("random image motion offset: %v", err)
		}
		seen := make(map[string]bool, len(pool))
		for ordinal := 0; ordinal < len(pool); ordinal++ {
			id := EntityImageMotionAtOffset(offset, ordinal)
			if !containsString(CertifiedImageMotions(), id) || seen[id] {
				t.Fatalf("offset %d ordinal %d emitted motion %q outside the certified catalog or repeated", offset, ordinal, id)
			}
			seen[id] = true
		}
		if len(seen) != len(pool) {
			t.Fatalf("offset %d covered %d motions, want all %d", offset, len(seen), len(pool))
		}
		// A retry with the same offset is bit-identical.
		if again := EntityImageMotionAtOffset(offset, 3); again != EntityImageMotionAtOffset(offset, 3) {
			t.Fatalf("entity image motion selection is not deterministic: %q", again)
		}
	}
}

func canonicalSelectableCaptionMotionIDs(t *testing.T) map[string]bool {
	t.Helper()
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
			ID       string   `json:"id"`
			Category string   `json:"category"`
			Targets  []string `json:"targets"`
			Tracks   []struct {
				Property  string            `json:"property"`
				Keyframes []json.RawMessage `json:"keyframes"`
			} `json:"tracks"`
			TextAnimators []struct {
				Properties []struct {
					Property  string            `json:"property"`
					Keyframes []json.RawMessage `json:"keyframes"`
				} `json:"properties"`
			} `json:"text_animators"`
		} `json:"motions"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	// Entity cards are restricted to the ten authored entity_card_v1 presets;
	// other caption families are intentionally not selected for them.
	categories := map[string]bool{"entity_card_v1": true}
	ids := map[string]bool{}
	for _, definition := range document.Motions {
		if !categories[definition.Category] || !(containsString(definition.Targets, "text") || containsString(definition.Targets, "entity")) {
			continue
		}
		if containsString(definition.Targets, "important_phrase") &&
			!containsString(definition.Targets, "caption") && !containsString(definition.Targets, "entity") {
			continue
		}
		animated := false
		for _, track := range definition.Tracks {
			animated = animated || track.Property != "" && len(track.Keyframes) > 0
		}
		for _, animator := range definition.TextAnimators {
			for _, property := range animator.Properties {
				animated = animated || property.Property != "" && len(property.Keyframes) > 0
			}
		}
		if animated {
			ids[definition.ID] = true
		}
	}
	return ids
}

func TestGeneratedEntityCaptionMotionsMatchCanonicalEntityPresentationCatalog(t *testing.T) {
	pool := CertifiedEntityCaptionMotions()
	if len(pool) != len(generatedEntityCaptionMotionCandidates) {
		t.Fatalf("generated entity-caption motion pool = %d motions, want %d entity_card_v1 presets", len(pool), len(generatedEntityCaptionMotionCandidates))
	}
	want := canonicalSelectableCaptionMotionIDs(t)
	poolSet := make(map[string]bool, len(pool))
	for _, id := range pool {
		if poolSet[id] {
			t.Fatalf("caption motion pool contains duplicate %q", id)
		}
		poolSet[id] = true
	}
	if !reflect.DeepEqual(poolSet, want) {
		t.Fatalf("caption motion pool = %v, want exact entity_card_v1 catalog %v", poolSet, want)
	}
	for offset := 0; offset < len(pool); offset++ {
		seen := map[string]bool{}
		for ordinal := 0; ordinal < len(pool); ordinal++ {
			id := EntityCaptionMotionAtOffset(offset, ordinal)
			if !poolSet[id] || seen[id] {
				t.Fatalf("caption offset %d ordinal %d emitted unknown/repeated motion %q", offset, ordinal, id)
			}
			seen[id] = true
		}
	}
	catalogIDs := canonicalSelectableCaptionMotionIDs(t)
	catalogSet := make(map[string]bool, len(catalogIDs))
	for id := range catalogIDs {
		catalogSet[id] = true
	}
	if !reflect.DeepEqual(catalogSet, poolSet) {
		t.Fatalf("generated entity captions %v differ from canonical entity_card_v1 %v", poolSet, catalogSet)
	}
}

func TestGeneratedEntityImageWithTextPoolsAreDiverseAndDistinct(t *testing.T) {
	imagePool := CertifiedImageWithTextMotions()
	captionPool := CertifiedEntityCaptionMotions()
	if len(imagePool) != len(generatedEntityImageMotionCandidates) || len(imagePool) < 5 {
		t.Fatalf("image-with-text motions = %v; expected the complete generated entity image pool", imagePool)
	}
	if !reflect.DeepEqual(imagePool, generatedEntityImageMotionCandidates) {
		t.Fatalf("image-with-text pool = %v, want complete entity image pool %v", imagePool, generatedEntityImageMotionCandidates)
	}
	if len(captionPool) != len(generatedEntityCaptionMotionCandidates) {
		t.Fatalf("entity caption motions = %d, want the complete %d-motion entity_card_v1 pool", len(captionPool), len(generatedEntityCaptionMotionCandidates))
	}
	seenImage, seenCaption := map[string]bool{}, map[string]bool{}
	rotationCount := min(len(imagePool), len(captionPool))
	for ordinal := 0; ordinal < rotationCount; ordinal++ {
		image := ImageWithTextMotionAtOffset(0, ordinal, 0)
		caption := EntityCaptionMotionAtOffset(0, ordinal)
		if !containsString(imagePool, image) || seenImage[image] {
			t.Fatalf("image-with-text ordinal %d repeats/outside pool: %q", ordinal, image)
		}
		if !containsString(captionPool, caption) || seenCaption[caption] {
			t.Fatalf("entity-caption ordinal %d repeats/outside entity family: %q", ordinal, caption)
		}
		seenImage[image], seenCaption[caption] = true, true
	}
	for _, image := range imagePool {
		if containsString(captionPool, image) {
			t.Fatalf("image motion %q was incorrectly used as a caption treatment", image)
		}
	}
}

func TestGeneratedEntityCaptionRotationAndCatalogStayInSync(t *testing.T) {
	pool := CertifiedEntityCaptionMotions()
	if len(pool) != len(generatedEntityCaptionMotionCandidates) {
		t.Fatalf("generated entity-caption motion pool = %d motions, want %d entity_card_v1 presets", len(pool), len(generatedEntityCaptionMotionCandidates))
	}
	poolSet := make(map[string]bool, len(pool))
	for _, id := range pool {
		if poolSet[id] {
			t.Fatalf("caption motion pool contains duplicate %q", id)
		}
		poolSet[id] = true
	}
	want := canonicalSelectableCaptionMotionIDs(t)
	for offset := 0; offset < len(pool); offset++ {
		seen := map[string]bool{}
		for ordinal := 0; ordinal < len(pool); ordinal++ {
			id := EntityCaptionMotionAtOffset(offset, ordinal)
			if !poolSet[id] || seen[id] {
				t.Fatalf("caption offset %d ordinal %d emitted unknown/repeated motion %q", offset, ordinal, id)
			}
			seen[id] = true
		}
		if again := EntityCaptionMotionAtOffset(offset, 2); again != EntityCaptionMotionAtOffset(offset, 2) {
			t.Fatalf("caption motion selection changed across retries: %q", again)
		}
	}
	catalogSet := make(map[string]bool, len(want))
	for id := range want {
		catalogSet[id] = true
	}
	if !reflect.DeepEqual(catalogSet, poolSet) {
		t.Fatalf("generated captions %v differ from certified selectable caption catalog %v", poolSet, catalogSet)
	}
	if !reflect.DeepEqual(poolSet, want) {
		t.Fatalf("generated caption family = %v, want catalog-backed selection %v", poolSet, want)
	}
}

func TestGeneratedImageMotionPoolMatchesCanonicalChrononCatalog(t *testing.T) {
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
	safeSet := make(map[string]bool, len(renderSafeImageMotions))
	for _, id := range renderSafeImageMotions {
		safeSet[id] = true
	}
	for _, definition := range document.Motions {
		if definition.Category == "image_25d_clean_v1" || definition.Category == "overlay_v3_image" || definition.Category == "editorial_image_v1" {
			catalogIDs = append(catalogIDs, definition.ID)
			if safeSet[definition.ID] {
				selectedIDs = append(selectedIDs, definition.ID)
				for _, track := range definition.Tracks {
					switch track.Property {
					case "opacity", "scale", "scale_x", "scale_y", "position_x", "position_y", "position_z", "rotation_x", "rotation_y", "rotation_z", "blur":
					default:
						t.Fatalf("certified image motion %q uses unsupported transform %q", definition.ID, track.Property)
					}
				}
			}
		}
		if _, removed := removedGeneratedPhraseMotions[definition.ID]; removed || !AnimationRendererSupported(definition.ID) {
			continue
		}
		if definition.Category == "apple_v2" || definition.Category == "apple_v3" || definition.Category == "phrase_apple_clean_v1" || definition.Category == "apple_phrase_v1" || definition.Category == "text_3d_v1" || definition.Category == "documentary_highlighting_v1" || (definition.Category == "brush_v1" && strings.HasPrefix(definition.ID, "brush_phrase_")) {
			catalogPhraseIDs = append(catalogPhraseIDs, definition.ID)
		}
	}
	sort.Strings(catalogIDs)
	sort.Strings(selectedIDs)
	sort.Strings(catalogPhraseIDs)
	if len(catalogIDs) == 0 {
		t.Fatal("catalog has no certified layer-only image motions")
	}
	wantSelected := append([]string(nil), renderSafeImageMotions...)
	sort.Strings(wantSelected)
	if len(selectedIDs) != len(wantSelected) {
		t.Fatalf("catalog matches %d selected motions, pool has %d", len(selectedIDs), len(wantSelected))
	}
	for i := range selectedIDs {
		if selectedIDs[i] != wantSelected[i] {
			t.Fatalf("selected image motion %d=%q, catalog=%q", i, selectedIDs[i], wantSelected[i])
		}
	}
	callable := excludeRemovedGeneratedPhraseMotions(phraseMotionCandidates)
	sort.Strings(callable)
	if len(callable) != len(catalogPhraseIDs) {
		t.Fatalf("catalog has %d callable phrase motions, callable pool has %d (removed motions excluded)", len(catalogPhraseIDs), len(callable))
	}
	for i := range catalogPhraseIDs {
		if catalogPhraseIDs[i] != callable[i] {
			t.Fatalf("catalog phrase motion %d=%q, callable pool=%q", i, catalogPhraseIDs[i], callable[i])
		}
	}
}
