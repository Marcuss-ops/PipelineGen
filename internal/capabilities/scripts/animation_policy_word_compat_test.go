package scriptgeneration

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// shortRecipeForWords returns a motion from the word-count group. Groups may
// share motions by design, so the id is not exclusive to one count.
func shortRecipeForWords(t *testing.T, words int) string {
	t.Helper()
	pool := capabilityoverlay.ShortPhraseMotionsForWordCount(words)
	if len(pool) == 0 {
		t.Fatalf("word group %d has no short-phrase motions", words)
	}
	return pool[0]
}

func TestAnimationPolicyFailsClosedOnWordMismatchedPhraseMotion(t *testing.T) {
	twoWordRecipe := shortRecipeForWords(t, 2)
	// A long-phrase motion is outside every short-phrase group.
	longPhraseMotion := "precision_tracking_lock"

	shortItem := []capabilityoverlay.OverlayItem{
		{ID: "s", SceneID: "scene", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Crollo margini"},
	}
	policy := []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "scene", MotionID: longPhraseMotion}}
	if err := applyAnimationPolicies(shortItem, policy, "plan", "video", nil); err == nil {
		t.Fatalf("long-phrase motion %q on a 2-word phrase must fail, applied %q", longPhraseMotion, shortItem[0].MotionID)
	}

	longText := "Il governo annuncia una nuova manovra economica oggi"
	longItem := []capabilityoverlay.OverlayItem{
		{ID: "l", SceneID: "scene", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: longText},
	}
	// Exact-cardinality recipes must never reach non-short copy.
	exactRecipe := "short_phrase_scale_settle_word"
	longPolicy := []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "scene", MotionID: exactRecipe}}
	if err := applyAnimationPolicies(longItem, longPolicy, "plan", "video", nil); err == nil {
		t.Fatalf("exact recipe %q on 8-word copy must fail, applied %q", exactRecipe, longItem[0].MotionID)
	}

	// Matching word counts apply; the short recipe admission that used to
	// reject every short id as uncertified now lets them through.
	okItem := []capabilityoverlay.OverlayItem{
		{ID: "s", SceneID: "scene", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Crollo margini"},
	}
	okPolicy := []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "scene", MotionID: twoWordRecipe}}
	if err := applyAnimationPolicies(okItem, okPolicy, "plan", "video", nil); err != nil {
		t.Fatalf("matching 2-word recipe must apply: %v", err)
	}
	if okItem[0].MotionID != twoWordRecipe {
		t.Fatalf("matching recipe not applied, got %q", okItem[0].MotionID)
	}

	// Long copy keeps the general catalog.
	generalItem := []capabilityoverlay.OverlayItem{
		{ID: "l", SceneID: "scene", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: longText},
	}
	general := capabilityoverlay.CertifiedPhraseMotions()[0]
	if err := applyAnimationPolicies(generalItem,
		[]scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "scene", MotionID: general}},
		"plan", "video", nil); err != nil {
		t.Fatalf("general catalog motion on long copy must apply: %v", err)
	}
}

