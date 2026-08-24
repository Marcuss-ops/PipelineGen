// Package images — search.go: image search & retrieval coordination.
//
// Former monolithic file (688 LOC) decomposed July 2026 per AGENTS.md Pattern 5:
//
//   - storage_ops.go   — typed error sentinels + SearchAndDownload +
//     searchAndDownloadInner (DB-first search with web fallback)
//   - search_queries.go — SearchWebImage + B5 fan-out primitives +
//     web search helpers (DDG, SearXNG, Wikidata, Wikipedia)
//
// This file is intentionally empty — it serves as a landing page for the split topology.
package images
