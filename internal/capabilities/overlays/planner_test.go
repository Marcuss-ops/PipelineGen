package overlays

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildPlanAppliesConservativeLimitsAndRanks(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "p1", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{
			ID: "scene-1",
			Phrases: []TimedAnnotation{
				{Text: "too many words for this phrase to be eligible", StartMs: 1, EndMs: 2, Score: 10},
				{Text: "This changes everything", StartMs: 100, EndMs: 900, Score: 2},
				{Text: "Lower priority", StartMs: 1000, EndMs: 1200, Score: 1},
			},
			Keywords: []TimedAnnotation{
				{Text: "warning", StartMs: 200, EndMs: 400, Score: 3},
				{Text: "exclusive", StartMs: 400, EndMs: 600, Score: 2},
				{Text: "retired", StartMs: 600, EndMs: 800, Score: 1},
				{Text: "ignored", StartMs: 800, EndMs: 900, Score: 0},
			},
			Images: []ImageCandidate{{AssetID: "img-1", SHA256: "hash", StartMs: 300, EndMs: 1000, Score: 1}},
		}}}, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 5 {
		t.Fatalf("items = %d, want image + 3 keywords + phrase", len(plan.Items))
	}
	// Z-index order (bottom → top): image first, keywords middle, phrase last.
	if plan.Items[0].Kind != "image" || plan.Items[0].AssetRefs[0].AssetID != "img-1" {
		t.Fatalf("image must be first (z=20): %+v", plan.Items[0])
	}
	if plan.Items[1].Text != "warning" || plan.Items[3].Text != "retired" {
		t.Fatalf("keyword ranking = %+v", plan.Items)
	}
	if plan.Items[4].Kind != "text_phrase" || plan.Items[4].Text != "This changes everything" {
		t.Fatalf("phrase must be last (z=100): %+v", plan.Items[4])
	}
}

func TestBuildPlanNeverInventsTiming(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "p1", VideoID: "v1", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene-1", Phrases: []TimedAnnotation{{Text: "No timing"}}}},
	}, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 0 {
		t.Fatalf("planner invented items without timing: %+v", plan.Items)
	}
}