func TestAnimationPolicySeedsShortGroupsPerWordCount(t *testing.T) {
	policies := []scriptpkg.AnimationPolicy{{Target: "important_phrase", GroupID: "mixed"}}
	makeItems := func() []capabilityoverlay.OverlayItem {
		return []capabilityoverlay.OverlayItem{
			{ID: "two", SceneID: "scene", GroupID: "mixed", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Crollo margini"},
			{ID: "four", SceneID: "scene", GroupID: "mixed", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "La casa nel bosco"},
		}
	}
	first, second := makeItems(), makeItems()
	if err := applyAnimationPolicies(first, policies, "stable-plan", "video", nil); err != nil {
		t.Fatalf("seeded mixed-word group must not fail: %v", err)
	}
	if err := applyAnimationPolicies(second, policies, "stable-plan", "video", nil); err != nil {
		t.Fatalf("seeded retry must not fail: %v", err)
	}
	for i, words := range []int{2, 4} {
		inGroup := false
		for _, id := range capabilityoverlay.ShortPhraseMotionsForWordCount(words) {
			if id == first[i].MotionID {
				inGroup = true
			}
		}
		if !inGroup {
			t.Fatalf("item %d (%d words) drew %q outside its word-count group", i, words, first[i].MotionID)
		}
		if first[i].MotionID != second[i].MotionID {
			t.Fatalf("seeded draw must be retry-stable, got %q then %q", first[i].MotionID, second[i].MotionID)
		}
	}
}

func TestAssignEntityImageMotionsHonoursRequestedCaptionPool(t *testing.T) {
	automatic := capabilityoverlay.CertifiedAutomaticEntityCaptionMotions()
	if len(automatic) < 2 {
		t.Fatalf("need at least two automatic caption motions, got %d", len(automatic))
	}
	requested := append([]string(nil), automatic[0], automatic[1])
	makeItems := func() []capabilityoverlay.OverlayItem {
		return []capabilityoverlay.OverlayItem{
			{ID: "e1", Kind: string(capabilityoverlay.KindEntityImage), EntityCaption: "Ada"},
			{ID: "e2", Kind: string(capabilityoverlay.KindEntityImage), EntityCaption: "Grace"},
			{ID: "e3", Kind: string(capabilityoverlay.KindEntityImage), EntityCaption: "Katherine"},
		}
	}
	items := makeItems()
	if err := assignEntityImageMotions(items, 0, 1920, 1080, "", nil, requested); err != nil {
		t.Fatalf("requested caption pool: %v", err)
	}
	want := []string{requested[0], requested[1], requested[0]}
	for i := range items {
		if items[i].CaptionMotionID != want[i] {
			t.Fatalf("item %d caption = %q, want rotation %q", i, items[i].CaptionMotionID, want[i])
		}
	}
	// The rotation is offset-aware: a fresh plan offset shifts the pool.
	shifted := makeItems()
	if err := assignEntityImageMotions(shifted, 1, 1920, 1080, "", nil, requested); err != nil {
		t.Fatalf("offset caption pool: %v", err)
	}
	if shifted[0].CaptionMotionID != requested[1] || shifted[1].CaptionMotionID != requested[0] {
		t.Fatalf("offset 1 must shift the pool, got %q %q", shifted[0].CaptionMotionID, shifted[1].CaptionMotionID)
	}
	// Fail closed on uncertified, repeated, and camera-unsafe ids.
	for _, pool := range [][]string{
		{"not_a_motion"},
		{requested[0], requested[0]},
		{"entity_depth_caption"},
	} {
		if err := assignEntityImageMotions(makeItems(), 0, 1920, 1080, "", nil, pool); err == nil {
			t.Fatalf("caption pool %q must fail closed", pool)
		}
	}
}

func TestAnimationPolicySeededCaptionUsesRequestedPool(t *testing.T) {
	automatic := capabilityoverlay.CertifiedAutomaticEntityCaptionMotions()
	if len(automatic) < 2 {
		t.Fatalf("need at least two automatic caption motions, got %d", len(automatic))
	}
	requested := append([]string(nil), automatic[0], automatic[1])
	policies := []scriptpkg.AnimationPolicy{{Target: "caption", GroupID: "scene"}}
	makeItems := func() []capabilityoverlay.OverlayItem {
		return []capabilityoverlay.OverlayItem{
			{ID: "c1", SceneID: "scene", Kind: "image", EntityCaption: "Ada"},
		}
	}
	first, second := makeItems(), makeItems()
	if err := applyAnimationPolicies(first, policies, "plan", "video", nil, requested); err != nil {
		t.Fatalf("seeded caption with requested pool: %v", err)
	}
	if err := applyAnimationPolicies(second, policies, "plan", "video", nil, requested); err != nil {
		t.Fatalf("seeded caption retry: %v", err)
	}
	if first[0].CaptionMotionID != requested[0] {
		t.Fatalf("seeded caption must draw from the requested pool, got %q", first[0].CaptionMotionID)
	}
	if first[0].CaptionMotionID != second[0].CaptionMotionID {
		t.Fatalf("seeded caption must be retry-stable, got %q then %q", first[0].CaptionMotionID, second[0].CaptionMotionID)
	}
	// Without a requested pool the certified automatic pool applies.
	plain := makeItems()
	if err := applyAnimationPolicies(plain, policies, "plan", "video", nil); err != nil {
		t.Fatalf("seeded caption without pool: %v", err)
	}
	allowed := make(map[string]bool, len(automatic))
	for _, id := range automatic {
		allowed[id] = true
	}
	if !allowed[plain[0].CaptionMotionID] {
		t.Fatalf("default seeded caption %q is outside the automatic pool", plain[0].CaptionMotionID)
	}
}
