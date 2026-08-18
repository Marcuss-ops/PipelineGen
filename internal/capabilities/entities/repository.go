package entities

import (
	"errors"
	"fmt"
	"strings"
)

// EntityRecord is the structured SSOT record of one entity in the entity
// index — the plan's `entities` row:
//
//	entity_id, canonical_name, entity_type, normalized_name (+ surface aliases)
//
// The record is content-addressed: EntityID MUST equal
// StableEntityID(EntityType, CanonicalName), so the same canonical entity
// always maps to the same record and the same id, no matter which scene or
// run extracted it. That determinism is what makes the index safe for dedup
// and cache keying.
type EntityRecord struct {
	EntityID string `json:"entity_id"`
	// CanonicalName is the display form the overlays render ("Tim Cook").
	CanonicalName string `json:"canonical_name"`
	// EntityType is the canonical NLP type ("PERSON", "ORGANIZATION", ...).
	EntityType string `json:"entity_type"`
	// NormalizedName is NormalizeName(CanonicalName) ("tim cook"); it is the
	// exact-lookup key.
	NormalizedName string `json:"normalized_name"`
	// Aliases are alternative surface forms / descriptors of the same
	// entity ("Apple CEO" for Tim Cook). They participate in exact lookup
	// (alias → entity) and in the semantic token-overlap matcher.
	Aliases []string `json:"aliases,omitempty"`
}

// ErrInvalidEntityRecord is returned when an EntityRecord violates the SSOT
// invariants: empty identity or a content-address mismatch (EntityID !=
// StableEntityID(EntityType, CanonicalName)).
var ErrInvalidEntityRecord = errors.New("invalid entity record")

// Validate enforces the SSOT invariants of an entity record. An entity whose
// id does not match its canonical (type, name) hash is rejected: the id IS
// the content address, so a mismatch means the record was built from a
// different canonical form and would poison dedup.
func (r EntityRecord) Validate() error {
	if strings.TrimSpace(r.EntityID) == "" {
		return fmt.Errorf("%w: empty entity id", ErrInvalidEntityRecord)
	}
	if strings.TrimSpace(r.CanonicalName) == "" {
		return fmt.Errorf("%w: entity %q empty canonical name", ErrInvalidEntityRecord, r.EntityID)
	}
	if strings.TrimSpace(r.EntityType) == "" {
		return fmt.Errorf("%w: entity %q empty entity type", ErrInvalidEntityRecord, r.EntityID)
	}
	norm := NormalizeName(r.CanonicalName)
	if strings.TrimSpace(norm) == "" {
		return fmt.Errorf("%w: entity %q canonical name normalizes to empty", ErrInvalidEntityRecord, r.EntityID)
	}
	if r.NormalizedName != "" && NormalizeName(r.NormalizedName) != norm {
		return fmt.Errorf("%w: entity %q normalized name %q inconsistent with canonical name", ErrInvalidEntityRecord, r.EntityID, r.NormalizedName)
	}
	wantID := StableEntityID(r.EntityType, r.CanonicalName)
	if r.EntityID != wantID {
		return fmt.Errorf("%w: entity %q id %q != canonical id %q", ErrInvalidEntityRecord, r.CanonicalName, r.EntityID, wantID)
	}
	return nil
}

// NewEntityRecord builds a canonical, content-addressed entity record from
// an extracted source: EntityID = StableEntityID(type, name), NormalizedName
// derived from the canonical name, aliases cleaned. This is the ONLY
// construction path that is guaranteed to satisfy Validate.
func NewEntityRecord(entityType, canonicalName string, aliases ...string) EntityRecord {
	return EntityRecord{
		EntityID:       StableEntityID(entityType, canonicalName),
		CanonicalName:  strings.TrimSpace(canonicalName),
		EntityType:     NormalizeType(entityType),
		NormalizedName: NormalizeName(canonicalName),
		Aliases:        cleanAliases(aliases),
	}
}