func TestAllCandidatesPlannerConfigKeepsOnlyEditorialImagesAndPhrases(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "all-candidates", VideoID: "video-all-candidates", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{
			ID: "scene-1",
			Phrases: []TimedAnnotation{
				{Text: "a very long phrase that is still spoken and must be rendered", StartMs: 100, EndMs: 600, Score: 1},
				{Text: "second phrase", StartMs: 100, EndMs: 600, Score: 0.9},
			},
			Keywords: []TimedAnnotation{{Text: "KEYWORD", StartMs: 100, EndMs: 600}},
			Images:   []ImageCandidate{{AssetID: "image-1", SHA256: "hash", StartMs: 100, EndMs: 600}},
		}},
	}, AllCandidatesPlannerConfig([]SceneInput{{
		ID: "scene-1",
		Phrases: []TimedAnnotation{
			{Text: "a very long phrase that is still spoken and must be rendered", StartMs: 100, EndMs: 600},
			{Text: "second phrase", StartMs: 100, EndMs: 600},
		},
		Keywords: []TimedAnnotation{{Text: "KEYWORD", StartMs: 100, EndMs: 600}},
		Images:   []ImageCandidate{{AssetID: "image-1", SHA256: "hash", StartMs: 100, EndMs: 600}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 3 {
		t.Fatalf("items = %d, want 2 phrases + 1 image; non-editorial keyword must be excluded: %+v", len(plan.Items), plan.Items)
	}
}

func TestBuildPlanAppliesRunLevelPhraseBudgetAcrossScenes(t *testing.T) {
	scenes := []SceneInput{
		{ID: "scene-1", Phrases: []TimedAnnotation{
			{Text: "Alpha phrase", StartMs: 100, EndMs: 300, Score: 0.6},
			{Text: "Shared phrase", StartMs: 400, EndMs: 600, Score: 0.2},
			{Text: "Bravo phrase", StartMs: 700, EndMs: 900, Score: 0.5},
		}},
		{ID: "scene-2", Phrases: []TimedAnnotation{
			{Text: "SHARED   PHRASE", StartMs: 100, EndMs: 300, Score: 0.95},
			{Text: "Charlie phrase", StartMs: 400, EndMs: 600, Score: 0.4},
			{Text: "Delta phrase", StartMs: 700, EndMs: 900, Score: 0.3},
			{Text: "Echo phrase", StartMs: 1000, EndMs: 1200, Score: 0.1},
		}},
	}
	plan, err := BuildPlan(PlanInput{
		PlanID: "global-phrase-budget", VideoID: "video-global-phrase-budget",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, Scenes: scenes,
	}, AllCandidatesPlannerConfig(scenes))
	if err != nil {
		t.Fatal(err)
	}

	phrases := make([]OverlayItem, 0, MaxPhraseOverlaysPerRun)
	for _, item := range plan.Items {
		if item.Kind == "text_phrase" {
			phrases = append(phrases, item)
		}
	}
	if len(phrases) != MaxPhraseOverlaysPerRun {
		t.Fatalf("phrase overlays = %d, want global cap %d: %+v", len(phrases), MaxPhraseOverlaysPerRun, phrases)
	}
	seen := map[string]bool{}
	for _, item := range phrases {
		key := strings.ToLower(strings.Join(strings.Fields(item.Text), " "))
		if seen[key] {
			t.Fatalf("duplicate phrase survived global dedupe: %q", item.Text)
		}
		seen[key] = true
	}
	if !seen["shared phrase"] || !seen["alpha phrase"] || !seen["bravo phrase"] || !seen["charlie phrase"] || !seen["delta phrase"] || seen["echo phrase"] {
		t.Fatalf("run-level rank/dedupe chose wrong phrases: %+v", phrases)
	}
}

func TestApplyPhraseOverlayBudgetReportsShortfallWithoutInventingItems(t *testing.T) {
	items := []OverlayItem{
		{ID: "phrase-1", Kind: "text_phrase", Text: "Grounded phrase", Params: map[string]any{"priority": 0.9}},
		{ID: "phrase-2", Kind: "text_phrase", Text: "  GROUNDED   PHRASE ", Params: map[string]any{"priority": 0.2}},
		{ID: "keyword-1", Kind: "keyword", Text: "keyword"},
	}
	got, budget := ApplyPhraseOverlayBudget(items)
	if len(got) != 2 || got[0].ID != "phrase-1" || got[1].ID != "keyword-1" {
		t.Fatalf("budgeted items = %+v, want one grounded phrase and the non-phrase item", got)
	}
	if budget.Requested != 5 || budget.Materialized != 1 || budget.Shortfall != 4 {
		t.Fatalf("phrase budget = %+v, want requested=5 materialized=1 shortfall=4", budget)
	}
}

func TestApplyEditorialOverlayBudgetEnforcesRunLevelFivePlusFive(t *testing.T) {
	items := make([]OverlayItem, 0, 15)
	for i := 0; i < 7; i++ {
		items = append(items, OverlayItem{
			ID:        fmt.Sprintf("image-%d", i),
			Kind:      "entity_image",
			AssetRefs: []OverlayAssetRef{{AssetID: fmt.Sprintf("asset-%d", i), SHA256: fmt.Sprintf("hash-%d", i)}},
			Params:    map[string]any{"priority": float64(i)},
		})
	}
	for i := 0; i < 7; i++ {
		items = append(items, OverlayItem{
			ID:     fmt.Sprintf("phrase-%d", i),
			Kind:   "text_phrase",
			Text:   fmt.Sprintf("Grounded phrase %d", i),
			Params: map[string]any{"priority": float64(i)},
		})
	}
	items = append(items, OverlayItem{ID: "location-1", Kind: "location", Text: "Brooklyn"})

	got, budget := ApplyEditorialOverlayBudget(items)
	images, phrases := 0, 0
	for _, item := range got {
		switch item.Kind {
		case "entity_image", "image":
			images++
		case "text_phrase":
			phrases++
		default:
			t.Fatalf("non-editorial overlay survived: %+v", item)
		}
	}
	if images != MaxImageOverlaysPerRun || phrases != MaxPhraseOverlaysPerRun || len(got) != 10 {
		t.Fatalf("image/phrase/total counts = %d/%d/%d, want 5/5/10", images, phrases, len(got))
	}
	if budget != (PhraseOverlayBudget{Requested: 5, Materialized: 5, Shortfall: 0}) {
		t.Fatalf("phrase budget = %+v, want 5 requested and materialized", budget)
	}
	if got[0].ID != "image-2" || got[4].ID != "image-6" || got[5].ID != "phrase-2" || got[9].ID != "phrase-6" {
		t.Fatalf("run-level ranking chose wrong survivors: %+v", got)
	}
}

func TestApplyEditorialOverlayBudgetDoesNotInventPhraseShortfall(t *testing.T) {
	got, budget := ApplyEditorialOverlayBudget([]OverlayItem{
		{ID: "phrase-1", Kind: "text_phrase", Text: "Grounded phrase"},
		{ID: "image-1", Kind: "entity_image", AssetRefs: []OverlayAssetRef{{SHA256: "image-hash"}}},
		{ID: "location-1", Kind: "location", Text: "Brooklyn"},
	})
	if len(got) != 2 || got[0].ID != "phrase-1" || got[1].ID != "image-1" {
		t.Fatalf("budgeted items = %+v, want only the provided phrase and image", got)
	}
	if budget != (PhraseOverlayBudget{Requested: 5, Materialized: 1, Shortfall: 4}) {
		t.Fatalf("phrase budget = %+v, want requested=5 materialized=1 shortfall=4", budget)
	}
}

func TestBuildPlanClampsImageDurationAndSelectsAnimation(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "image-duration", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene-1", Images: []ImageCandidate{{
			AssetID: "img", URL: "assets/img.png", SHA256: "hash", StartMs: 1_000, EndMs: 20_000,
		}}}},
	}, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(plan.Items))
	}
	item := plan.Items[0]
	if item.EndMs-item.StartMs > MaxImageOverlayDurationMS {
		t.Fatalf("image duration = %dms, want <= %dms", item.EndMs-item.StartMs, MaxImageOverlayDurationMS)
	}
	animation, ok := item.Params["animation"].(map[string]any)
	if !ok || animation["preset"] != SelectImageAnimation("image-duration", "scene-1", item.ID) {
		t.Fatalf("image animation = %#v", item.Params["animation"])
	}
}

