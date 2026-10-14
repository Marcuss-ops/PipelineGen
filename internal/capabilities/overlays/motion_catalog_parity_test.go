package overlays

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// motion_catalog_parity_test.go pins the single source of truth for animation
// IDs: ChrononTemplate authors the motion/preset vocabulary, RenderingGen
// embeds the emitted artifact (see RenderingGen/scripts/sync_motion_catalog.sh
// --check), and PipelineGen only selects deterministically from it. The pool
// lists in preset_selection.go are handwritten copies, so without this test a
// catalog rename/removal would keep being selected here and die fail-closed in
// the worker (validateDeprecatedMotions / resolveRegisteredMotion) at render
// time instead of at build time.

type canonicalMotionUniverse struct {
	motionIDs       map[string]bool
	motionTargets   map[string][]string
	shortWordCount  map[string]int
	shortTargets    map[string][]string
	imagePresets    map[string]bool
	textPresets     map[string]bool
	presentationIDs map[string]bool
	decorIDs        map[string]bool
}

func loadCanonicalMotionUniverse(t *testing.T) canonicalMotionUniverse {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	// refactored/internal/capabilities/overlays -> repo root.
	root := filepath.Join(filepath.Dir(source), "..", "..", "..", "..")
	read := func(rel string) []byte {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Skipf("canonical catalog %s not available: %v", rel, err)
		}
		return data
	}
	universe := canonicalMotionUniverse{
		motionIDs:       map[string]bool{},
		motionTargets:   map[string][]string{},
		shortWordCount:  map[string]int{},
		shortTargets:    map[string][]string{},
		imagePresets:    map[string]bool{},
		textPresets:     map[string]bool{},
		presentationIDs: map[string]bool{},
		decorIDs:        map[string]bool{},
	}
	var chronon struct {
		Motions []struct {
			ID      string   `json:"id"`
			Targets []string `json:"targets"`
		} `json:"motions"`
		OverlayPresets struct {
			Image []string `json:"image"`
			Text  []string `json:"text"`
		} `json:"overlay_presets"`
		EntityPresentation struct {
			Families []struct {
				Presets []struct {
					ID string `json:"id"`
				} `json:"presets"`
			} `json:"families"`
		} `json:"entity_presentation"`
	}
	if err := json.Unmarshal(read("RenderingGen/renderinggen/internal/motion/catalog/chronontemplate_catalog.v1.json"), &chronon); err != nil {
		t.Fatalf("decode canonical chronontemplate catalog: %v", err)
	}
	for _, motion := range chronon.Motions {
		universe.motionIDs[motion.ID] = true
		universe.motionTargets[motion.ID] = append([]string(nil), motion.Targets...)
	}
	for _, id := range chronon.OverlayPresets.Image {
		universe.imagePresets[id] = true
	}
	for _, id := range chronon.OverlayPresets.Text {
		universe.textPresets[id] = true
	}
	for _, family := range chronon.EntityPresentation.Families {
		for _, preset := range family.Presets {
			universe.presentationIDs[preset.ID] = true
		}
	}
	var shortPhrase struct {
		Recipes []struct {
			ID        string   `json:"id"`
			WordCount int      `json:"word_count"`
			Targets   []string `json:"targets"`
		} `json:"recipes"`
		Decor []struct {
			ID string `json:"id"`
		} `json:"decor"`
	}
	if err := json.Unmarshal(read("RenderingGen/renderinggen/internal/motion/catalog/short_phrase_motion.v1.json"), &shortPhrase); err != nil {
		t.Fatalf("decode canonical short-phrase catalog: %v", err)
	}
	for _, recipe := range shortPhrase.Recipes {
		universe.motionIDs[recipe.ID] = true
		universe.shortWordCount[recipe.ID] = recipe.WordCount
		universe.shortTargets[recipe.ID] = append([]string(nil), recipe.Targets...)
	}
	for _, decor := range shortPhrase.Decor {
		universe.decorIDs[decor.ID] = true
	}
	if len(universe.motionIDs) == 0 {
		t.Fatal("canonical universe is empty")
	}
	return universe
}

func assertPoolInUniverse(t *testing.T, universe canonicalMotionUniverse, poolName string, pool []string) {
	t.Helper()
	if len(pool) == 0 {
		t.Fatalf("pool %s is empty", poolName)
	}
	seen := map[string]bool{}
	for _, id := range pool {
		if strings.TrimSpace(id) == "" {
			t.Fatalf("pool %s carries an empty id", poolName)
		}
		if seen[id] {
			t.Fatalf("pool %s repeats %q", poolName, id)
		}
		seen[id] = true
		if !universe.motionIDs[id] && !universe.presentationIDs[id] {
			t.Errorf("pool %s id %q is absent from the canonical ChrononTemplate catalogs", poolName, id)
		}
	}
}

