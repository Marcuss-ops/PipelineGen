package detail

import (
	"errors"
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