func TestBuildPlanAssignsDistinctPhraseMotions(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "mike-tyson-3-phrases", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene-1", Phrases: []TimedAnnotation{
			{Text: "La velocità apre la distanza", StartMs: 100, EndMs: 900, Score: 1},
			{Text: "La pressione mantiene il controllo", StartMs: 1000, EndMs: 1800, Score: 0.9},
			{Text: "La disciplina trasforma la potenza", StartMs: 1900, EndMs: 2700, Score: 0.8},
		}}},
	}, PlannerConfig{MaxPhrases: 3})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		if item.MotionID == "" || seen[item.MotionID] {
			t.Fatalf("phrase motion must be non-empty and distinct: %+v", plan.Items)
		}
		seen[item.MotionID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("got %d distinct phrase motions, want 3: %+v", len(seen), plan.Items)
	}
}

func TestBuildPlanSharesPayloadSelectedPhraseFamilyMotion(t *testing.T) {
	scenes := []SceneInput{{ID: "scene-1", Phrases: []TimedAnnotation{
		{Text: "Prima frase", StartMs: 100, EndMs: 900, Score: 1},
		{Text: "Seconda frase", StartMs: 1000, EndMs: 1800, Score: .9},
		{Text: "Terza frase", StartMs: 1900, EndMs: 2700, Score: .8},
	}}}
	input := PlanInput{PlanID: "family-payload", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		PhraseMotionFamily: "modern_apple", Scenes: scenes}
	first, err := BuildPlan(input, PlannerConfig{MaxPhrases: 3})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlan(input, PlannerConfig{MaxPhrases: 3})
	if err != nil {
		t.Fatal(err)
	}
	chosen := ""
	for i, item := range first.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		if chosen == "" {
			chosen = item.MotionID
		}
		if item.MotionID != chosen || item.MotionID != second.Items[i].MotionID {
			t.Fatalf("phrase motions differ within/reacross payload: first=%q item=%q retry=%q", chosen, item.MotionID, second.Items[i].MotionID)
		}
	}
	if chosen == "" || !strings.HasPrefix(chosen, "phrase_apple_clean_") {
		t.Fatalf("family selected motion %q, want modern_apple", chosen)
	}
}

