package asset

import (
	"errors"
	"reflect"
	"testing"
)

func TestDefaultSourceCatalogCanonicalizesAliases(t *testing.T) {
	catalog := DefaultSourceCatalog()
	cases := map[string]string{
		"artlist":       "artlist",
		"YOUTUBE":       "clips",
		"youtube_clip":  "clips",
		"ai_generated":  "stock",
		"audio":         "voiceover",
		"image":         "images",
		"sound_effects": "sound_effect",
		"sfx":           "sound_effect",
	}
	for input, want := range cases {
		if got := catalog.Canonical(input); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", input, got, want)
		}
	}
	if got := catalog.Canonical("unknown"); got != "" {
		t.Errorf("Canonical(unknown) = %q, want empty", got)
	}
}

func TestDefaultSourceCatalogNamesAreDeterministic(t *testing.T) {
	want := []string{"artlist", "clips", "images", "sound_effect", "stock", "voiceover"}
	if got := DefaultSourceCatalog().Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
}

func TestSourceCatalogBuilderRejectsDuplicateAndAliasConflict(t *testing.T) {
	builder := NewSourceCatalogBuilder()
	if err := builder.Add(SourceDefinition{Canonical: "clips", Aliases: []string{"youtube"}}); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if err := builder.Add(SourceDefinition{Canonical: "clips"}); !errors.Is(err, ErrSourceCatalogDuplicate) {
		t.Fatalf("duplicate Add error = %v, want ErrSourceCatalogDuplicate", err)
	}
	if err := builder.Add(SourceDefinition{Canonical: "video", Aliases: []string{"youtube"}}); !errors.Is(err, ErrSourceCatalogAliasConflict) {
		t.Fatalf("alias conflict error = %v, want ErrSourceCatalogAliasConflict", err)
	}
}

func TestSourceCatalogBuilderBuildCopiesDefinitions(t *testing.T) {
	builder := NewSourceCatalogBuilder()
	if err := builder.Add(SourceDefinition{Canonical: "clips", Aliases: []string{"youtube"}, MediaType: "video"}); err != nil {
		t.Fatal(err)
	}
	catalog := builder.Build()
	defs := catalog.Definitions()
	defs[0].Aliases[0] = "mutated"
	if got := catalog.Canonical("youtube"); got != "clips" {
		t.Fatalf("catalog was mutated through Definitions result: %q", got)
	}
}
