package entities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEntityIndex_TestC_InsertExactSemantic is the plan's Test C: index Tim
// Cook, Apple and artificial intelligence; then exact lookup("Tim Cook") →
// the exact entity, and semantic("Apple executive") → Tim Cook.
func TestEntityIndex_TestC_InsertExactSemantic(t *testing.T) {
	ix := NewEntityIndex()
	require.NoError(t, ix.IndexAll(
		NewEntityRecord("PERSON", "Tim Cook", "Apple CEO", "Apple executive"),
		NewEntityRecord("ORGANIZATION", "Apple"),
		NewEntityRecord("CONCEPT", "artificial intelligence"),
	))
	require.Equal(t, 3, ix.Len())

	// ── Exact lookup (relational DB) ──────────────────────────────
	rec, ok := ix.LookupExact("Tim Cook")
	require.True(t, ok, "exact lookup must resolve Tim Cook")
	require.Equal(t, "Tim Cook", rec.CanonicalName)
	require.Equal(t, "PERSON", rec.EntityType)
	require.Equal(t, StableEntityID("PERSON", "Tim Cook"), rec.EntityID)

	// Case/whitespace-insensitive: the same record, always.
	rec, ok = ix.LookupExact("  TIM   COOK ")
	require.True(t, ok)
	require.Equal(t, rec.EntityID, StableEntityID("PERSON", "Tim Cook"))

	// Aliases participate in exact lookup: "Apple CEO" → Tim Cook.
	rec, ok = ix.LookupExact("Apple CEO")
	require.True(t, ok)
	require.Equal(t, "Tim Cook", rec.CanonicalName)

	// Unknown surface → not found.
	_, ok = ix.LookupExact("Elon Musk")
	require.False(t, ok)

	// ── Semantic lookup ───────────────────────────────────────────
	rec, score, ok := ix.LookupSemantic("Apple executive")
	require.True(t, ok, "semantic lookup must resolve the query")
	require.Equal(t, "Tim Cook", rec.CanonicalName, "Apple executive → Tim Cook")
	require.GreaterOrEqual(t, score, MinSemanticScore)

	// A query that matches nothing is rejected, not guessed.
	_, _, ok = ix.LookupSemantic("quantum computing")
	require.False(t, ok)
}

// TestEntityIndex_ExactLookupIsSourceOfTruth pins that the structured record
// (not the matcher) is the SSOT: exact lookup returns the canonical name,
// type and stable id exactly as indexed.
func TestEntityIndex_ExactLookupIsSourceOfTruth(t *testing.T) {
	ix := NewEntityIndex()
	require.NoError(t, ix.IndexAll(
		NewEntityRecord("GPE", "Los Angeles"),
		NewEntityRecord("PERSON", "Tom Hanks"),
	))
	rec, ok := ix.LookupExact("Los Angeles")
	require.True(t, ok)
	require.Equal(t, "GPE", rec.EntityType)
	require.Equal(t, "los angeles", rec.NormalizedName)
	require.Equal(t, StableEntityID("GPE", "Los Angeles"), rec.EntityID)

	rec, ok = ix.LookupExact("tom hanks")
	require.True(t, ok)
	require.Equal(t, "Tom Hanks", rec.CanonicalName)
	require.Equal(t, "PERSON", rec.EntityType)
}

// TestEntityIndex_DedupByStableID pins the content-addressed dedup: indexing
// the same canonical entity twice (as it happens across scenes/runs) keeps
// ONE record — the second upsert refreshes it instead of duplicating it.
func TestEntityIndex_DedupByStableID(t *testing.T) {
	ix := NewEntityIndex()
	require.NoError(t, ix.Index(NewEntityRecord("PERSON", "Tim Cook", "Apple CEO")))
	require.NoError(t, ix.Index(NewEntityRecord("PERSON", "Tim Cook", "Apple CEO", "Cupertino resident")))
	require.Equal(t, 1, ix.Len(), "same stable id must dedup to one record")

	rec, ok := ix.LookupExact("Cupertino resident")
	require.True(t, ok, "refreshed aliases must be searchable")
	require.Equal(t, "Tim Cook", rec.CanonicalName)
}

