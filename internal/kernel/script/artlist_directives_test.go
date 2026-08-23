package script

import "testing"

func TestParseArtlistDirectivesRemovesDirectiveAndPreservesQueries(t *testing.T) {
	clean, queries := ParseArtlistDirectives("1. Bread\n[ARTLIST: bread, fresh bread, bakery bread]\nBread is ancient.")
	if clean != "1. Bread\nBread is ancient." {
		t.Fatalf("clean text = %q", clean)
	}
	if len(queries) != 3 || queries[0] != "bread" || queries[2] != "bakery bread" {
		t.Fatalf("queries = %#v", queries)
	}
}

func TestArtlistSearchIntentHashChangesWhenExplicitQueryChanges(t *testing.T) {
	if ArtlistSearchIntentHash([]string{"bread"}) == ArtlistSearchIntentHash([]string{"sourdough bread"}) {
		t.Fatal("different explicit Artlist intents must not share a cache identity")
	}
}
