package overlays

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestSelectEntityImagePresetUsesOnlyRenderSafeCandidates pins the generated
// entity-image preset contract: every selection comes from the render-safe
// candidate set (never modern_rounded_pop, whose native mask path is not
// implemented), stays bit-identical for a retry of the same job, and still
// varies across identities so one run does not render five identical motions.
func TestSelectEntityImagePresetUsesOnlyRenderSafeCandidates(t *testing.T) {
	safe := make(map[string]bool, len(imagePresetCandidates))
	for _, id := range imagePresetCandidates {
		safe[id] = true
	}
	if safe["modern_rounded_pop"] {
		t.Fatal("modern_rounded_pop must not be selectable for generated entity images")
	}

	variants := map[string]bool{}
	for i := 0; i < 64; i++ {
		jobID := fmt.Sprintf("job-%d", i)
		itemID := fmt.Sprintf("overlay-scene-0-entity-%d", i)
		preset := SelectEntityImagePreset(jobID, "scene-0", itemID)
		if !safe[preset] {
			t.Fatalf("selected preset %q is not in the render-safe image candidate set", preset)
		}
		// A retry of the same job must resolve to the same preset.
		if again := SelectEntityImagePreset(jobID, "scene-0", itemID); again != preset {
			t.Fatalf("preset selection is not deterministic: %q vs %q", preset, again)
		}
		variants[preset] = true
	}
	if len(variants) < 2 {
		t.Fatalf("entity image preset selection never varies across identities: %v", variants)
	}
}

// TestGeneratedImageAnimationSelectorUsesOnlyTheNewSingleImagePool pins the
// single-image contract: generated overlays rotate over the COMPLETE
// 32-motion render-safe catalog (the IDs RenderingGen's runtime catalog
// contract verifies), stay deterministic per item and never emit an id
// outside that pool.
func TestGeneratedImageAnimationSelectorUsesOnlyTheNewSingleImagePool(t *testing.T) {
	pool := CertifiedSingleImageMotions()
	if len(pool) != 32 {
		t.Fatalf("certified single-image pool has %d motions, want 32", len(pool))
	}
	allowed := make(map[string]bool, len(pool))
	for _, id := range pool {
		allowed[id] = true
	}
	for i := 0; i < 256; i++ {
		jobID := fmt.Sprintf("image-job-%d", i)
		itemID := fmt.Sprintf("image-item-%d", i)
		motion := SelectImageAnimation(jobID, "scene", itemID)
		if !allowed[motion] {
			t.Fatalf("image animation selector emitted uncertified motion %q", motion)
		}
		if again := SelectImageAnimation(jobID, "scene", itemID); again != motion {
			t.Fatalf("image animation is not deterministic for the same item: %q vs %q", motion, again)
		}
	}
	for _, id := range renderSafeImageMotions {
		if !allowed[id] {
			t.Fatalf("render-safe motion %q is missing from the certified single-image pool", id)
		}
	}
	if len(renderSafeImageMotions) != 32 {
		t.Fatalf("render-safe catalog has %d motions, want 32", len(renderSafeImageMotions))
	}
}