func TestBuildPlanAssignsDistinctCertifiedMotionsAcrossRunAfterBudget(t *testing.T) {
	scenes := []SceneInput{
		{ID: "scene-0", Phrases: []TimedAnnotation{{Text: "Discipline gave speed a direction", StartMs: 0, EndMs: 900, Score: 0.8}}},
		{ID: "scene-1", Phrases: []TimedAnnotation{{Text: "Twenty years old and champion", StartMs: 1000, EndMs: 1900, Score: 0.9}}},
		{ID: "scene-2", Phrases: []TimedAnnotation{{Text: "Tokyo ended the unbeaten run", StartMs: 2000, EndMs: 2900, Score: 0.7}}},
		{ID: "scene-3", Phrases: []TimedAnnotation{{Text: "A title fight to the eleventh", StartMs: 3000, EndMs: 3900, Score: 0.6}}},
		{ID: "scene-4", Phrases: []TimedAnnotation{{Text: "The rematch ended in disqualification", StartMs: 4000, EndMs: 4900, Score: 0.5}}},
	}
	plan, err := BuildPlan(PlanInput{
		PlanID: "phrases-across-scenes", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: scenes,
	}, AllCandidatesPlannerConfig(scenes))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != MaxPhraseOverlaysPerRun {
		t.Fatalf("phrase overlays = %d, want %d", len(plan.Items), MaxPhraseOverlaysPerRun)
	}
	// The rotation pool is read from its single owner: a test that kept its own
	// copy could pass while the planner rotated over an unrenderable motion.
	allowed := map[string]bool{}
	for _, id := range CertifiedPhraseMotions() {
		allowed[id] = true
	}
	seen := make(map[string]bool, len(plan.Items))
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" {
			t.Fatalf("unexpected non-phrase overlay survived: %+v", item)
		}
		if !allowed[item.MotionID] || seen[item.MotionID] {
			t.Fatalf("phrase motion is not distinct and certified: %+v", plan.Items)
		}
		seen[item.MotionID] = true
	}
}

// TestBuildPlanExtendedEntities pins the NUMBER / QUOTE / PRODUCT / LOGO
// planner path: certified timing only, ranked by score, capped per scene, and
// each item terminating in its canonical template id (the kind→template
// mapping itself is owned by the registry).
func TestBuildPlanExtendedEntities(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "p1", VideoID: "v1", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{
			ID: "scene-1",
			Numbers: []TimedAnnotation{
				{Text: "1.2 million", StartMs: 100, EndMs: 400, Score: 2},
				{Text: "no timing", Score: 9}, // never invented
			},
			Quotes: []TimedAnnotation{
				{Text: "We are just getting started", StartMs: 500, EndMs: 900, Score: 1},
			},
			Products: []ImageCandidate{
				{AssetID: "prod-1", SHA256: "hash", StartMs: 1000, EndMs: 1400, Score: 3},
				{AssetID: "prod-2", SHA256: "hash", StartMs: 1500, EndMs: 1900, Score: 1}, // over cap
			},
			Logos: []ImageCandidate{
				{AssetID: "logo-1", SHA256: "hash", StartMs: 2000, EndMs: 2400, Score: 1},
			},
		}},
	}, PlannerConfig{MaxNumbers: 1, MaxProducts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 4 {
		t.Fatalf("items = %d, want number + quote + product + logo", len(plan.Items))
	}
	byKind := map[string]OverlayItem{}
	for _, item := range plan.Items {
		byKind[item.Kind] = item
	}
	number := byKind["number"]
	if number.TemplateID != "NUMBER" || number.Text != "1.2 million" || number.Params["style"] != "stat" {
		t.Fatalf("number item = %+v", number)
	}
	quote := byKind["quote"]
	if quote.TemplateID != "QUOTE" || quote.Text != "We are just getting started" {
		t.Fatalf("quote item = %+v", quote)
	}
	product := byKind["product"]
	if product.TemplateID != "PRODUCT" || len(product.AssetRefs) != 1 || product.AssetRefs[0].AssetID != "prod-1" {
		t.Fatalf("product item = %+v", product)
	}
	logo := byKind["logo"]
	if logo.TemplateID != "LOGO" || logo.Params["position"] != "corner" {
		t.Fatalf("logo item = %+v", logo)
	}
}

