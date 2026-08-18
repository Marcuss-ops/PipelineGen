// Package entities — canonical_entity_resolver.go owns the SINGLE derivation
// of a stable, human-readable canonical entity id for an entity-typed
// SemanticItem.
//
// Pipeline position:
//
//	Semantic Index → Canonical Entity Resolver → Visual Intent Resolver → ...
//
// The resolver is pure and deterministic: it never consults a database or an
// external model. Given the same (type, name) it always yields the same
// canonical_entity_id, so the id is safe as a cache/dedup key and as the join
// key of the future Entity Media Index (canonical_entity_id → available
// assets).
//
// The id format is {lowercase-type}:{safe-slug}, e.g.
//
//	("PERSON", "Floyd Mayweather Jr.") → "person:floyd-mayweather-jr"
//
// This is the human-readable identity. It is distinct from, and not a
// replacement for, the two other entity-id spellings this package already
// owns:
//
//	CanonicalKey   → "PERSON:tim cook"  (uppercase type, space-normalized name;
//	                                      the hash input of StableEntityID)
//	StableEntityID → "ent_"+16-hex      (content-addressed machine id; the
//	                                      EntityRecord.EntityID)
//	CanonicalEntityID → "person:tim-cook" (readable, stable link id; the
//	                                      SemanticItem.CanonicalEntityID)
package entities

import "strings"

// CanonicalEntityID derives the stable, human-readable canonical entity id:
// the lowercase normalized type joined by ":" to SafeEntityID(name).
//
//	CanonicalEntityID("PERSON", "Floyd Mayweather Jr.") == "person:floyd-mayweather-jr"
//
// The derivation is case- and whitespace-insensitive: the type is normalized
// (trimmed + uppercased) then lowercased, and the name is first folded through
// NormalizeName (lowercased, internal whitespace collapsed to single spaces)
// and then through SafeEntityID (alphanumerics kept, every other rune becomes
// a dash, leading/trailing dashes trimmed). Two spellings that normalize
// identically always produce the same id. An empty (or non-alphanumeric) name
// yields "" — an id is never minted for nothing.
func CanonicalEntityID(entityType, name string) string {
	slug := SafeEntityID(NormalizeName(name))
	if slug == "" {
		return ""
	}
	return strings.ToLower(NormalizeType(entityType)) + ":" + slug
}

// IsEntityType reports whether a semantic type is a canonical entity type:
// one that can be linked to a canonical entity record (and its assets).
//
// Value types (DATE/MONEY/NUMBER/PERCENTAGE) and editorial artifacts
// (IMPORTANT_PHRASE/QUOTE/CLAIM/STATISTIC/RANKING/TITLE/EVENT) are NOT entity
// types: they describe a surface, not a linkable entity, so they never carry
// a canonical_entity_id.
func IsEntityType(t SemanticType) bool {
	switch t {
	case SemanticPerson, SemanticOrganization, SemanticLocation, SemanticImageEntity:
		return true
	default:
		return false
	}
}

// CanonicalEntityResolver resolves an indexed SemanticItem to its stable
// canonical entity identity. It is the single owner of the
// SemanticItem.CanonicalEntityID field and is stateless and safe for
// concurrent use.
type CanonicalEntityResolver struct{}

// Resolve returns a copy of the item with its normalized_text and
// canonical_entity_id filled in:
//
//   - NormalizedText is derived from Text via NormalizeName only when it is
//     empty — an extractor-provided normalized form is never overwritten;
//   - CanonicalEntityID is set for entity types (see IsEntityType) and
//     cleared otherwise, so the invariant "only entity types carry a
//     canonical entity id" is enforced here rather than at every consumer.
//
// The input item is never mutated.
func (CanonicalEntityResolver) Resolve(item SemanticItem) SemanticItem {
	if strings.TrimSpace(item.NormalizedText) == "" {
		item.NormalizedText = NormalizeName(item.Text)
	}
	if IsEntityType(item.Type) {
		item.CanonicalEntityID = CanonicalEntityID(string(item.Type), item.Text)
	} else {
		item.CanonicalEntityID = ""
	}
	return item
}

// ResolveAll applies Resolve to every item, preserving input order.
func (r CanonicalEntityResolver) ResolveAll(items []SemanticItem) []SemanticItem {
	out := make([]SemanticItem, len(items))
	for i, item := range items {
		out[i] = r.Resolve(item)
	}
	return out
}

// DefaultCanonicalEntityResolver is the process-wide resolver. Every call
// site resolves through this single instance so canonical ids stay uniform
// across the pipeline.
var DefaultCanonicalEntityResolver = CanonicalEntityResolver{}