func TestGeneratedTextOverlaysStayOnTheRenderSafeTextContract(t *testing.T) {
	officialTextPreset := map[string]bool{"static_text_smoke": true, "phrase_default": true}
	for _, id := range append(append([]string{}, namePresetRenderSafeCandidates...),
		append(append([]string{}, phrasePresetCandidates...), wordPresetCandidates...)...) {
		if !officialTextPreset[id] {
			t.Fatalf("text preset candidate %q is not an official RenderingGen preset", id)
		}
	}

	motions := RenderSafeTextMotions()
	if len(motions) == 0 {
		t.Fatal("no render-safe text motions; every generated text overlay would render statically")
	}
	// The animator families below cannot lower on the native kernel: naming one
	// here means the render fails closed (and, with a glow, crashes the render).
	for _, id := range motions {
		for _, banned := range []string{
			"kinetic_split_word", "masked_upward_reveal", "staggered_char_float",
			"soft_edge_spotlight_dissolve", "velocity_inertia_snap", "apple_phrase_v2",
			"character_cascade", "word_reveal", "char_wave",
		} {
			if id == banned {
				t.Fatalf("motion %q needs a text-animator stack the native lane rejects", id)
			}
		}
	}

	scenes := []SceneInput{{
		ID:       "scene-1",
		Phrases:  []TimedAnnotation{{Text: "a grounded important phrase", StartMs: 0, EndMs: 1000, StartUS: 0, DurationUS: 1_000_000, Score: 1}},
		Quotes:   []TimedAnnotation{{Text: "a grounded quote", StartMs: 0, EndMs: 1000, StartUS: 0, DurationUS: 1_000_000, Score: 1}},
		Keywords: []TimedAnnotation{{Text: "KEYWORD", StartMs: 0, EndMs: 1000, StartUS: 0, DurationUS: 1_000_000, Score: 1}},
		Numbers:  []TimedAnnotation{{Text: "42", StartMs: 0, EndMs: 1000, StartUS: 0, DurationUS: 1_000_000, Score: 1}},
	}}
	plan, err := BuildPlan(PlanInput{
		PlanID: "render-safe", VideoID: "video-1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Scenes: scenes,
	}, AllCandidatesPlannerConfig(scenes))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	safe := map[string]bool{}
	for _, id := range motions {
		safe[id] = true
	}
	textItems := 0
	for _, item := range plan.Items {
		if item.Text == "" {
			continue
		}
		textItems++
		if item.MotionID == "" {
			t.Fatalf("text item %q carries no explicit motion: the preset's own glyph motion would be transported", item.ID)
		}
		if item.Kind == "text_phrase" {
			if !containsString(CertifiedPhraseMotions(), item.MotionID) {
				t.Fatalf("phrase item %q motion %q is outside the callable phrase catalog", item.ID, item.MotionID)
			}
		} else if !safe[item.MotionID] {
			t.Fatalf("non-phrase text item %q motion %q is outside the render-safe pool %v", item.ID, item.MotionID, motions)
		}
	}
	if textItems == 0 {
		t.Fatal("no text items in the plan; the fixture stopped exercising the contract")
	}
}

func TestDateAndMetricEntityTypesRouteToCertifiedPresentationTemplates(t *testing.T) {
	cases := []struct {
		typeName, template string
		motions            []string
	}{
		{"DATE", "TIMELINE_DATE_CARD", DatePresentationMotionCandidates()},
		{"TIME", "TIMELINE_DATE_CARD", DatePresentationMotionCandidates()},
		{"METRIC", "METRIC_STAT_CARD", MetricPresentationMotionCandidates()},
		{"STATISTIC", "METRIC_STAT_CARD", MetricPresentationMotionCandidates()},
		{"NUMBER", "METRIC_STAT_CARD", MetricPresentationMotionCandidates()},
	}
	items := make([]TimedAnnotation, len(cases))
	for index, tc := range cases {
		items[index] = TimedAnnotation{
			Text: fmt.Sprintf("value-%d", index), Type: tc.typeName,
			StartMs: int64(index * 3000), EndMs: int64(index*3000 + 3000),
			StartUS: int64(index) * 3_000_000, DurationUS: 3_000_000, Score: 1,
		}
	}
	scenes := []SceneInput{{ID: "scene", Numbers: items}}
	plan, err := BuildPlan(PlanInput{
		PlanID: "presentation-routing", VideoID: "video", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: scenes,
	}, AllCandidatesPlannerConfig(scenes))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != len(cases) {
		t.Fatalf("routed value items=%d, want %d: %+v", len(plan.Items), len(cases), plan.Items)
	}
	byText := make(map[string]OverlayItem, len(plan.Items))
	for _, item := range plan.Items {
		byText[item.Text] = item
	}
	for index, tc := range cases {
		item := byText[fmt.Sprintf("value-%d", index)]
		if item.TemplateID != tc.template {
			t.Errorf("%s template=%q, want %q", tc.typeName, item.TemplateID, tc.template)
		}
		if item.Kind != "number" {
			t.Errorf("%s kind=%q, want the established number budget kind", tc.typeName, item.Kind)
		}
		if !containsString(tc.motions, item.MotionID) {
			t.Errorf("%s motion=%q is outside its certified catalog pool %v", tc.typeName, item.MotionID, tc.motions)
		}
		wantDurationUS := int64(3_000_000)
		if tc.typeName == "DATE" || tc.typeName == "TIME" {
			wantDurationUS = 5_000_000
		}
		if item.StartUS != int64(index)*3_000_000 || item.DurationUS != wantDurationUS {
			t.Errorf("%s lost certified timing: start_us=%d duration_us=%d", tc.typeName, item.StartUS, item.DurationUS)
		}
		if tc.template == "TIMELINE_DATE_CARD" && item.Params["font_size_px"] != 140.0 {
			t.Errorf("%s font_size_px=%v, want shared text size +28px", tc.typeName, item.Params["font_size_px"])
		}
		if tc.template == "METRIC_STAT_CARD" && item.Params["font_size_px"] != 140.0 {
			t.Errorf("%s font_size_px=%v, want shared text size +28px", tc.typeName, item.Params["font_size_px"])
		}
	}
}

