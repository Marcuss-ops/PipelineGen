// Package embedding is the canonical Single Source Of Truth (SSOT) for the
// text embedding contract shared by the indexing path (clipindexer), the
// Qdrant schema, the Python embedding sidecar, and the query embedder.
//
// The contract encodes more than "dimension=768": two models can emit the
// same vector length while producing incompatible vector spaces. Every
// component that produces or consumes document/query vectors MUST anchor to
// these exact values, and the boot-time handshake (Verify) fails closed with
// QDRANT_EMBEDDING_CONTRACT_MISMATCH when any of them disagree.
//
// godlike/06 SSOT: this is the single owner of the embedding identity facts.
// Do NOT add embedding model constants in other packages — reference
// CanonicalText (or the individual constants below) instead.
package embedding

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Canonical string constants for the multilingual E5 text model.
//
// These are the values the Python sidecar (scripts/services/embedding_server)
// actually loads and reports. The document vectors in production were
// produced by this model (audit: "canonical temporaneo = multilingual-e5-base"),
// so these — not the legacy nomic-embed-text literals scattered across the
// indexing/search layers — are the truth.
const (
	// ModelIDMultilingualE5 is the canonical text model id.
	ModelIDMultilingualE5 = "intfloat/multilingual-e5-base"
	// ModelRevisionMultilingualE5 is the pinned model revision reported by
	// the sidecar (scripts/services/embedding_server/__init__.py:
	// TEXT_MODEL_VERSION).
	ModelRevisionMultilingualE5 = "2026-06-26-v1"
	// DimensionText is the canonical text vector dimensionality.
	DimensionText = 768
	// NormalizationL2 marks L2-normalized vectors.
	NormalizationL2 = "l2"
	// DistanceCosine is the Qdrant distance metric for the text channels.
	DistanceCosine = "Cosine"
	// QueryPrefixE5 is prepended to queries (asymmetric E5 retrieval).
	QueryPrefixE5 = "query: "
	// DocumentPrefixE5 is prepended to documents at index time.
	DocumentPrefixE5 = "passage: "
	// ContractVersionV1 identifies the contract schema version.
	ContractVersionV1 = "v1"
	// SemanticDocumentVersionV3 tracks the canonical search-text version
	// (mirrors schema.CurrentSearchTextVersion).
	SemanticDocumentVersionV3 = "v3"
)

// Contract is the runtime/indexing identity of the text embedding channel.
// It is the canonical value every component must agree on.
type Contract struct {
	// ContractVersion is the schema version of the contract itself.
	ContractVersion string
	// ModelID is the canonical model id (e.g. "intfloat/multilingual-e5-base").
	ModelID string
	// ModelRevision is the pinned model release/revision.
	ModelRevision string
	// Dimension is the output vector length.
	Dimension int
	// Normalization describes the vector normalization (e.g. "l2").
	Normalization string
	// Distance is the Qdrant distance metric ("Cosine", "Euclid", "Dot").
	Distance string
	// QueryPrefix is prepended to query text.
	QueryPrefix string
	// DocumentPrefix is prepended to document text at index time.
	DocumentPrefix string
	// SemanticDocumentVersion pins the canonical search-text composition.
	SemanticDocumentVersion string
}

// CanonicalText is the single canonical text embedding contract. It is the
// value the boot handshake compares against the sidecar runtime, the Qdrant
// active-collection metadata, and the query embedder.
var CanonicalText = Contract{
	ContractVersion:         ContractVersionV1,
	ModelID:                 ModelIDMultilingualE5,
	ModelRevision:           ModelRevisionMultilingualE5,
	Dimension:               DimensionText,
	Normalization:           NormalizationL2,
	Distance:                DistanceCosine,
	QueryPrefix:             QueryPrefixE5,
	DocumentPrefix:          DocumentPrefixE5,
	SemanticDocumentVersion: SemanticDocumentVersionV3,
}

// Hash returns the deterministic embedding_contract_hash (SHA-256 hex) of the
// contract. It is the fingerprint used to detect contract drift and to name
// future Qdrant collection generations.
func (c Contract) Hash() string {
	canonical := fmt.Sprintf("%s|%s|%s|%d|%s|%s|%s|%s|%s",
		c.ContractVersion,
		c.ModelID,
		c.ModelRevision,
		c.Dimension,
		c.Normalization,
		c.Distance,
		c.QueryPrefix,
		c.DocumentPrefix,
		c.SemanticDocumentVersion,
	)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// Equal reports full equality of two contracts.
func (c Contract) Equal(other Contract) bool {
	return c == other
}

// MatchesPartial reports whether c agrees with other on the fields that are
// actually populated in other (non-zero). Qdrant collection metadata only
// exposes dimension and distance, and the query-embedder config only exposes
// a model id — so those legs of the handshake compare only the fields they
// can observe.
func (c Contract) MatchesPartial(other Contract) bool {
	if other.ContractVersion != "" && c.ContractVersion != other.ContractVersion {
		return false
	}
	if other.ModelID != "" && c.ModelID != other.ModelID {
		return false
	}
	if other.ModelRevision != "" && c.ModelRevision != other.ModelRevision {
		return false
	}
	if other.Dimension != 0 && c.Dimension != other.Dimension {
		return false
	}
	if other.Normalization != "" && c.Normalization != other.Normalization {
		return false
	}
	if other.Distance != "" && c.Distance != other.Distance {
		return false
	}
	if other.QueryPrefix != "" && c.QueryPrefix != other.QueryPrefix {
		return false
	}
	if other.DocumentPrefix != "" && c.DocumentPrefix != other.DocumentPrefix {
		return false
	}
	if other.SemanticDocumentVersion != "" && c.SemanticDocumentVersion != other.SemanticDocumentVersion {
		return false
	}
	return true
}

// String renders the contract as a compact single-line representation for
// log lines and error messages.
func (c Contract) String() string {
	return fmt.Sprintf("contract=%s model=%s rev=%s dim=%d norm=%s dist=%s query=%q doc=%q semdoc=%s",
		c.ContractVersion, c.ModelID, c.ModelRevision, c.Dimension,
		c.Normalization, c.Distance, c.QueryPrefix, c.DocumentPrefix,
		c.SemanticDocumentVersion)
}
