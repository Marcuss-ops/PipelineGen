package entities

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNormalizeName pins the canonical name normalization: trimmed,
// lowercased, internal whitespace collapsed to single spaces. Two surface
// forms that normalize to the same canonical name are the SAME entity for
// dedup and cache purposes.
func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"Tim Cook":           "tim cook",
		"  Tim   Cook  ":     "tim cook",
		"TIM COOK":           "tim cook",
		"Tom Hanks":          "tom hanks",
		"Los Angeles":        "los angeles",
		"\t Apple\n Inc \t ": "apple inc",
		"":                   "",
		"   ":                "",
	}
	for in, want := range cases {
		require.Equal(t, want, NormalizeName(in), "NormalizeName(%q)", in)
	}
}

// TestNormalizeType pins the canonical entity type: trimmed and upper-cased.
// The canonical type is part of the entity identity.
func TestNormalizeType(t *testing.T) {
	require.Equal(t, "PERSON", NormalizeType("person"))
	require.Equal(t, "PERSON", NormalizeType(" PERSON "))
	require.Equal(t, "ORGANIZATION", NormalizeType("organization"))
	require.Equal(t, "GPE", NormalizeType("gpe"))
	require.Equal(t, "", NormalizeType(""))
}

// TestCanonicalKey pins the dedup/cache key: "TYPE:name" with normalized
// type and name. The same entity always yields the same canonical key.
func TestCanonicalKey(t *testing.T) {
	require.Equal(t, "PERSON:tim cook", CanonicalKey("PERSON", "Tim Cook"))
	require.Equal(t, "PERSON:tim cook", CanonicalKey(" person ", "  TIM   COOK "))
	require.Equal(t, "ORGANIZATION:apple inc", CanonicalKey("ORGANIZATION", "Apple Inc"))
	require.Equal(t, "PERSON:tim cook", CanonicalKey("PERSON", "Tim Cook"))
	require.Equal(t, "GPE:los angeles", CanonicalKey("GPE", "Los Angeles"))
}

// TestStableEntityID_Dedup pins the dedup contract (plan Test B): the same
// canonical (type, name) always produces the SAME stable entity ID, no
// matter the surface casing or whitespace — that is what makes entity dedup,
// asset cache reuse and statistics deterministic across scenes, runs and
// repos. No random UUID is ever involved.
func TestStableEntityID_Dedup(t *testing.T) {
	id := StableEntityID("PERSON", "Tim Cook")
	require.True(t, strings.HasPrefix(id, "ent_"), "stable id must carry the ent_ prefix")
	require.Len(t, id, len("ent_")+StableEntityIDLen, "stable id must be ent_ + %d hex chars", StableEntityIDLen)

	// Same canonical entity → same ID regardless of surface form.
	require.Equal(t, id, StableEntityID("PERSON", "Tim Cook"))
	require.Equal(t, id, StableEntityID("PERSON", "tim cook"))
	require.Equal(t, id, StableEntityID(" person ", "  TIM   COOK  "))
	require.Equal(t, id, StableEntityID("PERSON", "Tim Cook"))
}

// TestStableEntityID_DistinguishesType pins that the canonical TYPE is part
// of the identity: the same surface name with a different entity type is a
// DIFFERENT entity (PERSON:tim cook != ORGANIZATION:tim cook), so the stable
// id never collapses distinct entities into one cache key.
func TestStableEntityID_DistinguishesType(t *testing.T) {
	asPerson := StableEntityID("PERSON", "Tim Cook")
	asOrg := StableEntityID("ORGANIZATION", "Tim Cook")
	asProduct := StableEntityID("PRODUCT", "Tim Cook")
	require.NotEqual(t, asPerson, asOrg)
	require.NotEqual(t, asPerson, asProduct)
	require.NotEqual(t, asOrg, asProduct)
}

// TestStableEntityID_DistinguishesName pins that two different canonical
// names never share an entity id (within the 64-bit space).
func TestStableEntityID_DistinguishesName(t *testing.T) {
	ids := map[string]bool{}
	for _, name := range []string{"Tim Cook", "Tom Hanks", "Los Angeles", "Apple", "artificial intelligence"} {
		id := StableEntityID("PERSON", name)
		require.False(t, ids[id], "duplicate stable id for %q", name)
		ids[id] = true
	}
}

// TestStableEntityID_PinsCanonicalValues certifies the exact stable ids of
// the golden entities, so a future change of the canonical key format (or of
// the hash truncation) fails loudly instead of silently rewriting every
// cached asset key.
func TestStableEntityID_PinsCanonicalValues(t *testing.T) {
	require.Equal(t, "ent_"+stableHex("PERSON:tom hanks"), StableEntityID("PERSON", "Tom Hanks"))
	require.Equal(t, "ent_"+stableHex("GPE:los angeles"), StableEntityID("GPE", "Los Angeles"))
	require.Equal(t, "ent_"+stableHex("PERSON:tim cook"), StableEntityID("PERSON", "Tim Cook"))
}

// TestStableEntityID_GoldenOldNew pins the byte-identical migration
// contract: the stable entity ID for each canonical entity MUST equal the
// absolute literal computed by the pre-migration implementation (sha256 of
// the canonical key, truncated to StableEntityIDLen hex chars). If the
// kernel digest SSOT or the canonical key format ever drifts, these
// literals fail loudly.
func TestStableEntityID_GoldenOldNew(t *testing.T) {
	cases := []struct {
		entityType, name string
		want             string
	}{
		{"PERSON", "Tom Hanks", "ent_46579c59f05f045f"},
		{"PERSON", "Tim Cook", "ent_52fd80dd0140190c"},
		{"GPE", "Los Angeles", "ent_6bee252f34e49fa3"},
		{"PERSON", "Michael Jordan", "ent_8be57fefa2fac43f"},
	}
	for _, c := range cases {
		got := StableEntityID(c.entityType, c.name)
		if got != c.want {
			t.Errorf("StableEntityID(%q, %q) = %q, want %q (old hash != new hash)", c.entityType, c.name, got, c.want)
		}
	}
	// Pin the prefix shape.
	for _, c := range cases {
		got := StableEntityID(c.entityType, c.name)
		if len(got) != len("ent_")+StableEntityIDLen {
			t.Errorf("StableEntityID(%q, %q) length = %d, want %d", c.entityType, c.name, len(got), len("ent_")+StableEntityIDLen)
		}
	}
}

// stableHex recomputes the truncated sha256 hex of a canonical key. It is the
// test-side twin of StableEntityID (same sha256 + same truncation), used to
// pin exact values without repeating the production expression.
func stableHex(canonicalKey string) string {
	sum := sha256.Sum256([]byte(canonicalKey))
	return hex.EncodeToString(sum[:])[:StableEntityIDLen]
}
