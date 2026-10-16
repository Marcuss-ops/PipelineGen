package overlays

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// entity_style_parity_test.go pins the PipelineGen side of the entity-style
// contract (overlay-plan.v1 `entity_style_id`) to the RenderingGen registry
// that actually renders it.
//
// CertifiedAppleSpatialEntityStyles and CertifiedEntityStyleSelectors are
// handwritten copies of RenderingGen's composition catalog, so without this
// test a RenderingGen rename or removal would keep being certified here and
// die fail-closed at render time instead of at build time — the same rationale
// as motion_catalog_parity_test.go.
//
// RenderingGen/renderinggen/internal/overlay/entity_style.go is the authority:
// premiumEntityStyles is the LIVE catalog (former group 3, styles 11–15, and
// style 10 were dropped as duplicates), entityStyleAliases keeps those retired
// IDs resolvable by stable id, and entityTagSelectors is the accepted selector
// vocabulary. The certified list is therefore deliberately NOT equal to the
// live list — the retired IDs stay certified because a producer must still be
// able to transport them. What is asserted here is resolution, coverage and
// uniqueness, never equality.
var (
	premiumEntityStylesBlock = regexp.MustCompile(`(?s)premiumEntityStyles\s*=\s*\[\.\.\.\]entityStyleVariant\{(.*?)\n\}`)
	entityStyleAliasesBlock  = regexp.MustCompile(`(?s)entityStyleAliases\s*=\s*map\[string\]string\{(.*?)\n\}`)
	entityTagSelectorsBlock  = regexp.MustCompile(`(?s)entityTagSelectors\s*=\s*map\[string\]entityStyleQuery\{(.*?)\n\}`)

	variantIDLine   = regexp.MustCompile(`(?m)^\s*ID:\s+"([^"]+)"`)
	stringMapEntry  = regexp.MustCompile(`"([^"]*)"\s*:\s*"([^"]*)"`)
	selectorKeyLine = regexp.MustCompile(`(?m)^\s*"([^"]*)"\s*:`)
)

// entityStyleRegistrySource is the parsed shape of RenderingGen's registry:
// the live variant ids, the backward-compatibility aliases (key → live id) and
// the selector words the renderer accepts.
type entityStyleRegistrySource struct {
	liveIDs      map[string]bool
	aliasTargets map[string]string
	selectorKeys map[string]bool
}

func loadEntityStyleRegistrySource(t *testing.T) entityStyleRegistrySource {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	// refactored/internal/capabilities/overlays -> repo root.
	root := filepath.Join(filepath.Dir(source), "..", "..", "..", "..")
	const rel = "RenderingGen/renderinggen/internal/overlay/entity_style.go"
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Skipf("RenderingGen entity-style registry %s not available: %v", rel, err)
	}
	text := string(raw)

	block := func(re *regexp.Regexp, what string) string {
		match := re.FindStringSubmatch(text)
		if match == nil {
			t.Fatalf("cannot parse %s out of %s; the registry shape changed", what, rel)
		}
		return match[1]
	}

	registry := entityStyleRegistrySource{
		liveIDs:      map[string]bool{},
		aliasTargets: map[string]string{},
		selectorKeys: map[string]bool{},
	}
	for _, id := range variantIDLine.FindAllStringSubmatch(block(premiumEntityStylesBlock, "premiumEntityStyles"), -1) {
		registry.liveIDs[normalizeRendererStyleKey(id[1])] = true
	}
	for _, pair := range stringMapEntry.FindAllStringSubmatch(block(entityStyleAliasesBlock, "entityStyleAliases"), -1) {
		registry.aliasTargets[normalizeRendererStyleKey(pair[1])] = normalizeRendererStyleKey(pair[2])
	}
	for _, key := range selectorKeyLine.FindAllStringSubmatch(block(entityTagSelectorsBlock, "entityTagSelectors"), -1) {
		registry.selectorKeys[normalizeRendererStyleKey(key[1])] = true
	}
	if len(registry.liveIDs) == 0 || len(registry.aliasTargets) == 0 || len(registry.selectorKeys) == 0 {
		t.Fatalf("parsed an empty entity-style registry (%d live, %d aliases, %d selectors); refusing to assert against nothing",
			len(registry.liveIDs), len(registry.aliasTargets), len(registry.selectorKeys))
	}
	return registry
}

