package entities

import (
	"strings"
)

// SemanticLookuper resolves a free-text query ("Apple executive") to the
// best-matching entity. The plan's semantic/vector indexing is a pluggable
// layer: a vector index can be dropped in behind this interface without
// touching the repository — but the source of truth STAYS the structured
// entity record the repository returns.
type SemanticLookuper interface {
	// Lookup returns the best-matching entity, its score in [0,1], and
	// whether the match is confident enough to act on.
	Lookup(query string, entities []EntityRecord) (EntityRecord, float64, bool)
}

// MinSemanticScore is the minimum token-coverage score the default matcher
// accepts: at least half of the query tokens must be covered by the entity's
// searchable text.
const MinSemanticScore = 0.5

// tokenOverlapLookuper is the DEFAULT deterministic semantic matcher. It
// scores every entity by the fraction of query tokens found in its
// searchable text (canonical name + normalized name + type + aliases) and
// returns the highest-scoring entity when that score clears MinSemanticScore.
//
// Tie-break: at equal total coverage, the entity whose CANONICAL name covers
// more query tokens wins — so a bare query "Apple" prefers the ORGANIZATION
// Apple over Tim Cook (whose alias "Apple CEO" also contains the token),
// while "Apple executive" still resolves to Tim Cook (higher total
// coverage). Remaining ties are broken by stable entity id order, so the
// matcher is fully deterministic.
//
// It is deliberately pure and deterministic (no external model), so the
// index works hermetically in tests and offline; a real vector index can
// replace it via SetSemanticLookuper.
type tokenOverlapLookuper struct{}

// Lookup implements SemanticLookuper.
func (tokenOverlapLookuper) Lookup(query string, entities []EntityRecord) (EntityRecord, float64, bool) {
	queryTokens := tokenSet(NormalizeName(query))
	if len(queryTokens) == 0 {
		return EntityRecord{}, 0, false
	}
	var best EntityRecord
	var bestTotal, bestCanonical float64
	for _, rec := range entities {
		total := tokenCoverage(queryTokens, searchableTokenSet(rec))
		canonical := tokenCoverage(queryTokens, tokenSet(NormalizeName(rec.CanonicalName)))
		if total > bestTotal || (total == bestTotal && canonical > bestCanonical) {
			best, bestTotal, bestCanonical = rec, total, canonical
		}
	}
	if bestTotal < MinSemanticScore {
		return EntityRecord{}, bestTotal, false
	}
	return best, bestTotal, true
}

// tokenCoverage returns the fraction of query tokens present in the
// candidate's token set.
func tokenCoverage(queryTokens map[string]struct{}, candidateTokens map[string]struct{}) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	matched := 0
	for tok := range queryTokens {
		if _, ok := candidateTokens[tok]; ok {
			matched++
		}
	}
	return float64(matched) / float64(len(queryTokens))
}

// searchableTokenSet builds the deterministic token set an entity is
// matched against: canonical name, normalized name, type and every alias.
func searchableTokenSet(rec EntityRecord) map[string]struct{} {
	var b strings.Builder
	b.WriteString(NormalizeName(rec.CanonicalName))
	b.WriteByte(' ')
	b.WriteString(NormalizeName(rec.NormalizedName))
	b.WriteByte(' ')
	b.WriteString(NormalizeType(rec.EntityType))
	for _, alias := range rec.Aliases {
		b.WriteByte(' ')
		b.WriteString(NormalizeName(alias))
	}
	return tokenSet(b.String())
}

// tokenSet splits normalized text into its unique word tokens.
func tokenSet(text string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, tok := range strings.Fields(text) {
		set[tok] = struct{}{}
	}
	return set
}

// EntityIndex is the entity lookup index: exact lookup (the relational
// repository, source of truth) + semantic lookup (pluggable matcher). This
// is the plan's index used by the overlay planner and asset resolver: given
// a surface name it returns the stable entity, and given a free-text query
// it returns the best candidate without ever replacing the structured
// record.
type EntityIndex struct {
	repo     EntityRepository
	semantic SemanticLookuper
}

// NewEntityIndex returns an index backed by a fresh in-memory repository and
// the default token-overlap semantic matcher. The composition root can swap
// either backend (SQLite adapter / vector index) via the setters.
func NewEntityIndex() *EntityIndex {
	return &EntityIndex{
		repo:     NewInMemoryEntityRepository(),
		semantic: tokenOverlapLookuper{},
	}
}

// SetRepository swaps the backing store, e.g. a durable SQLite adapter.
func (ix *EntityIndex) SetRepository(repo EntityRepository) {
	if repo != nil {
		ix.repo = repo
	}
}

// SetSemanticLookuper swaps the semantic backend, e.g. a vector index.
func (ix *EntityIndex) SetSemanticLookuper(s SemanticLookuper) {
	if s != nil {
		ix.semantic = s
	}
}

// Index upserts one entity record (content-addressed dedup by stable id).
func (ix *EntityIndex) Index(rec EntityRecord) error {
	return ix.repo.Upsert(rec)
}

// IndexAll upserts several records and fails closed on the first invalid
// one: a poisoned record never partially indexes.
func (ix *EntityIndex) IndexAll(records ...EntityRecord) error {
	for _, rec := range records {
		if err := ix.repo.Upsert(rec); err != nil {
			return err
		}
	}
	return nil
}

// LookupExact resolves a surface name or alias to its entity via the
// repository (the relational exact lookup).
func (ix *EntityIndex) LookupExact(name string) (EntityRecord, bool) {
	return ix.repo.LookupExact(name)
}

// LookupSemantic resolves a free-text query to the best-matching entity via
// the configured semantic backend.
func (ix *EntityIndex) LookupSemantic(query string) (EntityRecord, float64, bool) {
	if strings.TrimSpace(query) == "" {
		return EntityRecord{}, 0, false
	}
	return ix.semantic.Lookup(query, ix.repo.All())
}

// Len returns the number of indexed entities.
func (ix *EntityIndex) Len() int {
	return ix.repo.Len()
}
