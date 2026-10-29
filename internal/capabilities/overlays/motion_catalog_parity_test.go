package overlays

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
	// renderGoldenIDs / renderGoldenBasemapStyles are derived from the map
	// geo-label golden assets: every file <style>__<id>.png certifies that
	// ChrononTemplate's map renderer implements that id for that basemap
	// style. This is the verified backing for the map-animation and map-
	// basemap registry entries that live outside the emitted C++ catalogs.
	renderGoldenIDs           map[string]bool
	renderGoldenBasemapStyles map[string]bool
	// cameraRecipeIDs comes from ChrononTemplate/catalog/
	// entity_visual_sequence.v1.json camera_recipes: the documented entity
	// camera-motion vocabulary.
	cameraRecipeIDs map[string]bool
	// basemapStyleIDs is parsed from the cartography module's
	// dynamic_tile_pyramid.py BASEMAP_STYLES (now owned by
	// ChrononPlugin/plugins/maps/tools/cartography; the legacy
	// Chronon3d/tools/cartography copy is an accepted fallback): the
	// production palette the dynamic-map renderer validates against
	// (is_basemap_style).
	basemapStyleIDs map[string]bool
	// mapMotionIDs is parsed from the map renderer vocabulary tables
	// (MAP_LABEL_ANIMATIONS, MAP_CAMERA_ANIMATIONS and siblings): these are
	// the ids the geo camera accepts (the dynamic lane is the native renderer
	// of ChrononPlugin since the Python renderer was retired).
	mapMotionIDs map[string]bool
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
		motionIDs:                 map[string]bool{},
		motionTargets:             map[string][]string{},
		shortWordCount:            map[string]int{},
		shortTargets:              map[string][]string{},
		imagePresets:              map[string]bool{},
		textPresets:               map[string]bool{},
		presentationIDs:           map[string]bool{},
		decorIDs:                  map[string]bool{},
		renderGoldenIDs:           map[string]bool{},
		renderGoldenBasemapStyles: map[string]bool{},
		cameraRecipeIDs:           map[string]bool{},
		basemapStyleIDs:           map[string]bool{},
		mapMotionIDs:              map[string]bool{},
	}
	// Machine-readable backing source 1: the geo-label golden assets. Every
	// <style>__<id>.png is a rendered golden proving the renderer draws <id>
	// on basemap <style>.
	goldenDir := filepath.Join(root, "ChrononTemplate", "tools", "map", "assets", "geo_label_basemap_goldens")
	goldenEntries, err := os.ReadDir(goldenDir)
	if err == nil {
		for _, golden := range goldenEntries {
			name := golden.Name()
			if golden.IsDir() || filepath.Ext(name) != ".png" {
				continue
			}
			sep := strings.Index(name, "__")
			if sep <= 0 {
				continue
			}
			style, id := name[:sep], strings.TrimSuffix(name[sep+2:], ".png")
			if id != "" {
				universe.renderGoldenIDs[id] = true
			}
			if style != "" {
				universe.renderGoldenBasemapStyles[style] = true
			}
		}
	}
	// Machine-readable backing source 2: the entity camera-motion vocabulary.
	var cameraSeq struct {
		CameraRecipes []struct {
			ID string `json:"id"`
		} `json:"camera_recipes"`
	}
	if err := json.Unmarshal(read("ChrononTemplate/catalog/entity_visual_sequence.v1.json"), &cameraSeq); err == nil {
		for _, recipe := range cameraSeq.CameraRecipes {
			if recipe.ID != "" {
				universe.cameraRecipeIDs[recipe.ID] = true
			}
		}
	}
	// Machine-readable backing source 3+4: the python map renderer
	// vocabulary. BASEMAP_STYLES is the palette is_basemap_style validates
	// against; the executor modules are the dispatchers the runtime scripts
	// actually call. We collect every id-shaped string literal from the
	// executor chain so a registry id is backed whenever the python renderer
	// knows it (tables like MAP_LABEL_SPECS or MAP_EDITORIAL_SCENES).
	parsePyStringTuple := func(rel, varName string) []string {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil
		}
		src := string(data)
		idx := strings.Index(src, varName+" = (")
		if idx < 0 {
			return nil
		}
		tail := src[idx+len(varName)+4:]
		end := strings.Index(tail, ")")
		if end < 0 {
			return nil
		}
		var out []string
		for _, m := range regexp.MustCompile(`"([a-z0-9_]+)"`).FindAllStringSubmatch(tail[:end], -1) {
			out = append(out, m[1])
		}
		return out
	}
	basemapStyles := parsePyStringTuple("ChrononPlugin/plugins/maps/tools/cartography/dynamic_tile_pyramid.py", "BASEMAP_STYLES")
	if len(basemapStyles) == 0 {
		basemapStyles = parsePyStringTuple("Chronon3d/tools/cartography/dynamic_tile_pyramid.py", "BASEMAP_STYLES")
	}
	for _, style := range basemapStyles {
		universe.basemapStyleIDs[style] = true
	}
	scanExecutorVocabulary := func(dir string) {
		wordRe := regexp.MustCompile(`"([a-z][a-z0-9_]+)"`)
		for _, pattern := range []string{"*.py", "*.cpp", "*.hpp"} {
			files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir), pattern))
			if err != nil {
				continue
			}
			for _, file := range files {
				data, err := os.ReadFile(file)
				if err != nil {
					continue
				}
				for _, m := range wordRe.FindAllStringSubmatch(string(data), -1) {
					universe.mapMotionIDs[m[1]] = true
				}
			}
		}
	}
	scanExecutorVocabulary("ChrononTemplate/tools/map")
	scanExecutorVocabulary("ChrononPlugin/plugins/maps/tools/cartography")
	// The dynamic map lane is the native renderer of ChrononPlugin: its
	// sources are the verified executor vocabulary since the Python renderer
	// was retired (2026-10-26).
	scanExecutorVocabulary("ChrononPlugin/plugins/maps/src")
	scanExecutorVocabulary("ChrononPlugin/plugins/maps/include/chrononplugin/maps")
	scanExecutorVocabulary("ChrononPlugin/plugins/maps/tools/map_native")
	scanExecutorVocabulary("Chronon3d/tools/cartography")
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
	assertPoolInUniverse(t, universe, "text3DMotionCandidates", text3DMotionCandidates)
	assertPoolInUniverse(t, universe, "phraseAppleCleanMotionCandidates", phraseAppleCleanMotionCandidates)
	assertPoolInUniverse(t, universe, "longPhraseMotionCandidates", longPhraseMotionCandidates)
	// documentaryCleanPhraseMotionCandidates was retired 2026-10-15 with its
	// family key; its four motions stay canonical through modern_apple.
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

