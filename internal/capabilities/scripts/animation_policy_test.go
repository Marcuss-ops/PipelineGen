package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestAnimationPoliciesSelectMotionPerGroupAndPreserveAutomaticDefaults(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "phrase-1", SceneID: "scene-a", Kind: "text_phrase", MotionID: "auto-phrase"},
		{ID: "phrase-2", SceneID: "scene-a", Kind: "text_phrase", MotionID: "another-auto-phrase"},
		{ID: "phrase-3", SceneID: "scene-b", Kind: "text_phrase", MotionID: "scene-b-auto"},
	}
	if err := applyAnimationPolicies(items, nil, "plan", "video", nil); err != nil {
		t.Fatalf("empty policy application: %v", err)
	}
	if items[0].MotionID != "auto-phrase" || items[1].MotionID != "another-auto-phrase" {
		t.Fatalf("without payload policies, preserve planner selection: %q %q", items[0].MotionID, items[1].MotionID)
	}
	if err := applyAnimationPolicies(items, []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "scene-a", MotionID: "not_certified"}}, "plan", "video", nil); err == nil {
		t.Fatal("uncertified group policy should fail")
	}

	fallback := []capabilityoverlay.OverlayItem{
		{ID: "scene-phrase-1", SceneID: "scene-random", Kind: "text_phrase"},
		{ID: "scene-phrase-2", SceneID: "scene-random", Kind: "text_phrase"},
	}
	if err := applyAnimationPolicies(fallback, []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "scene-random", MotionID: capabilityoverlay.CertifiedPhraseMotions()[2]}}, "plan", "video", nil); err != nil {
		t.Fatalf("scene group fallback: %v", err)
	}
	if fallback[0].MotionID != capabilityoverlay.CertifiedPhraseMotions()[2] || fallback[1].MotionID != capabilityoverlay.CertifiedPhraseMotions()[2] {
		t.Fatalf("scene group should share its chosen motion, got %q and %q", fallback[0].MotionID, fallback[1].MotionID)
	}

	policies := []scriptpkg.AnimationPolicy{
		{Target: "important_phrase", GroupID: "scene-a", MotionID: capabilityoverlay.CertifiedPhraseMotions()[0], MotionParams: map[string]any{"enter_frames": 8}},
	}
	if err := applyAnimationPolicies(items, policies, "plan", "video", nil); err != nil {
		t.Fatalf("explicit group policy: %v", err)
	}
	if items[0].MotionID != policies[0].MotionID || items[1].MotionID != policies[0].MotionID {
		t.Fatalf("scene-a should use the requested motion for every phrase, got %q and %q", items[0].MotionID, items[1].MotionID)
	}
	if items[2].MotionID == items[0].MotionID {
		t.Fatalf("different scene group reused scene-a motion: %q", items[2].MotionID)
	}
	if items[0].MotionParams["enter_frames"] != float64(8) && items[0].MotionParams["enter_frames"] != 8 {
		t.Fatalf("motion params were not carried through: %+v", items[0].MotionParams)
	}
}

func TestAnimationPoliciesOmitUnsetMotionFromWorkerPayload(t *testing.T) {
	payload, err := json.Marshal(toOverlayAnimationPolicies([]scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "scene-a"}}))
	if err != nil {
		t.Fatalf("marshal worker animation policy: %v", err)
	}
	if strings.Contains(string(payload), `"motion_id"`) {
		t.Fatalf("unset motion_id must be omitted so RenderingGen selects random: %s", payload)
	}
}

func TestAnimationPoliciesChooseStableRandomMotionWhenGroupSelectionIsOmitted(t *testing.T) {
	policies := []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "scene-random"}}
	makeItems := func() []capabilityoverlay.OverlayItem {
		return []capabilityoverlay.OverlayItem{
			{ID: "a", SceneID: "scene-random", Kind: "text_phrase"},
			{ID: "b", SceneID: "scene-random", Kind: "text_phrase"},
			{ID: "other", SceneID: "untouched", Kind: "text_phrase", MotionID: "planner-choice"},
		}
	}
	first, second := makeItems(), makeItems()
	if err := applyAnimationPolicies(first, policies, "stable-plan", "video", nil); err != nil {
		t.Fatalf("select random motion for group: %v", err)
	}
	if err := applyAnimationPolicies(second, policies, "stable-plan", "video", nil); err != nil {
		t.Fatalf("repeat random motion selection: %v", err)
	}
	if first[0].MotionID == "" || first[0].MotionID != first[1].MotionID || first[0].MotionID != second[0].MotionID {
		t.Fatalf("group random selection must be shared and retry-stable: first=%q/%q second=%q", first[0].MotionID, first[1].MotionID, second[0].MotionID)
	}
	if first[2].MotionID != "planner-choice" {
		t.Fatalf("undeclared groups must preserve the automatic planner choice, got %q", first[2].MotionID)
	}

	subgroupOnly := []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "scene-random", SubgroupID: "one"}}
	untargeted := []capabilityoverlay.OverlayItem{{ID: "two", GroupID: "scene-random", SubgroupID: "two", Kind: "text_phrase", MotionID: "planner-choice"}}
	if err := applyAnimationPolicies(untargeted, subgroupOnly, "stable-plan", "video", nil); err != nil {
		t.Fatalf("subgroup-only policy should be valid: %v", err)
	}
	if untargeted[0].MotionID != "planner-choice" {
		t.Fatalf("a sibling outside a declared subgroup must retain its planner motion, got %q", untargeted[0].MotionID)
	}
}

