package entities

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
)

// TestStableEntityIDFoldsTypeAliases is the identity contract the ticket
// asked for: the SAME place spelled by two extractors must mint the SAME id.
// Before the alias table, GPE and LOCATION produced two ids, the resolver
// stored one and the plan looked up the other, and the entity reported zero
// images.
func TestStableEntityIDFoldsTypeAliases(t *testing.T) {
	gpe := StableEntityID("GPE", "Garlasco")
	for _, spelling := range []string{"LOCATION", "CITY", "PLACE", "COUNTRY", "LOC", "location"} {
		require.Equal(t, gpe, StableEntityID(spelling, "Garlasco"), "%s must fold onto PLACE", spelling)
	}
	require.Equal(t, "ent_f9441d9fd86969f3", gpe, "the canonical PLACE literal is pinned")

	require.Equal(t, CanonicalEntityID("GPE", "Garlasco"), CanonicalEntityID("LOCATION", "Garlasco"))
	require.Equal(t, "place:garlasco", CanonicalEntityID("LOCATION", "Garlasco"))

	// A genuinely different type stays a different entity.
	require.NotEqual(t, gpe, StableEntityID("PERSON", "Garlasco"))
	require.NotEqual(t, StableEntityID("ORG", "Garlasco"), gpe)
}

// TestCanonicalEntityTypesAreClosed keeps the alias table honest: every alias
// must target a declared canonical type, every canonical type must map to
// itself, and no two canonical types may share a spelling.
func TestCanonicalEntityTypesAreClosed(t *testing.T) {
	for _, canonical := range CanonicalEntityTypes() {
		require.Equal(t, canonical, NormalizeType(canonical), "canonical type %q must be stable", canonical)
	}
	for alias, canonical := range entitytype.AliasSpellings() {
		require.True(t, entitytype.IsCanonical(canonical), "alias %q targets undeclared canonical type %q", alias, canonical)
		require.Equal(t, canonical, NormalizeType(alias))
	}
	require.NotEmpty(t, CanonicalEntityTypes())
}

// TestEntityTypeAliasesEnumeratesLegacySpellings pins the compat enumeration:
// canonical first, legacy spellings after it, deterministic order.
func TestEntityTypeAliasesEnumeratesLegacySpellings(t *testing.T) {
	aliases := EntityTypeAliases("gpe")
	require.Equal(t, "PLACE", aliases[0])
	require.Contains(t, aliases, "GPE")
	require.Contains(t, aliases, "LOCATION")
	require.Contains(t, aliases, "CITY")
	require.Equal(t, EntityTypeAliases("LOCATION"), aliases, "every spelling of a type shares one enumeration")

	require.Equal(t, []string{"SATELLITE"}, EntityTypeAliases("satellite"))
	require.Nil(t, EntityTypeAliases("   "))
}

// TestFoldCanonicalEntityIDTranslatesAlreadyWrittenIDs pins the compatibility
// READ: ids persisted before the fold are translated without renaming their
// slug or inventing a second format.
func TestFoldCanonicalEntityIDTranslatesAlreadyWrittenIDs(t *testing.T) {
	cases := map[string]string{
		"gpe:garlasco":           "place:garlasco",
		"GPE:Los-Angeles":        "place:Los-Angeles", // slug preserved verbatim
		"location:times-square":  "place:times-square",
		"city:garlasco":          "place:garlasco",
		"organization:acme-corp": "org:acme-corp",
		"brand:acme":             "logo:acme",
		"work:the-odyssey":       "work_of_art:the-odyssey",
		"person:michael-jordan":  "person:michael-jordan",
		"place:garlasco":         "place:garlasco",
		"ent_6bee252f34e49fa3":   "ent_6bee252f34e49fa3", // stable id: untouched
		"garlasco":               "garlasco",             // bare slug: untouched
		"  gpe:garlasco  ":       "place:garlasco",
		"":                       "",
		"   ":                    "",
		"gpe:":                   "gpe:",
	}
	for in, want := range cases {
		require.Equal(t, want, FoldCanonicalEntityID(in), "FoldCanonicalEntityID(%q)", in)
	}
}

// TestCanonicalEntityIDCandidatesFindsPreMigrationRows pins the catalog read
// path: the canonical id leads, and every id an older spelling would have
// written is still reachable.
func TestCanonicalEntityIDCandidatesFindsPreMigrationRows(t *testing.T) {
	candidates := CanonicalEntityIDCandidates("LOCATION", "Garlasco")
	require.NotEmpty(t, candidates)
	require.Equal(t, "place:garlasco", candidates[0], "the canonical id must lead")
	require.Contains(t, candidates, "gpe:garlasco")
	require.Contains(t, candidates, "location:garlasco")
	require.Contains(t, candidates, "city:garlasco")
	require.Contains(t, candidates, "country:garlasco")

	seen := map[string]bool{}
	for _, id := range candidates {
		require.False(t, seen[id], "duplicate candidate %q", id)
		seen[id] = true
	}

	// An unknown type has no legacy spellings: exactly one candidate.
	require.Equal(t, []string{"satellite:iss"}, CanonicalEntityIDCandidates("satellite", "ISS"))
	require.Nil(t, CanonicalEntityIDCandidates("PERSON", "   "))
}

// TestCanonicalEntityIDFoldsPersonAliases pins that the person possessive
// rule follows the folded type, not just the literal PERSON spelling.
func TestCanonicalEntityIDFoldsPersonAliases(t *testing.T) {
	require.Equal(t, "person:donald-trump", CanonicalEntityID("CEO", "Donald Trump's"))
	require.Equal(t, "place:garlasco", CanonicalEntityID("GPE", "Garlasco"))
}
