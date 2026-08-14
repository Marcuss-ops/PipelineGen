package schema

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// CollectionNamePrefix is the canonical physical-name prefix for the media
// asset projection. Every signed generation shares it; the suffix is the
// generation's unique signature.
const CollectionNamePrefix = "media_assets_"

// CollectionSignature is the signed identity of one immutable Qdrant
// collection generation (PR-HASH-SEMANTICS item 13/14, August 2026).
//
// Two collections are interchangeable for search ONLY when their signature
// is identical: the schema version (layout), the embedding contract hash
// (model id/revision/dimension/normalization/distance/prefixes — the vector
// space), and the semantic document version (the exact text that was
// composed and embedded). "Dimension = 768" alone is NOT enough to declare
// two collections compatible — the hash pins the full contract.
type CollectionSignature struct {
	// SchemaVersion is the collection layout version (e.g. "v4").
	SchemaVersion string
	// EmbeddingContractHash is the 64-hex SHA-256 fingerprint of the
	// canonical text-embedding contract (see kernel/embedding.Contract.Hash).
	EmbeddingContractHash string
	// SemanticDocumentVersion pins the canonical search-text composition
	// (see kernel/embedding.SemanticDocumentVersion).
	SemanticDocumentVersion string
	// TextDimension is the text-channel vector length (e.g. 768).
	TextDimension int
}

// Validate fails closed on an unsigned or malformed signature. A collection
// generation may only be created and promoted when every signature component
// is present and the embedding contract hash is a real SHA-256 digest.
func (s CollectionSignature) Validate() error {
	if strings.TrimSpace(s.SchemaVersion) == "" {
		return fmt.Errorf("collection signature: schema version is required")
	}
	if !mediaregistry.IsSHA256Hex(s.EmbeddingContractHash) {
		return fmt.Errorf("collection signature: embedding contract hash must be a 64-hex SHA-256, got %q", s.EmbeddingContractHash)
	}
	if strings.TrimSpace(s.SemanticDocumentVersion) == "" {
		return fmt.Errorf("collection signature: semantic document version is required")
	}
	if s.TextDimension <= 0 {
		return fmt.Errorf("collection signature: text dimension must be positive, got %d", s.TextDimension)
	}
	return nil
}

// PhysicalName derives the deterministic, signed physical collection name:
//
//	media_assets_<schemaVersion>_<embeddingContractHash>_<semanticDocVersion>_<dim>
//
// e.g. media_assets_v4_<64-hex>_v3_768. The name is derived, never
// hand-authored, so two generations that agree on the signature always
// collapse to the same physical name (idempotent blue-green prepare).
func (s CollectionSignature) PhysicalName() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	return strings.Join([]string{
		CollectionNamePrefix + s.SchemaVersion,
		s.EmbeddingContractHash,
		s.SemanticDocumentVersion,
		strconv.Itoa(s.TextDimension),
	}, "_"), nil
}

// Matches reports whether name encodes exactly this signature. It is the
// pre-alias verification: the active/aliased collection must carry the same
// schema version, contract hash, semantic document version and dimension as
// the canonical signature before it is promoted.
func (s CollectionSignature) Matches(name string) bool {
	parsed, ok := ParseCollectionSignature(name)
	if !ok {
		return false
	}
	return parsed == s
}

// ParseCollectionSignature reverses PhysicalName. The embedding contract
// hash is recovered verbatim (the full 64-hex digest is embedded, not a
// truncated prefix), so Matches performs an exact comparison.
func ParseCollectionSignature(name string) (CollectionSignature, bool) {
	if !strings.HasPrefix(name, CollectionNamePrefix) {
		return CollectionSignature{}, false
	}
	rest := strings.TrimPrefix(name, CollectionNamePrefix)
	parts := strings.Split(rest, "_")
	if len(parts) != 4 {
		return CollectionSignature{}, false
	}
	dim, err := strconv.Atoi(parts[3])
	if err != nil {
		return CollectionSignature{}, false
	}
	return CollectionSignature{
		SchemaVersion:           parts[0],
		EmbeddingContractHash:   parts[1],
		SemanticDocumentVersion: parts[2],
		TextDimension:           dim,
	}, true
}
