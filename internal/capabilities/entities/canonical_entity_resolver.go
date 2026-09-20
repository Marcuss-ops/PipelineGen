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
	// English possessives are grammatical attachment, not part of the
	// person's identity: "Donald Trump's" must resolve to the same PERSON as
	// "Donald Trump". Keep this normalization at the canonical owner so every
	// caller (NLP, image search, catalog and indexing) agrees on the join key.
	if strings.EqualFold(strings.TrimSpace(entityType), "PERSON") {
		name = strings.TrimSpace(name)
		for _, suffix := range []string{"'s", "’s"} {
			if len(name) > len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix) {
				name = strings.TrimSpace(name[:len(name)-len(suffix)])
				break
			}
		}
	}
	slug := SafeEntityID(NormalizeName(name))
	if slug == "" {
		return ""
	}
	return strings.ToLower(NormalizeType(entityType)) + ":" + slug
}

// CanonicalEntitySlug returns the slug part of a canonical entity id — the
// identity without its type namespace:
//
//	CanonicalEntitySlug("person:michael-jordan") == "michael-jordan"
//
// It is the canonical-owner INVERSE of CanonicalEntityID, so a consumer that
// needs a filesystem/Drive-safe segment for an entity (the per-image folder of
// the canonical image library) never re-implements the "type:slug" split. A
// value carrying no ":" is returned trimmed and unchanged, so a caller may pass
// either the canonical id or an already-derived slug; a blank input yields "".
//
// The result is a slug, not a path segment: callers still pass it through the
// path builder's SafeFolderName before using it as a folder name. That keeps
// this function pure (no infrastructure dependency) and leaves folder-name
// sanitization with its existing single owner.
func CanonicalEntitySlug(canonicalID string) string {
	trimmed := strings.TrimSpace(canonicalID)
	if trimmed == "" {
		return ""
	}
	if idx := strings.LastIndex(trimmed, ":"); idx >= 0 {
		return strings.TrimSpace(trimmed[idx+1:])
	}
	return trimmed
}

// IsEntityType reports whether a semantic type is a canonical entity type:
// one that can be linked to a canonical entity record (and its assets).
//
// Value types (DATE/MONEY/NUMBER/PERCENTAGE) and editorial artifacts
// (IMPORTANT_PHRASE/QUOTE/CLAIM/STATISTIC/RANKING/TITLE/EVENT) are NOT entity
// types: they describe a surface, not a linkable entity, so they never carry
// a canonical_entity_id.

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

// ResolveAll applies Resolve to every item, preserving input order.

// DefaultCanonicalEntityResolver is the process-wide resolver. Every call
// site resolves through this single instance so canonical ids stay uniform
// across the pipeline.
var DefaultCanonicalEntityResolver = CanonicalEntityResolver{}
