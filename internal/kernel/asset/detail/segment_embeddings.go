// Package asset — SliceEmbeddingRecord type only (Wave C slim).
//
// Wave C (Blocco 1 Asset SSOT, June 2026): SQL receivers
// DeleteSegmentEmbeddingsByScriptKey,
// GetSegmentEmbeddingsByScriptKey, UpsertSegmentEmbedding migrated
// to `internal/platform/sqlite/assets
// /segment_embedding_queries.go`. The slim domain file retains ONLY
// the SegmentEmbeddingRecord type — no SQL, no `database/sql`
// import — acceptance criterion: zero stdlib database/sql imports in this package.
//
// The type itself stays in domain because callers in application/
// refer to it by name and don't want a type duplication. The slim
// design is the same as migration 091 (`media.db.sqlite` is the
// canonical store; the type is the in-memory shape).
package detail

// SegmentEmbeddingRecord stores the semantic cache for a script
// segment. Rows are persisted in the `segment_embeddings` table
// (see `internal/platform/sqlite/assets
// /segment_embedding_queries.go` for the SQL projection); this
// struct is the in-process representation consumed by the script
// pipeline.
type SegmentEmbeddingRecord struct {
	ID                    int64
	ScriptKey             string
	SourceHash            string
	Topic                 string
	Language              string
	Template              string
	Duration              int
	SegmentIndex          int
	RawSubject            string
	CanonicalSubject      string
	RawKeywordsJSON       string
	CanonicalKeywordsJSON string
	RawEntitiesJSON       string
	CanonicalEntitiesJSON string
	SegmentJSON           string
	EmbeddingJSON         string
	BestSource            string
	BestPath              string
	BestLink              string
	BestScore             int
}