// TestEntityIndex_RejectsContentAddressMismatch pins the fail-closed SSOT
// invariant: a record whose id does not match its canonical (type, name)
// hash is rejected — it would poison dedup and cache.
func TestEntityIndex_RejectsContentAddressMismatch(t *testing.T) {
	ix := NewEntityIndex()
	err := ix.Index(EntityRecord{
		EntityID:       "ent_wrong",
		CanonicalName:  "Tim Cook",
		EntityType:     "PERSON",
		NormalizedName: "tim cook",
	})
	require.ErrorIs(t, err, ErrInvalidEntityRecord)
	require.Equal(t, 0, ix.Len(), "an invalid record must never index")

	// An empty entity also fails closed.
	err = ix.Index(EntityRecord{EntityID: StableEntityID("PERSON", ""), CanonicalName: "", EntityType: "PERSON"})
	require.ErrorIs(t, err, ErrInvalidEntityRecord)
}

// TestEntityIndex_SemanticLookuperIsPluggable pins the pluggable semantic
// backend: a custom SemanticLookuper (e.g. a future vector index) replaces
// the default token-overlap matcher without touching the repository.
func TestEntityIndex_SemanticLookuperIsPluggable(t *testing.T) {
	ix := NewEntityIndex()
	require.NoError(t, ix.IndexAll(
		NewEntityRecord("PERSON", "Tim Cook"),
		NewEntityRecord("ORGANIZATION", "Apple"),
	))
	// Stub vector backend: always returns the first record with score 1.
	ix.SetSemanticLookuper(stubSemanticLookuper{})
	rec, score, ok := ix.LookupSemantic("anything")
	require.True(t, ok)
	require.Equal(t, "Tim Cook", rec.CanonicalName)
	require.Equal(t, 1.0, score)

	// The repository (exact lookup) is untouched by the swap.
	rec, ok = ix.LookupExact("Apple")
	require.True(t, ok)
	require.Equal(t, "ORGANIZATION", rec.EntityType)
}

// stubSemanticLookuper is a deterministic stand-in for a vector backend.
type stubSemanticLookuper struct{}

func (stubSemanticLookuper) Lookup(_ string, entities []EntityRecord) (EntityRecord, float64, bool) {
	if len(entities) == 0 {
		return EntityRecord{}, 0, false
	}
	return entities[0], 1.0, true
}

// TestEntityIndex_SemanticMatchesAliases pins that the default matcher
// searches aliases too: a descriptor alias ("Apple executive") makes the
// entity reachable by a free-text query even when the canonical name does
// not contain the query words.
func TestEntityIndex_SemanticMatchesAliases(t *testing.T) {
	ix := NewEntityIndex()
	require.NoError(t, ix.IndexAll(
		NewEntityRecord("PERSON", "Tim Cook", "Apple CEO", "Apple executive"),
		NewEntityRecord("ORGANIZATION", "Apple"),
	))
	rec, score, ok := ix.LookupSemantic("Apple executive")
	require.True(t, ok)
	require.Equal(t, "Tim Cook", rec.CanonicalName)
	require.Equal(t, 1.0, score, "both query tokens covered by the aliases")

	// A partial overlap (only "apple") is below full coverage but still
	// resolvable: Apple wins over Tim Cook on token coverage.
	rec, _, ok = ix.LookupSemantic("Apple")
	require.True(t, ok)
	require.Equal(t, "Apple", rec.CanonicalName)
}

// TestEntityIndex_AllIsDeterministic pins the repository iteration contract:
// All() returns records in stable entity-id order, so semantic scoring and
// iteration never depend on map order.
func TestEntityIndex_AllIsDeterministic(t *testing.T) {
	repo := NewInMemoryEntityRepository()
	require.NoError(t, repo.Upsert(NewEntityRecord("PERSON", "Tim Cook")))
	require.NoError(t, repo.Upsert(NewEntityRecord("ORGANIZATION", "Apple")))
	require.NoError(t, repo.Upsert(NewEntityRecord("CONCEPT", "artificial intelligence")))

	all := repo.All()
	require.Len(t, all, 3)
	for i := 1; i < len(all); i++ {
		require.Less(t, all[i-1].EntityID, all[i].EntityID, "records must be in stable id order")
	}
}

// TestEntityIndex_EmptyQueryFailsClosed pins that an empty exact or semantic
// lookup never returns a guessed entity.
func TestEntityIndex_EmptyQueryFailsClosed(t *testing.T) {
	ix := NewEntityIndex()
	require.NoError(t, ix.Index(NewEntityRecord("PERSON", "Tim Cook")))

	_, ok := ix.LookupExact("   ")
	require.False(t, ok)
	_, _, ok = ix.LookupSemantic("")
	require.False(t, ok)
}
