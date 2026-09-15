package mediaregistry

import (
	"sort"
	"strings"
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
	// whoosh1..whoosh3 are BOUND (canonical) so random_whoosh resolves; the
	// unbound rest of the family and the directive itself stay compat-only.
	retired := []string{"whoop1", "whoop2", "whoop3", "whoop4", "whoosh4", "whoosh9", "random_whoosh"}
	sort.Strings(retired)
	for _, alias := range retired {
		if _, ok := catalog[alias]; ok {
			t.Errorf("retired alias %q must not be canonical editorial content", alias)
		}
	}
}

// TestEveryWhooshFamilyAliasIsBoundByTheCatalog is the regression test for the
// random_whoosh dead path: the kernel declares the family the directive selects
// from, but only the catalog can bind an alias to a resolvable Drive identity.
// If a family member is not bound, random_whoosh can emit an asset id that the
// media registry cannot resolve — which is exactly the bug this pins shut.
//
// It fails in BOTH directions: an unbound family member, and a bound whoosh the
// family omits (which would make a resolvable alias unreachable via the
// directive).
func TestEveryWhooshFamilyAliasIsBoundByTheCatalog(t *testing.T) {
	catalog := EditorialAudioAliases()
	family := scriptkernel.BuiltInWhooshAliases()
	if len(family) == 0 {
		t.Fatal("the whoosh family is empty; random_whoosh cannot expand")
	}
	inFamily := make(map[string]struct{}, len(family))
	for _, alias := range family {
		inFamily[alias] = struct{}{}
		identity, bound := catalog[alias]
		if !bound {
			t.Errorf("random_whoosh can select %q, but the catalog binds no identity for it (the directive would emit a dead asset id)", alias)
		} else if strings.TrimSpace(identity) == "" {
			// Presence alone is not resolvability: an alias declared with an
			// empty identity still resolves to a dead asset id, which is the
			// exact failure the directive expansion is supposed to prevent.
			t.Errorf("random_whoosh can select %q, but its catalog identity is empty — the alias is declared without a Drive binding, so it is still not resolvable", alias)
		}
		asset, ok := lookupEditorialAudioAsset(alias)
		if ok && asset.Subtype != "whoosh" {
			t.Errorf("whoosh family member %q has subtype %q, want whoosh", alias, asset.Subtype)
		}
	}
	for alias := range catalog {
		asset, _ := lookupEditorialAudioAsset(alias)
		if asset.Subtype == "whoosh" {
			if _, ok := inFamily[alias]; !ok {
				t.Errorf("bound whoosh alias %q is missing from the random_whoosh family, so the directive can never select it", alias)
			}
		}
	}
}