// TestMotionPoolsAreCanonicalSubsets pins every handwritten PipelineGen motion
// pool to the canonical catalogs: any selected ID must exist upstream, or the
// worker would reject the plan fail-closed at render time.
func TestMotionPoolsAreCanonicalSubsets(t *testing.T) {
	universe := loadCanonicalMotionUniverse(t)
	assertPoolInUniverse(t, universe, "phraseMotionCandidates", phraseMotionCandidates)
	assertPoolInUniverse(t, universe, "classicAppleMotionCandidates", classicAppleMotionCandidates)
	assertPoolInUniverse(t, universe, "modernAppleMotionCandidates", modernAppleMotionCandidates)
	assertPoolInUniverse(t, universe, "brushPhraseMotionCandidates", brushPhraseMotionCandidates)
	assertPoolInUniverse(t, universe, "text3DMotionCandidates", text3DMotionCandidates)
	assertPoolInUniverse(t, universe, "phraseAppleCleanMotionCandidates", phraseAppleCleanMotionCandidates)
	assertPoolInUniverse(t, universe, "longPhraseMotionCandidates", longPhraseMotionCandidates)
	assertPoolInUniverse(t, universe, "documentaryCleanPhraseMotionCandidates", documentaryCleanPhraseMotionCandidates)
	assertPoolInUniverse(t, universe, "renderSafeImageMotions", renderSafeImageMotions)
	assertPoolInUniverse(t, universe, "automaticSingleImageMotionCandidates", automaticSingleImageMotionCandidates)
	assertPoolInUniverse(t, universe, "centeredImageMotionCandidates", centeredImageMotionCandidates)
	assertPoolInUniverse(t, universe, "generatedEntityImageMotionCandidates", generatedEntityImageMotionCandidates)
	assertPoolInUniverse(t, universe, "generatedEntityCaptionMotionCandidates", generatedEntityCaptionMotionCandidates)
	assertPoolInUniverse(t, universe, "datePresentationMotionCandidates", datePresentationMotionCandidates)
	assertPoolInUniverse(t, universe, "metricPresentationMotionCandidates", metricPresentationMotionCandidates)
	for _, id := range CertifiedAutomaticEntityCaptionMotions() {
		if !universe.motionIDs[id] && !universe.presentationIDs[id] {
			t.Errorf("automatic entity caption motion %q is absent from the canonical catalogs", id)
		}
	}
	for _, id := range imagePresetCandidates {
		if !universe.imagePresets[id] {
			t.Errorf("image preset %q is absent from the canonical overlay_presets.image list", id)
		}
	}
	for _, id := range phrasePresetCandidates {
		if !universe.textPresets[id] {
			t.Errorf("phrase preset %q is absent from the canonical overlay_presets.text list", id)
		}
	}
	for _, id := range wordPresetCandidates {
		if !universe.textPresets[id] {
			t.Errorf("word preset %q is absent from the canonical overlay_presets.text list", id)
		}
	}
}

// TestShortPhraseWordGroupsMatchCatalog ensures every routed recipe still
// exists in the canonical catalog and is only listed at its authored word_count.
// ShortPhrase overlays themselves are static in the planner; these groups are
// retained for explicit exact-cardinality template tooling.
func TestShortPhraseWordGroupsMatchCatalog(t *testing.T) {
	universe := loadCanonicalMotionUniverse(t)
	for wordCount := 1; wordCount <= 5; wordCount++ {
		pool := shortPhraseMotionCandidates[wordCount]
		for _, id := range pool {
			want, ok := universe.shortWordCount[id]
			if !ok {
				t.Errorf("short-phrase id %q (word group %d) is not a canonical short-phrase recipe", id, wordCount)
				continue
			}
			if want != wordCount {
				t.Errorf("short-phrase id %q sits in word group %d but the catalog declares word_count %d", id, wordCount, want)
			}
		}
	}
	if got := len(ShortPhraseMotionCandidates()); got != len(flattenShortPhraseMotionCandidates()) {
		t.Fatalf("ShortPhraseMotionCandidates() = %d ids, want the flattened word groups", got)
	}
}

// TestAnimationLaneAliasesResolveToOneLimit pins the single alias table: every
// alias of a lane resolves to the same limit, and the first positive alias
// wins regardless of which planner branch reads the lane.
func TestAnimationLaneAliasesResolveToOneLimit(t *testing.T) {
	for lane, keys := range animationLaneKeys {
		if len(keys) == 0 {
			t.Fatalf("lane %s has no keys", lane)
		}
		seen := map[string]bool{}
		for _, key := range keys {
			if seen[key] {
				t.Fatalf("lane %s repeats alias %q", lane, key)
			}
			seen[key] = true
		}
		// Each alias alone must produce the same limit.
		for _, key := range keys {
			if got := laneAnimationCount(map[string]int{key: 3}, lane); got != 3 {
				t.Errorf("lane %s alias %q alone = %d, want 3", lane, key, got)
			}
		}
		// First positive alias wins, in table order.
		counts := map[string]int{keys[len(keys)-1]: 7, keys[0]: 2}
		if got := laneAnimationCount(counts, lane); got != 2 {
			t.Errorf("lane %s precedence = %d, want 2 (first positive alias wins)", lane, got)
		}
		if got := laneAnimationCount(nil, lane); got != 0 {
			t.Errorf("lane %s empty counts = %d, want 0 (full catalog)", lane, got)
		}
	}
	// The unified images-with-text lane must honor every historic alias,
	// including entity_text_images on every planner branch.
	for _, alias := range []string{"images_with_text", "single_image_with_text", "entity_text_images", "image_four_with_text"} {
		if got := laneAnimationCount(map[string]int{alias: 4}, laneImagesWithText); got != 4 {
			t.Errorf("images-with-text alias %q = %d, want 4", alias, got)
		}
	}
	if got := laneAnimationCount(map[string]int{"no_such_lane": 1}, "no_such_lane"); got != 0 {
		t.Errorf("unknown lane = %d, want 0", got)
	}
}