func TestBuildPlanRejectsInvalidImageIdentity(t *testing.T) {
	_, err := BuildPlan(PlanInput{
		PlanID: "p1", VideoID: "v1", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene-1", Images: []ImageCandidate{{StartMs: 1, EndMs: 2}}}},
	}, PlannerConfig{MaxImages: 1})
	if err != nil {
		t.Fatal("invalid image should be skipped, not fail the whole plan:", err)
	}
}

// TestBuildPlanGolden01ImportantPhrase certifies the GOLDEN 01 editorial
// rules for important phrases, all in one deterministic scenario:
//
//   - concept captured: a short important phrase is selected verbatim with
//     its certified timing preserved;
//   - no paragraph copying: a 14-word sentence is dropped (over the word
//     budget), never emitted as a headline;
//   - max 2 per scene: MaxPhrases=2 keeps the top-2 by score;
//   - no duplicates: an identical phrase text is emitted exactly once;
//   - in-scene timing only: empty text and negative/zero timing are dropped,
//     never given guessed timing.
func TestBuildPlanGolden01ImportantPhrase(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "golden-01", VideoID: "video-golden-01", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{
			ID: "scene-1",
			Phrases: []TimedAnnotation{
				// Paragraph copy: 14 words > 8 → dropped.
				{Text: "Apple has just announced a major change to the iPhone and the company says", StartMs: 800, EndMs: 2600, Score: 0.95},
				{Text: "A MAJOR CHANGE", StartMs: 800, EndMs: 2600, Score: 0.9},
				{Text: "THIS COULD CHANGE EVERYTHING", StartMs: 3200, EndMs: 5000, Score: 0.85},
				// Duplicate of the first phrase → deduped.
				{Text: "A MAJOR CHANGE", StartMs: 3200, EndMs: 5000, Score: 0.4},
				// Empty / invalid timing → dropped, never invented.
				{Text: "", StartMs: 100, EndMs: 200, Score: 1.0},
				{Text: "Negative timing", StartMs: -10, EndMs: 200, Score: 1.0},
			},
		}},
	}, PlannerConfig{MaxPhrases: 2, MaxPhraseWords: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 2 {
		t.Fatalf("items = %d, want exactly 2 distinct phrases\n%+v", len(plan.Items), plan.Items)
	}
	first, second := plan.Items[0], plan.Items[1]
	if first.TemplateID != "IMPORTANT_PHRASE" || first.Text != "A MAJOR CHANGE" {
		t.Fatalf("first phrase = %+v, want the short concept (not the paragraph)", first)
	}
	if first.StartMs != 800 || first.EndMs != 2600 {
		t.Fatalf("first phrase timing = [%d,%d], want certified [800,2600]", first.StartMs, first.EndMs)
	}
	if second.TemplateID != "IMPORTANT_PHRASE" || second.Text != "THIS COULD CHANGE EVERYTHING" {
		t.Fatalf("second phrase = %+v", second)
	}
	if second.StartMs != 3200 || second.EndMs != 5000 {
		t.Fatalf("second phrase timing = [%d,%d], want certified [3200,5000]", second.StartMs, second.EndMs)
	}
	// No duplicates: "A MAJOR CHANGE" appears exactly once.
	count := 0
	for _, item := range plan.Items {
		if item.Text == "A MAJOR CHANGE" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("phrase %q emitted %d times, want exactly 1", "A MAJOR CHANGE", count)
	}
}

