package entitycatalog

import (
	"errors"
	"testing"
)

func TestPersonAliasGraphResolvesManyAliasesToOneIdentity(t *testing.T) {
	graph := NewPersonAliasGraph()
	if err := graph.Register("person:john-gotti", "John Gotti", "Teflon Don", "Dapper Don"); err != nil {
		t.Fatalf("register aliases: %v", err)
	}
	for _, alias := range []string{"John Gotti", "Teflon Don", "Dapper Don", "  TEFLON   DON  "} {
		identity, err := CanonicalizePersonNameWithAliases(alias, graph)
		if err != nil {
			t.Fatalf("CanonicalizePersonNameWithAliases(%q): %v", alias, err)
		}
		if identity.CanonicalEntityID != "person:john-gotti" {
			t.Fatalf("%q resolved to %q, want person:john-gotti", alias, identity.CanonicalEntityID)
		}
	}
	aliases := graph.Aliases("person:john-gotti")
	want := []string{"dapper don", "john gotti", "teflon don"}
	if len(aliases) != len(want) {
		t.Fatalf("aliases = %v, want %v", aliases, want)
	}
	for i := range want {
		if aliases[i] != want[i] {
			t.Fatalf("aliases = %v, want %v", aliases, want)
		}
	}
}

func TestPersonAliasGraphLeavesHomonymUnresolved(t *testing.T) {
	graph := NewPersonAliasGraph()
	if err := graph.Register("person:john-smith--actor", "John Smith"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Register("person:john-smith--director", "John Smith"); err != nil {
		t.Fatal(err)
	}
	identity, err := CanonicalizePersonNameWithAliases("John Smith", graph)
	if !errors.Is(err, ErrAmbiguousPersonName) {
		t.Fatalf("expected ErrAmbiguousPersonName, got identity=%+v err=%v", identity, err)
	}
	if !identity.Unresolved || len(identity.Candidates) != 2 {
		t.Fatalf("ambiguous identity = %+v, want unresolved with two candidates", identity)
	}
	if _, err := CanonicalizePersonIdentityWithAliases("John Smith", "", graph); !errors.Is(err, ErrAmbiguousPersonName) {
		t.Fatalf("expected unresolved homonym without a supplied id, got %v", err)
	}
	selected, err := CanonicalizePersonIdentityWithAliases("John Smith", "person:john-smith--actor", graph)
	if err != nil {
		t.Fatalf("select actor identity: %v", err)
	}
	if selected.CanonicalEntityID != "person:john-smith--actor" {
		t.Fatalf("selected = %q, want actor", selected.CanonicalEntityID)
	}
}

func TestCanonicalPersonIDWithDisambiguatorIsStableAndDistinct(t *testing.T) {
	actor, err := CanonicalPersonIDWithDisambiguator("John Smith", "actor")
	if err != nil {
		t.Fatal(err)
	}
	director, err := CanonicalPersonIDWithDisambiguator("John Smith", "director")
	if err != nil {
		t.Fatal(err)
	}
	if actor.CanonicalEntityID != "person:john-smith--actor" {
		t.Fatalf("actor id = %q", actor.CanonicalEntityID)
	}
	if actor.CanonicalEntityID == director.CanonicalEntityID {
		t.Fatalf("homonyms collided on %q", actor.CanonicalEntityID)
	}
	if actor.Disambiguator != "actor" || director.Disambiguator != "director" {
		t.Fatalf("disambiguators = %q / %q", actor.Disambiguator, director.Disambiguator)
	}
	// A supplied disambiguated id is accepted even without a graph; the base
	// surface alone is not silently rewritten into a homonym.
	selected, err := CanonicalizePersonIdentity("John Smith", actor.CanonicalEntityID)
	if err != nil {
		t.Fatalf("CanonicalizePersonIdentity with explicit disambiguator: %v", err)
	}
	if selected.CanonicalEntityID != actor.CanonicalEntityID {
		t.Fatalf("selected = %q, want %q", selected.CanonicalEntityID, actor.CanonicalEntityID)
	}
}
