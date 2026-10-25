// Package profile defines the canonical SearchProfile typed-envelope that maps
// source-aware search weight presets. The resolver returns a SearchProfile
// containing per-channel weights (text, transcript, visual, bm25, metadata)
// that the search backend uses to configure hybrid retrieval, multi-vector
// fusion, and reranking behavior.
//
// Design rationale (godlike/06 SSOT):
//   - YouTube clips rely heavily on transcript + text dense signals
//   - Stock footage relies heavily on visual signals (no useful transcript)
//   - Default fallback provides a safe balanced preset
//
// This package is pure domain — no infrastructure imports, no Qdrant client,
// no database access. It is consumed by the application-layer search backend
// and the composition root.
package search
