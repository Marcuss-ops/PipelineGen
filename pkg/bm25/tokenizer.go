// Package bm25 retains only the legacy sparse-vector wire shape used by
// diagnostic/bulk callers. Live indexing and querying use Qdrant server-side
// BM25 inference ("qdrant/bm25") via SparseText; client-side tokenization was
// removed after reaching zero callers.
package bm25

// SparseVector is a Qdrant-compatible pre-computed sparse vector.
// New live-search code should prefer raw SparseText and server-side BM25.
type SparseVector struct {
	Indices []uint32  `json:"indices"`
	Values  []float32 `json:"values"`
}