// normalizeRendererStyleKey mirrors RenderingGen's normalizeStyleKey: lookup
// there is normalized, so the parity comparison must be too.
func normalizeRendererStyleKey(key string) string {
	norm := strings.ToLower(strings.TrimSpace(key))
	norm = strings.ReplaceAll(norm, "-", "_")
	return strings.ReplaceAll(norm, " ", "_")
}

// TestCertifiedEntityStylesResolveInRenderingGen asserts every certified
// Apple Spatial style still resolves in the renderer: either as a live
// composition or through the alias table, and in the latter case to a live
// composition. A style that resolves nowhere would be certified here and
// refused there.
func TestCertifiedEntityStylesResolveInRenderingGen(t *testing.T) {
	registry := loadEntityStyleRegistrySource(t)
	for _, id := range CertifiedAppleSpatialEntityStyles {
		key := normalizeRendererStyleKey(id)
		if registry.liveIDs[key] {
			continue
		}
		target, aliased := registry.aliasTargets[key]
		if !aliased {
			t.Errorf("certified entity style %q resolves in neither premiumEntityStyles nor entityStyleAliases", id)
			continue
		}
		if !registry.liveIDs[target] {
			t.Errorf("certified entity style %q aliases to %q, which is not a live RenderingGen composition", id, target)
		}
	}
}

// TestRenderingGenEntityStylesAreAllCertified is the other direction: a
// composition added to the RenderingGen registry without being certified here
// would be unreachable by the planner, which may only stamp and transport
// selectors this vocabulary knows.
func TestRenderingGenEntityStylesAreAllCertified(t *testing.T) {
	registry := loadEntityStyleRegistrySource(t)
	certified := map[string]bool{}
	for _, id := range CertifiedAppleSpatialEntityStyles {
		certified[normalizeRendererStyleKey(id)] = true
	}
	for id := range registry.liveIDs {
		if !certified[id] {
			t.Errorf("RenderingGen composition %q is live but not listed in CertifiedAppleSpatialEntityStyles", id)
		}
	}
}

// TestCertifiedEntityStyleSelectorsAreRendererSelectors asserts the certified
// selector words are exactly the words ResolveEntityStyle accepts as tag
// queries; a certified selector the renderer does not know would fail closed
// only at render time.
func TestCertifiedEntityStyleSelectorsAreRendererSelectors(t *testing.T) {
	registry := loadEntityStyleRegistrySource(t)
	for _, selector := range CertifiedEntityStyleSelectors {
		if !registry.selectorKeys[normalizeRendererStyleKey(selector)] {
			t.Errorf("certified entity_style_id selector %q is not a key of RenderingGen's entityTagSelectors", selector)
		}
	}
}

// TestCertifiedEntityStyleVocabularyHasNoDuplicates keeps both handwritten
// lists honest: a duplicated entry silently narrows or skews whatever samples
// from them.
func TestCertifiedEntityStyleVocabularyHasNoDuplicates(t *testing.T) {
	assertNoDuplicateEntityStyleEntries(t, "CertifiedAppleSpatialEntityStyles", CertifiedAppleSpatialEntityStyles)
	assertNoDuplicateEntityStyleEntries(t, "CertifiedEntityStyleSelectors", CertifiedEntityStyleSelectors)
}

func assertNoDuplicateEntityStyleEntries(t *testing.T, name string, entries []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, entry := range entries {
		key := normalizeRendererStyleKey(entry)
		if seen[key] {
			t.Errorf("%s lists %q more than once", name, entry)
		}
		seen[key] = true
	}
}