func TestAnimationPoliciesApplySubgroupOverridesAndFallbackByGroup(t *testing.T) {
	phrasePool := capabilityoverlay.CertifiedPhraseMotions()
	items := []capabilityoverlay.OverlayItem{
		{ID: "headline", SceneID: "scene-x", GroupID: "editorial", Kind: "text_phrase", SubgroupID: "headline", MotionID: "old-a"},
		{ID: "body", SceneID: "scene-x", GroupID: "editorial", Kind: "text_phrase", SubgroupID: "body", MotionID: "old-b"},
	}
	policies := []scriptpkg.AnimationPolicy{
		{Target: "important_phrase", GroupID: "editorial", MotionID: phrasePool[0]},
		{Target: "important_phrase", GroupID: "editorial", SubgroupID: "headline", MotionID: phrasePool[1]},
	}
	if err := applyAnimationPolicies(items, policies, "plan", "video", nil); err != nil {
		t.Fatalf("group/subgroup policies: %v", err)
	}
	if items[0].MotionID != phrasePool[1] || items[1].MotionID != phrasePool[0] {
		t.Fatalf("subgroup policy did not override group default: got %q and %q", items[0].MotionID, items[1].MotionID)
	}

	fallbackItems := []capabilityoverlay.OverlayItem{
		{ID: "a", SceneID: "same-scene", Kind: "text_phrase"},
		{ID: "b", SceneID: "same-scene", Kind: "text_phrase"},
	}
	if err := applyAnimationPolicies(fallbackItems, []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "same-scene", MotionID: phrasePool[2]}}, "stable-plan", "video", nil); err != nil {
		t.Fatal(err)
	}
	if fallbackItems[0].MotionID != phrasePool[2] || fallbackItems[1].MotionID != phrasePool[2] {
		t.Fatal("scene_id fallback must apply the selected motion to the whole scene group")
	}

	randomPolicies := []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "editorial", SubgroupID: "body"}}
	randomFirst := []capabilityoverlay.OverlayItem{
		{ID: "body-1", GroupID: "editorial", SubgroupID: "body", Kind: "text_phrase"},
		{ID: "body-2", GroupID: "editorial", SubgroupID: "body", Kind: "text_phrase"},
	}
	randomSecond := append([]capabilityoverlay.OverlayItem(nil), randomFirst...)
	if err := applyAnimationPolicies(randomFirst, randomPolicies, "stable-plan", "video", nil); err != nil {
		t.Fatal(err)
	}
	if err := applyAnimationPolicies(randomSecond, randomPolicies, "stable-plan", "video", nil); err != nil {
		t.Fatal(err)
	}
	if randomFirst[0].MotionID == "" || randomFirst[0].MotionID != randomFirst[1].MotionID || randomFirst[0].MotionID != randomSecond[0].MotionID {
		t.Fatalf("omitted subgroup motion must be shared and retry-stable: %q %q %q", randomFirst[0].MotionID, randomFirst[1].MotionID, randomSecond[0].MotionID)
	}
}

