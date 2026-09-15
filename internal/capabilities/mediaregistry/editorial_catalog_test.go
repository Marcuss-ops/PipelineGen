package mediaregistry

import "testing"

func TestEditorialAudioCatalogIsCanonicalAndCollisionFree(t *testing.T) {
	if err := ValidateEditorialAudioCatalog(); err != nil {
		t.Fatal(err)
	}
	assets := EditorialAudioAssets()
	if len(assets) != 12 {
		t.Fatalf("editorial audio assets = %d, want 12 canonical BGM/SFX assets", len(assets))
	}
	aliases := EditorialAudioAliases()
	for _, want := range []string{"bgm1", "bgm2", "bgm3", "bgm4", "bgm5", "bgm6", "whop1", "whop2", "whop3", "whop4", "whop5", "whop6"} {
		if aliases[want] == "" {
			t.Fatalf("canonical alias %q is missing", want)
		}
	}
	for _, retired := range []string{"whoop1", "whoop2", "whoosh1", "whoosh2"} {
		if _, ok := aliases[retired]; ok {
			t.Fatalf("ambiguous legacy alias %q must not be canonical", retired)
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
