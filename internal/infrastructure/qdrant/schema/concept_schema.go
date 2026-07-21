// Package schema — concept_schema.go is the canonical Qdrant
// schema definition for the mediamemory `pipelinegen_media_concepts`
// collection.
//
// godlike/06 SSOT (one canonical owner per fact): this file is
// the SINGLE owner of the concept-index vector dimensions,
// channel names, and payload-index shape. The mediamemory
// capability NEVER redefines these values; it imports
// ConceptIndexSchema() and uses IndexSchema's helpers.
//
// godlike/07 NO-FAKE-AVAILABILITY: the schema lists every
// channel the mediamemory capability reads/writes. The
// Indexer concrete (qdrant_indexer.go) is the consumer; the
// validator at boot determines "this collection is ready".
package schema

// ConceptCollectionName is the canonical physical collection
// name for media_concepts (godlike/06 SSOT).
const ConceptCollectionName = "pipelinegen_media_concepts"

// ConceptEmbeddingVersion is the canonical embedding schema
// version for concept vectors (godlike/06 SSOT). It lives on
// IndexSchema.Version AND on each point's `embedding_version`
// payload field so readers can branch on the model release.
const ConceptEmbeddingVersion = "v1"

// ConceptIndexSchema returns the canonical IndexSchema for
// media_concepts. The schema mirrors the asset-side canonical
// e5-base 768d + bm25_text channel vocabulary used by
// search_backend_semantic.go (semanticDenseVectorName="text"
// / semanticSparseVectorName="bm25_text") so a future
// asset-side reencoder can also score concepts without an
// adapter translation.
//
// Wire-channel vocabulary (godlike/06 SSOT; must match
// search.CanonicalChannelNames byte-for-byte):
//
//	dense  "text"        : 768d Cosine, normalization on,
//	                        e5-base multilingual-e5-base
//	sparse "bm25_text"   : server-side BM25 (DefaultSparseModel)
//
// Payload index shape:
//
//	concept_id         keyword  : canonical concept ID
//	language           keyword  : ISO 639-1 (it, en, ...)
//	phrase_fingerprint keyword  : canonical Normalizer SHA256
//	concept_type       keyword  : closed set (phrase/...)
//	normalized_text    text     : inverted exact-token search
//	canonical_text     text     : free-text match
//	embedding_version  keyword  : ConceptEmbeddingVersion SSOT
func ConceptIndexSchema() *IndexSchema {
	return &IndexSchema{
		Version:      ConceptEmbeddingVersion,
		PhysicalName: ConceptCollectionName,
		RuntimeAlias: ConceptCollectionName,
		DenseVectors: []EmbeddingSpec{
			{
				Channel:       "text",
				Model:         "multilingual-e5-base",
				ModelVersion:  "2026-06-16-v1",
				Dimensions:    768,
				Distance:      "Cosine",
				Normalized:    true,
				QueryPrefix:   "query: ",
				IndexPrefix:   "passage: ",
				PreprocessVer: ConceptEmbeddingVersion + "-concept",
			},
		},
		SparseVectors: []SparseSpec{
			{Channel: "bm25_text", Modifier: "idf", Model: DefaultSparseModel},
		},
		PayloadIndexes: []PayloadIndexSpec{
			{FieldName: "concept_id", FieldType: "keyword"},
			{FieldName: "language", FieldType: "keyword"},
			{FieldName: "phrase_fingerprint", FieldType: "keyword"},
			{FieldName: "concept_type", FieldType: "keyword"},
			{FieldName: "normalized_text", FieldType: "text"},
			{FieldName: "canonical_text", FieldType: "text"},
			{FieldName: "embedding_version", FieldType: "keyword"},
		},
	}
}