func TestAnimationPoliciesTargetDatesAndMetricsAndSurviveWireProjection(t *testing.T) {
	dateMotion := capabilityoverlay.DatePresentationMotionCandidates()[0]
	metricMotion := capabilityoverlay.MetricPresentationMotionCandidates()[0]
	items := []capabilityoverlay.OverlayItem{
		{ID: "date-item", SceneID: "scene", GroupID: "values", SubgroupID: "date", Kind: "number", TemplateID: "TIMELINE_DATE_CARD", MotionID: "automatic-date"},
		{ID: "metric-item", SceneID: "scene", GroupID: "values", SubgroupID: "metric", Kind: "number", TemplateID: "METRIC_STAT_CARD", MotionID: "automatic-metric"},
	}
	policies := []scriptpkg.AnimationPolicy{
		{Target: "date", GroupID: "values", MotionID: dateMotion},
		{Target: "metric", GroupID: "values", MotionID: metricMotion},
	}
	if err := applyAnimationPolicies(items, policies, "plan", "video", nil); err != nil {
		t.Fatalf("apply date/metric policies: %v", err)
	}
	if items[0].MotionID != dateMotion || items[1].MotionID != metricMotion {
		t.Fatalf("date/metric policy motions = %q/%q, want %q/%q", items[0].MotionID, items[1].MotionID, dateMotion, metricMotion)
	}
	plan := capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan", VideoID: "video",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationMS: 1000,
		AnimationPolicies: toOverlayAnimationPolicies(policies), Items: items,
	}
	raw, err := marshalRenderingGenOverlayPlan(plan)
	if err != nil {
		t.Fatalf("marshal plan with presentation policies: %v", err)
	}
	var wire struct {
		AnimationPolicies []scriptpkg.AnimationPolicy `json:"animation_policies"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode worker payload: %v", err)
	}
	if len(wire.AnimationPolicies) != 2 || wire.AnimationPolicies[0].Target != "date" || wire.AnimationPolicies[1].Target != "metric" {
		t.Fatalf("worker animation policies = %+v, want date and metric selectors", wire.AnimationPolicies)
	}
}

func TestAnimationPolicyAcceptsCertifiedLightLeakForImage(t *testing.T) {
	motions := capabilityoverlay.CertifiedLightLeakMotions()
	if len(motions) == 0 {
		t.Fatal("light-leak catalog must be populated")
	}
	items := []capabilityoverlay.OverlayItem{{
		ID: "image-1", SceneID: "scene-1", GroupID: "visuals", SubgroupID: "image-1",
		Kind: "image", TemplateID: "IMAGE_OVERLAY",
		AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "image-asset"}},
	}}
	policy := []scriptpkg.AnimationPolicy{{Target: "image", GroupID: "visuals", MotionID: motions[0]}}
	if err := applyAnimationPolicies(items, policy, "plan-lightleak", "video", nil); err != nil {
		t.Fatalf("certified light leak must be accepted for image target: %v", err)
	}
	if items[0].MotionID != motions[0] {
		t.Fatalf("light-leak choice = %q, want payload selection %q", items[0].MotionID, motions[0])
	}

	// The generated-image path normally assigns a restricted entity image
	// pool before policies are applied. That path must still admit certified
	// light leaks rather than making them standalone-only by accident.
	entityItems := []capabilityoverlay.OverlayItem{{
		ID: "entity-image", SceneID: "scene-1", GroupID: "visuals", SubgroupID: "entity-image",
		Kind: string(capabilityoverlay.KindEntityImage), TemplateID: "IMAGE_OVERLAY",
		AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "portrait-asset"}},
	}}
	if err := applyAnimationPolicies(entityItems, policy, "plan-lightleak", "video", capabilityoverlay.CertifiedEntityImageMotions()); err != nil {
		t.Fatalf("entity image policy must accept certified light leak with its automatic pool present: %v", err)
	}
	if entityItems[0].MotionID != motions[0] {
		t.Fatalf("entity image light leak = %q, want %q", entityItems[0].MotionID, motions[0])
	}
}

// TestRetiredRecipeCannotBePinnedByAnAnimationPolicy pins the production
// guarantee behind the 2026-10-13 removals: a payload that names a retired
// recipe must fail closed instead of rendering it.
func TestRetiredRecipeCannotBePinnedByAnAnimationPolicy(t *testing.T) {
	const retired = "short_phrase_product_warp_text"
	if !capabilityoverlay.IsRemovedGeneratedPhraseMotion(retired) {
		t.Fatalf("%s is not retired any more; pick a retired id for this test", retired)
	}
	items := []capabilityoverlay.OverlayItem{{ID: "phrase", SceneID: "scene", Kind: "text_phrase"}}
	err := applyAnimationPolicies(items, []scriptpkg.AnimationPolicy{{
		Target: "important_phrase", GroupID: "scene", MotionID: retired,
	}}, "plan", "video", nil)
	if err == nil {
		t.Fatalf("a retired recipe was accepted as an explicit animation policy: %q", items[0].MotionID)
	}
	if !strings.Contains(err.Error(), "not certified") {
		t.Fatalf("error = %v, want the not-certified refusal", err)
	}
}

func TestAnimationPoliciesRejectUncertifiedAndDuplicateSelectors(t *testing.T) {
	cases := []struct {
		name     string
		policies []scriptpkg.AnimationPolicy
		want     string
	}{
		{"unknown motion", []scriptpkg.AnimationPolicy{{Target: "image", GroupID: "scene", MotionID: "missing"}}, "not certified"},
		{"params without motion", []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "scene", MotionParams: map[string]any{"enter_frames": 2}}}, "motion_params requires motion_id"},
		{"duplicate selector", []scriptpkg.AnimationPolicy{
			{Target: "important_phrase", GroupID: "scene", MotionID: capabilityoverlay.CertifiedPhraseMotions()[0]},
			{Target: "important_phrase", GroupID: "scene", MotionID: capabilityoverlay.CertifiedPhraseMotions()[1]},
		}, "duplicate animation policy"},
		{"bad timing", []scriptpkg.AnimationPolicy{{Target: "caption", GroupID: "scene", MotionID: capabilityoverlay.CertifiedEntityCaptionMotions()[0], MotionParams: map[string]any{"enter_frames": 241}}}, "enter_frames"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := []capabilityoverlay.OverlayItem{{ID: "phrase", SceneID: "scene", Kind: "text_phrase"}}
			err := applyAnimationPolicies(items, tc.policies, "plan", "video", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
