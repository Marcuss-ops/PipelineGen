// Package semantic scores already-segmented narration sentences from
// caller-supplied multilingual embeddings. V1 combines weighted sparse
// TextRank centrality on a symmetrized top-k cosine graph with local novelty,
// then selects temporally separated local maxima when canonical word timings
// are available.
//
// Model inference and sentence segmentation belong to callers. Phrase text
// remains grounded against canonical narration, and timestamps must come from
// the canonical word-level speech timing artifact through capabilities/audio;
// this package never estimates them. Scores can enrich ranking but must not
// bypass the existing run-level overlay budget or certified renderer/motion
// catalogs. This package is not wired into production.
//
// The repository's multilingual E5 embedding contract serves media retrieval
// and is not silently reused as this phrase-scoring contract.
package semantic
