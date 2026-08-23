package entities

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
)

// NormalizeName returns the canonical normalized form of an entity name:
// trimmed, lowercased, and internal whitespace collapsed to single spaces.
// "  Tim   Cook  " → "tim cook". Two surface forms that normalize to the same
// canonical name are the SAME entity for dedup and cache purposes.
func NormalizeName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
}

// NormalizeType returns the canonical entity type: trimmed and upper-cased.
// "person" → "PERSON". The canonical type is part of the entity identity, so
// the same surface name with a different type is a DIFFERENT entity.
func NormalizeType(entityType string) string {
	return strings.ToUpper(strings.TrimSpace(entityType))
}

// CanonicalKey builds the canonical identity string of an entity:
// "TYPE:name" (e.g. "PERSON:tim cook"). This is the dedup/cache key: it is
// content-addressed, so the same entity always yields the same key and the
// same stable entity ID — no random UUID, no central counter.
func CanonicalKey(entityType, name string) string {
	return NormalizeType(entityType) + ":" + NormalizeName(name)
}

// StableEntityIDLen is the number of hex characters kept from the sha256
// digest of the canonical key. 16 hex chars = 64 bits, collision-safe for
// the entity scale of this pipeline while keeping IDs readable.
const StableEntityIDLen = 16

// StableEntityID derives the content-addressed, stable entity ID from the
// canonical (type, name) key: "ent_" + hex(sha256("TYPE:name"))[:16].
//
//	StableEntityID("PERSON", "Tim Cook") == StableEntityID("PERSON", "  TIM   COOK ")
//
// always, and two different (type, name) canonical keys never collide within
// the 64-bit space. This is the identity used for entity dedup, asset cache
// reuse and statistics: it is deterministic across scenes, runs and repos.
func StableEntityID(entityType, name string) string {
	sum := digest.SHA256Bytes([]byte(CanonicalKey(entityType, name)))
	return "ent_" + sum[:StableEntityIDLen]
}