func TestDateAndMetricPresentationPoolsAreCuratedCanonicalSubsets(t *testing.T) {
	wantDates := []string{
		"date_fade_rise", "date_calendar_flip", "date_timeline_tick", "date_chronology_focus",
		"date_page_turn", "date_calendar_drop", "date_month_wipe", "date_era_zoom",
	}
	wantMetrics := []string{
		"metric_counter_scale_settle", "metric_odometer_vertical", "metric_digits_stagger",
		"metric_delta_reveal", "metric_focus_punch", "metric_before_after",
		"metric_count_flip", "metric_split_odometer",
	}
	if !reflect.DeepEqual(DatePresentationMotionCandidates(), wantDates) {
		t.Fatalf("date premium pool=%v, want curated date_v1 subset %v", DatePresentationMotionCandidates(), wantDates)
	}
	if !reflect.DeepEqual(MetricPresentationMotionCandidates(), wantMetrics) {
		t.Fatalf("metric premium pool=%v, want curated metric_v1 subset %v", MetricPresentationMotionCandidates(), wantMetrics)
	}
}

func TestDateAndMetricPresentationPoolsMatchChrononTemplateCatalog(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var catalogPath string
	for dir := root; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "ChrononTemplate", "catalog", "entity_presentation.v1.json")
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
		t.Fatal("could not locate ChrononTemplate's canonical entity presentation catalog")
	}
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Families []struct {
			ID      string `json:"id"`
			Presets []struct {
				ID string `json:"id"`
			} `json:"presets"`
		} `json:"families"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	familyIDs := map[string][]string{}
	for _, family := range catalog.Families {
		for _, preset := range family.Presets {
			familyIDs[family.ID] = append(familyIDs[family.ID], preset.ID)
		}
	}
	for _, tc := range []struct {
		family string
		pool   []string
	}{{"date_v1", DatePresentationMotionCandidates()}, {"metric_v1", MetricPresentationMotionCandidates()}} {
		canonical := make(map[string]bool, len(familyIDs[tc.family]))
		for _, id := range familyIDs[tc.family] {
			canonical[id] = true
		}
		if len(canonical) != 20 {
			t.Fatalf("ChrononTemplate %s catalog contains %d motions, want 20", tc.family, len(canonical))
		}
		for _, id := range tc.pool {
			if !canonical[id] {
				t.Errorf("%s selected motion %q outside ChrononTemplate's canonical family", tc.family, id)
			}
		}
	}
}

func TestPresentationSamplerUsesCuratedMotionsWithStablePerValueVariation(t *testing.T) {
	pool := DatePresentationMotionCandidates()
	seen := make(map[string]bool, len(pool))
	for i := 0; i < 512; i++ {
		jobID := fmt.Sprintf("date-job-%d", i/16)
		sceneID := fmt.Sprintf("scene-%d", i/8)
		itemID := fmt.Sprintf("date-item-%d", i)
		_, first := NumberPresentationForEntityType(jobID, sceneID, itemID, "DATE")
		_, retry := NumberPresentationForEntityType(jobID, sceneID, itemID, "DATE")
		if !containsString(pool, first) {
			t.Fatalf("date %q selected motion %q outside date_v1 catalog", itemID, first)
		}
		if retry != first {
			t.Fatalf("retry changed date %q motion: %q != %q", itemID, first, retry)
		}
		seen[first] = true
	}
	if len(seen) != len(pool) {
		t.Fatalf("distinct dates covered %d of %d curated date_v1 motions: %v", len(seen), len(pool), seen)
	}
	metricSeen := make(map[string]bool, len(MetricPresentationMotionCandidates()))
	for i := 0; i < 512; i++ {
		jobID := fmt.Sprintf("metric-job-%d", i/16)
		sceneID := fmt.Sprintf("scene-%d", i/8)
		itemID := fmt.Sprintf("metric-item-%d", i)
		_, first := NumberPresentationForEntityType(jobID, sceneID, itemID, "METRIC")
		_, retry := NumberPresentationForEntityType(jobID, sceneID, itemID, "METRIC")
		if !containsString(MetricPresentationMotionCandidates(), first) {
			t.Fatalf("metric %q selected motion %q outside curated metric_v1 pool", itemID, first)
		}
		if retry != first {
			t.Fatalf("retry changed metric %q motion: %q != %q", itemID, first, retry)
		}
		metricSeen[first] = true
	}
	if len(metricSeen) != len(MetricPresentationMotionCandidates()) {
		t.Fatalf("distinct metrics covered %d of %d curated metric_v1 motions", len(metricSeen), len(MetricPresentationMotionCandidates()))
	}
	for i := 0; i < len(pool); i++ {
		itemID := fmt.Sprintf("unique-date-%d", i)
		_, a := NumberPresentationForEntityType("job-per-date", "scene-per-date", itemID, "DATE")
		_, b := NumberPresentationForEntityType("job-per-date", "scene-per-date", itemID, "DATE")
		if a != b {
			t.Fatalf("same date identity changed motion across retries: %q != %q", a, b)
		}
	}
}

func TestPresentationTemplateRoutingCoversTypedValueClasses(t *testing.T) {
	for _, tc := range []struct{ entityType, want string }{
		{"DATE", "TIMELINE_DATE_CARD"}, {"TIME", "TIMELINE_DATE_CARD"},
		{"NUMBER", "METRIC_STAT_CARD"}, {"MONEY", "METRIC_STAT_CARD"},
		{"PERCENTAGE", "METRIC_STAT_CARD"}, {"METRIC", "METRIC_STAT_CARD"},
		{"STATISTIC", "METRIC_STAT_CARD"}, {"CONCEPT", ""},
	} {
		if got := NumberPresentationTemplateForEntityType(tc.entityType); got != tc.want {
			t.Errorf("presentation template for %s=%q, want %q", tc.entityType, got, tc.want)
		}
	}
}

func TestPresetSelectionIsDeterministicAndUsesKnownFamilies(t *testing.T) {
	first := SelectEntityNamePreset("job-1", "scene-1", "entity-1", "PERSON")
	second := SelectEntityNamePreset("job-1", "scene-1", "entity-1", "PERSON")
	if first == "" || first != second {
		t.Fatalf("entity preset selection is not deterministic: %q vs %q", first, second)
	}

	plan, err := BuildPlan(PlanInput{
		PlanID: "job-1", VideoID: "video-1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{
			ID:       "scene-1",
			Phrases:  []TimedAnnotation{{Text: "IMPORTANT", StartMs: 0, EndMs: 1000}},
			Keywords: []TimedAnnotation{{Text: "NOW", StartMs: 0, EndMs: 1000}},
			Images:   []ImageCandidate{{AssetID: "img-1", URL: "assets/img.png", StartMs: 0, EndMs: 1000}},
		}},
	}, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(plan.Items))
	}
	for _, item := range plan.Items {
		if item.PresetID == "" {
			t.Fatalf("item %q did not receive a preset", item.ID)
		}
	}
}
