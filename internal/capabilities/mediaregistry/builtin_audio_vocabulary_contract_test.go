package mediaregistry

import (
	"sort"
	"testing"

	scriptkernel "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestEditorialCatalogMatchesTheBuiltInAudioVocabulary is the drift gate that
// makes the audio consolidation enforceable instead of aspirational.
//
// The built-in alias vocabulary is declared in kernel
// (internal/kernel/script/builtin_audio_aliases.go) because
// percheck_kernel_boundary forbids kernel -> capabilities, so the wire decoder
// cannot read this package. That leaves exactly one way for the two to agree:
// this test. It fails in BOTH directions — a catalog alias the kernel does not
// declare, and a kernel-declared canonical alias this catalog does not bind —
// so neither side can gain or retire an editorial alias alone.
func TestEditorialCatalogMatchesTheBuiltInAudioVocabulary(t *testing.T) {
	catalog := EditorialAudioAliases()
	declared := scriptkernel.BuiltInAudioCanonicalAliases()

	declaredSet := make(map[string]struct{}, len(declared))
	for _, alias := range declared {
		declaredSet[alias] = struct{}{}
	}
	for _, alias := range declared {
		if _, ok := catalog[alias]; !ok {
			t.Errorf("kernel declares built-in alias %q but the editorial catalog does not bind it", alias)
		}
	}
	for alias := range catalog {
		if _, ok := declaredSet[alias]; !ok {
			t.Errorf("editorial catalog binds %q but kernel does not declare it as built-in", alias)
		}
	}

	if len(catalog) != len(declared) {
		t.Errorf("catalog binds %d aliases, kernel declares %d", len(catalog), len(declared))
	}
}

// TestEditorialCatalogKindMatchesTheBuiltInAudioKind keeps the two halves of a
// single concept from splitting: the catalog owns Family/Subtype, kernel owns
// the wire kind. A BGM must decode as background music and an SFX as a sound
// effect, so the same alias cannot mean two different things depending on which
// side of the wire you ask.
func TestEditorialCatalogKindMatchesTheBuiltInAudioKind(t *testing.T) {
	for _, asset := range EditorialAudioAssets() {
		kind, ok := scriptkernel.BuiltInAudioAliasKind(asset.Alias)
		if !ok {
			t.Errorf("alias %q binds no wire kind", asset.Alias)
			continue
		}
		want := scriptkernel.BuiltInAudioSoundEffect
		if asset.Family == "music" {
			want = scriptkernel.BuiltInAudioBackgroundMusic
		}
		if kind != want {
			t.Errorf("alias %q: wire kind %q, want %q (family=%q subtype=%q)",
				asset.Alias, kind, want, asset.Family, asset.Subtype)
		}
	}
}

// TestEditorialCatalogDeclaresNoCompatOnlyAlias pins the boundary between the
// canonical catalog and the compat vocabulary: an alias may be accepted on the
// wire for backward compatibility without being editable editorial content.
// whoop* used to be exactly that case (the same Drive identity addressed as a
// BGM and as a whoop), which is why the catalog retired it.
func TestEditorialCatalogDeclaresNoCompatOnlyAlias(t *testing.T) {
	catalog := EditorialAudioAliases()
	retired := []string{"whoop1", "whoop2", "whoop3", "whoop4", "whoosh1", "whoosh2", "random_whoosh"}
	sort.Strings(retired)
	for _, alias := range retired {
		if _, ok := catalog[alias]; ok {
			t.Errorf("retired alias %q must not be canonical editorial content", alias)
		}
	}
}