func TestAnimationRegistryActiveEntriesAreCanonicalAndRetiredEntriesAreNotSelectable(t *testing.T) {
	universe := loadCanonicalMotionUniverse(t)
	groups := map[string]bool{}
	for _, entry := range canonicalAnimationRegistry.Entries() {
		for _, group := range entry.Families {
			groups[group] = true
		}
		if entry.Status == "active" && entry.RendererSupported && (containsMotion(entry.Families, "phrase.all") || containsMotion(entry.Families, "image.all") || containsMotion(entry.Families, "entity.caption") || containsMotion(entry.Families, "entity.date") || containsMotion(entry.Families, "entity.metric")) && !universe.motionIDs[entry.ID] && !universe.presentationIDs[entry.ID] {
			t.Errorf("active registry motion %q is absent from accepted Chronon catalogs", entry.ID)
		}
	}
	for group := range groups {
		for _, id := range canonicalAnimationRegistry.Candidates(group) {
			if canonicalAnimationRegistry.IsRetired(id) || !canonicalAnimationRegistry.RendererSupported(id) {
				t.Errorf("group %q exposes retired or unsupported motion %q", group, id)
			}
		}
		for _, id := range canonicalAnimationRegistry.RetiredCandidates(group) {
			if canonicalAnimationRegistry.Selectable(id, group) {
				t.Errorf("retired motion %q remains selectable in group %q", id, group)
			}
		}
	}
	defaultBasemap := canonicalAnimationRegistry.MapTypeDefault()
	if !canonicalAnimationRegistry.Selectable(defaultBasemap, "map.basemap") {
		t.Fatalf("default map type %q does not resolve to an active basemap", canonicalAnimationRegistry.MapTypeDefault())
	}
}

// TestMapCompositionsAreTheChrononPluginCatalog pins the native renderer's
// servable subset to its published catalog
// (ChrononPlugin/plugins/maps/maps-renderer-catalog.v1.json). The animation
// registry may retain gallery concepts that are not yet exposed by the native
// renderer; every renderer entry must still be registered and servable.
func TestMapCompositionsAreTheChrononPluginCatalog(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..", "..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "ChrononPlugin", "plugins", "maps", "maps-renderer-catalog.v1.json"))
	if err != nil {
		t.Skipf("ChrononPlugin map catalog not available: %v", err)
	}
	var catalog struct {
		Source        string   `json:"source"`
		MapAnimations []string `json:"map_animations"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("decode ChrononPlugin map catalog: %v", err)
	}
	if catalog.Source != "ChrononPlugin" || len(catalog.MapAnimations) == 0 {
		t.Fatalf("ChrononPlugin map catalog is not the expected artifact: %+v", catalog)
	}
	cataloged := map[string]bool{}
	for _, id := range catalog.MapAnimations {
		cataloged[id] = true
	}
	registered := map[string]bool{}
	for _, id := range canonicalAnimationRegistry.Candidates("map.composition") {
		registered[id] = true
	}
	for id := range cataloged {
		if families, known := canonicalAnimationRegistry.FamiliesOf(id); !known || !containsMotion(families, "map.composition") {
			t.Errorf("ChrononPlugin map composition %q is not registered in map.composition", id)
		}
	}
	ported := map[string]bool{}
	for _, id := range PortedMapCompositions() {
		if !cataloged[id] || !registered[id] {
			t.Errorf("servable composition %q must be both cataloged and registered", id)
		}
		ported[id] = true
	}
	if len(ported) != len(cataloged) {
		t.Errorf("servable composition count = %d, native catalog count = %d", len(ported), len(cataloged))
	}
	for id := range cataloged {
		if !ported[id] {
			t.Errorf("native catalog composition %q is missing from the servable subset", id)
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