// TestBuildPlanGolden02ImportantWords certifies the GOLDEN 02 editorial
// rules for important words on the overlay side:
//
//   - max 3 per scene: MaxKeywords=3 keeps the top-3 by score, so a valid
//     4th keyword ("ARTIFICIAL INTELLIGENCE") is dropped by the cap, never
//     by word length;
//   - no repeated words: an identical keyword text is emitted exactly once;
//   - in-scene timing only: empty text and negative timing are dropped,
//     never given guessed timing;
//   - kinetic words: every kept keyword compiles to the active_word_pop preset.
//
// Entity/type selection (PERSON/COMPANY/MONEY/DATE/NUMBER/LOCATION) and
// stop-word rejection are owned upstream of this planner — by the extraction
// layer (scripts.normalizeEntityAnnotationType + the LexiconRegistry
// stop-word SSOT). The planner consumes pre-ranked keywords.
func TestBuildPlanGolden02ImportantWords(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "golden-02", VideoID: "video-golden-02", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{
			ID: "scene-1",
			Keywords: []TimedAnnotation{
				{Text: "ELON MUSK", StartMs: 500, EndMs: 900, Score: 1.0},
				{Text: "TESLA", StartMs: 900, EndMs: 1300, Score: 0.9},
				{Text: "$10 BILLION", StartMs: 1300, EndMs: 1700, Score: 0.8},
				{Text: "ARTIFICIAL INTELLIGENCE", StartMs: 1700, EndMs: 2100, Score: 0.7},
				{Text: "OPENAI", StartMs: 2100, EndMs: 2500, Score: 0.6},       // over cap → dropped
				{Text: "TESLA", StartMs: 500, EndMs: 900, Score: 0.4},          // duplicate → dropped
				{Text: "", StartMs: 100, EndMs: 200, Score: 1.0},               // empty → dropped
				{Text: "negative timing", StartMs: -1, EndMs: 100, Score: 1.0}, // invalid → dropped
			},
		}},
	}, PlannerConfig{MaxKeywords: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 3 {
		t.Fatalf("items = %d, want exactly 3 keywords (cap)\n%+v", len(plan.Items), plan.Items)
	}
	for i, want := range []string{"ELON MUSK", "TESLA", "$10 BILLION"} {
		item := plan.Items[i]
		if item.TemplateID != "IMPORTANT_WORD" || item.Text != want {
			t.Fatalf("keyword[%d] = %+v, want IMPORTANT_WORD %q", i, item, want)
		}
	}
	// No repeated words: "TESLA" appears exactly once.
	teslaCount := 0
	for _, item := range plan.Items {
		if item.Text == "TESLA" {
			teslaCount++
		}
	}
	if teslaCount != 1 {
		t.Fatalf("keyword %q emitted %d times, want exactly 1", "TESLA", teslaCount)
	}
	// Kinetic words: every kept keyword carries a non-empty preset id, so
	// RenderingGen can lower it without inventing a default.
	for _, item := range plan.Items {
		if item.TemplateID == "IMPORTANT_WORD" && item.PresetID == "" {
			t.Fatalf("keyword item %q has an empty preset_id", item.ID)
		}
	}
}

// TestBuildPlanGolden01EmptyWhenNothingImportant certifies the empty-result
// contract: a scene with no annotations produces an empty plan (no error, no
// invented items). The planner must never fill empty air with filler text.
func TestBuildPlanGolden01EmptyWhenNothingImportant(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "golden-01-empty", VideoID: "video-golden-01-empty", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene-1"}},
	}, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 0 {
		t.Fatalf("empty annotations produced items: %+v", plan.Items)
	}
}
