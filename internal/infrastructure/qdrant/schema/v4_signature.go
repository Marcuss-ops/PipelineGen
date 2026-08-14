// Package schema — v4_signature.go: the signed identity of the
// media_assets_v4 projection generation (PR-HASH-SEMANTICS item 13, August 2026).
//
// Two collections are interchangeable for search ONLY when their signature is
// identical: the schema version (layout), the embedding contract hash (model
// id/revision/dimension/normalization/distance/prefixes — the vector space),
// and the semantic document version (the exact text composed and embedded).
// "Dimension = 768" alone is NOT enough to declare two collections compatible:
// the hash pins the full contract.
//
// godlike/06 SSOT: the signature is anchored to the committed
// kernel/embedding SSOT (CanonicalText.Hash, SemanticDocumentVersionV3,
// DimensionText). A v4 generation is named and verified ONLY through this type;
// no hand-authored collection-name literal may substitute for it.
package schema

import (
	"fmt"
	"strconv"
	"strings"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
)

// V4SchemaVersion is the canonical media_assets_v4 collection layout version.
const V4SchemaVersion = "v4"

// V4CollectionNamePrefix is the canonical physical-name prefix for the media
// asset projection. Every signed generation shares it; the suffix is the
// generation's signature.
const V4CollectionNamePrefix = "media_assets_"

// V4Signature is the signed identity of one immutable Qdrant collection
// generation.
type V4Signature struct {
	// SchemaVersion is the collection layout version ("v4").
	SchemaVersion string

	// EmbeddingContractHash is the 64-hex SHA-256 fingerprint of the
	// canonical text-embedding contract (kernel/embedding.CanonicalText.Hash).
	EmbeddingContractHash string

	// SemanticDocumentVersion pins the canonical search-text composition
	// (kernel/embedding.SemanticDocumentVersionV3).
	SemanticDocumentVersion string

	// TextDimension is the text-channel vector length (e.g. 768).
	TextDimension int
}

// CanonicalV4Signature returns the v4 signature anchored to the committed
// embedding SSOT. It is the single constructor used to name and verify the
// media_assets_v4 generation.
func CanonicalV4Signature() V4Signature {
	return V4Signature{
		SchemaVersion:           V4SchemaVersion,
		EmbeddingContractHash:   coreembedding.CanonicalText.Hash(),
		SemanticDocumentVersion: coreembedding.SemanticDocumentVersionV3,
		TextDimension:           coreembedding.DimensionText,
	}
}

// Validate fails closed on an unsigned or malformed signature. A collection
// generation may only be created and promoted when every component is present
// and the embedding contract hash is a real 64-hex SHA-256 digest.
func (s V4Signature) Validate() error {
	if strings.TrimSpace(s.SchemaVersion) == "" {
		return fmt.Errorf("v4 signature: schema version is required")
	}
	if !isSHA256Hex(s.EmbeddingContractHash) {
		return fmt.Errorf("v4 signature: embedding contract hash must be a 64-hex SHA-256, got %q", s.EmbeddingContractHash)
	}
	if strings.TrimSpace(s.SemanticDocumentVersion) == "" {
		return fmt.Errorf("v4 signature: semantic document version is required")
	}
	if s.TextDimension <= 0 {
		return fmt.Errorf("v4 signature: text dimension must be positive, got %d", s.TextDimension)
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
func (s V4Signature) PhysicalName() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	return strings.Join([]string{
		V4CollectionNamePrefix + s.SchemaVersion,
		s.EmbeddingContractHash,
		s.SemanticDocumentVersion,
		strconv.Itoa(s.TextDimension),
	}, "_"), nil
}

// Matches reports whether name encodes exactly this signature. It is the
// pre-alias verification: the active/aliased collection must carry the same
// schema version, contract hash, semantic document version and dimension as
// the canonical signature before it is promoted.
func (s V4Signature) Matches(name string) bool {
	parsed, ok := ParseV4Signature(name)
	if !ok {
		return false
	}
	return parsed == s
}

// ParseV4Signature reverses PhysicalName. The embedding contract hash is
// recovered verbatim (the full 64-hex digest is embedded, not a truncated
// prefix), so Matches performs an exact comparison.
func ParseV4Signature(name string) (V4Signature, bool) {
	if !strings.HasPrefix(name, V4CollectionNamePrefix) {
		return V4Signature{}, false
	}
	rest := strings.TrimPrefix(name, V4CollectionNamePrefix)
	parts := strings.Split(rest, "_")
	if len(parts) != 4 {
		return V4Signature{}, false
	}
	dim, err := strconv.Atoi(parts[3])
	if err != nil {
		return V4Signature{}, false
	}
	return V4Signature{
		SchemaVersion:           parts[0],
		EmbeddingContractHash:   parts[1],
		SemanticDocumentVersion: parts[2],
		TextDimension:           dim,
	}, true
}

// isSHA256Hex reports whether s is a 64-character lowercase hex digest. It is
// kept local to this file to avoid a dependency on the media-registry package
// (which is still converging on its taxonomy constants).
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