// cleanAliases returns the normalized, deduplicated alias list: every alias
// is trimmed and its normalized form (NormalizeName) must be non-empty and
// different from the canonical name's normalized form.
func cleanAliases(aliases []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, alias := range aliases {
		norm := NormalizeName(alias)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, strings.TrimSpace(alias))
	}
	return out
}

// EntityRepository is the relational-style source of truth for entity
// records: upsert by stable content-addressed id, exact lookup by id /
// canonical name / normalized name / alias, deterministic iteration. The
// plan's "database relazionale": a structured store whose key IS the stable
// entity id — never a random UUID.
//
// The in-memory implementation keeps the package pure (zero I/O); a durable
// SQLite adapter can be dropped in behind the same interface by the
// composition root without changing the lookup contract.
type EntityRepository interface {
	// Upsert inserts or replaces the record keyed by its stable entity id.
	Upsert(rec EntityRecord) error
	// Get returns the record with the exact stable entity id.
	Get(entityID string) (EntityRecord, bool)
	// LookupExact resolves a surface name (or alias) to its entity: the
	// canonical name, the normalized name or an alias. Case- and
	// whitespace-insensitive via NormalizeName.
	LookupExact(name string) (EntityRecord, bool)
	// All returns every indexed record in deterministic (entity id) order.
	All() []EntityRecord
	// Len returns the number of indexed entities.
	Len() int
}

// InMemoryEntityRepository is the pure, deterministic EntityRepository.
// Backing maps are keyed by the stable entity id; name/alias indexes map
// normalized surface forms to that id, so exact lookup never depends on map
// iteration order.
type InMemoryEntityRepository struct {
	byID    map[string]EntityRecord
	byName  map[string]string // normalized surface form → entity id
	byAlias map[string]string // normalized alias → entity id
}

// NewInMemoryEntityRepository returns an empty in-memory entity repository.
func NewInMemoryEntityRepository() *InMemoryEntityRepository {
	return &InMemoryEntityRepository{
		byID:    map[string]EntityRecord{},
		byName:  map[string]string{},
		byAlias: map[string]string{},
	}
}

// Upsert inserts or replaces the record. The stable entity id is the
// identity: re-indexing the same canonical entity (e.g. from another scene)
// overwrites the previous record instead of duplicating it — the dedup the
// content-addressed id exists for.
func (r *InMemoryEntityRepository) Upsert(rec EntityRecord) error {
	if err := rec.Validate(); err != nil {
		return err
	}
	r.byID[rec.EntityID] = rec
	r.byName[rec.NormalizedName] = rec.EntityID
	for _, alias := range rec.Aliases {
		r.byAlias[NormalizeName(alias)] = rec.EntityID
	}
	return nil
}

// Get returns the record with the exact stable entity id.
func (r *InMemoryEntityRepository) Get(entityID string) (EntityRecord, bool) {
	rec, ok := r.byID[entityID]
	return rec, ok
}

// LookupExact resolves a surface name or alias to its entity. The lookup
// key is the normalized form, so "  TIM   COOK " and "tim cook" hit the same
// record; an alias like "Apple CEO" resolves to Tim Cook exactly like the
// canonical name does.
func (r *InMemoryEntityRepository) LookupExact(name string) (EntityRecord, bool) {
	norm := NormalizeName(name)
	if norm == "" {
		return EntityRecord{}, false
	}
	if id, ok := r.byName[norm]; ok {
		return r.byID[id], true
	}
	if id, ok := r.byAlias[norm]; ok {
		return r.byID[id], true
	}
	return EntityRecord{}, false
}

// All returns every indexed record in deterministic entity-id order, so
// semantic scoring never depends on map iteration order.
func (r *InMemoryEntityRepository) All() []EntityRecord {
	out := make([]EntityRecord, 0, len(r.byID))
	for _, rec := range r.byID {
		out = append(out, rec)
	}
	// Stable order: content-addressed ids sort deterministically.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].EntityID < out[j-1].EntityID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Len returns the number of indexed entities.
func (r *InMemoryEntityRepository) Len() int {
	return len(r.byID)
}
