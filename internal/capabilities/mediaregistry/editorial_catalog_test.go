package mediaregistry

import (
	"strings"
	"testing"
)

// assertEditorialAudioCatalogCanonical is the oracle for the catalog invariant:
// one public alias and one Drive identity may belong to one editorial role.
func assertEditorialAudioCatalogCanonical(t *testing.T) {
	t.Helper()
	aliases := make(map[string]struct{}, len(editorialAudioAssets))
	identities := make(map[string]string, len(editorialAudioAssets))
	for _, asset := range editorialAudioAssets {
		if strings.TrimSpace(asset.Alias) == "" || strings.TrimSpace(asset.DriveFileID) == "" {
			t.Fatalf("editorial audio catalog: alias and Drive identity are required")
		}
		if _, exists := aliases[asset.Alias]; exists {
			t.Fatalf("editorial audio catalog: duplicate alias %q", asset.Alias)
		}
		aliases[asset.Alias] = struct{}{}
		if previous, exists := identities[asset.DriveFileID]; exists && previous != asset.Alias {
			t.Fatalf("editorial audio catalog: Drive identity %q is shared by %q and %q", asset.DriveFileID, previous, asset.Alias)
		}
		identities[asset.DriveFileID] = asset.Alias
	}
}

func TestEditorialAudioCatalogIsCanonicalAndCollisionFree(t *testing.T) {
	assertEditorialAudioCatalogCanonical(t)
	assets := EditorialAudioAssets()
	if len(assets) != 15 {
		t.Fatalf("editorial audio assets = %d, want 15 canonical BGM/SFX assets", len(assets))
	}
	aliases := EditorialAudioAliases()
	for _, want := range []string{"bgm1", "bgm2", "bgm3", "bgm4", "bgm5", "bgm6", "whop1", "whop2", "whop3", "whop4", "whop5", "whop6", "whoosh1", "whoosh2", "whoosh3"} {
		if aliases[want] == "" {
			t.Fatalf("canonical alias %q is missing", want)
		}
	}
	// whoop* stays retired: it was an ambiguous alias space sharing one Drive
	// identity with the BGM catalog. whoosh4..whoosh9 remain unbound compat
	// vocabulary, so they must not become canonical either.
	for _, retired := range []string{"whoop1", "whoop2", "whoosh4", "whoosh5"} {
		if _, ok := aliases[retired]; ok {
			t.Fatalf("ambiguous legacy alias %q must not be canonical", retired)
		}
	}
}

func TestEditorialTransitionAliasesCoverBothTransitionSubtypes(t *testing.T) {
	aliases := make([]string, 0, len(editorialAudioAssets))
	for _, asset := range EditorialAudioAssets() {
		if asset.Family == "transition" {
			aliases = append(aliases, asset.Alias)
		}
	}
	if len(aliases) == 0 {
		t.Fatal("no transition aliases are declared")
	}
	subtypes := make(map[string]bool)
	for _, alias := range aliases {
		asset, ok := lookupEditorialAudioAsset(alias)
		if !ok {
			t.Fatalf("transition alias %q is not bound by the catalog", alias)
		}
		if asset.Family != "transition" {
			t.Errorf("transition alias %q family = %q", alias, asset.Family)
		}
		subtypes[asset.Subtype] = true
	}
	for _, want := range []string{"whop", "whoosh"} {
		if !subtypes[want] {
			t.Errorf("transition subtype %q is missing from the editorial catalog", want)
		}
	}
}

func TestEditorialAudioAssetsReturnsDefensiveCopy(t *testing.T) {
	assets := EditorialAudioAssets()
	assets[0].BestFor[0] = "mutated"
	assets[0].Tags[0] = "mutated"
	assets[0].Alias = "mutated"
	fresh := EditorialAudioAssets()
	if fresh[0].Alias == "mutated" || fresh[0].BestFor[0] == "mutated" || fresh[0].Tags[0] == "mutated" {
		t.Fatal("editorial catalog leaked mutable state")
	}
}
