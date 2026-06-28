// filter_types.go — marker file for the Qdrant filter surface.
//
// PR3 mechanical split (June 2026): the user-stated file spec
// referenced "Filter/Condition/Match" as the canonical filter-shape
// types. The Qdrant package has no dedicated Filter / Condition /
// Match types in Go — every filter lives inline on the request shape
// as a `map[string]interface{}`:
//
//   - SearchRequest.Filter   (search_types.go)
//   - HybridSearchRequest.Filter   (search_types.go)
//   - ScrollPoints(...).filter   (client_scroll.go)
//
// The reason for the inline shape is twofold:
//
//  1. The compiled filter compiler (Qdrant REST's filter DSL) is
//     already a tree of maps — imposing a Go type on it would
//     either lock us to a subset of the DSL (and force an escape
//     hatch for new operators) or carry the entire DSL as an
//     allocation-heavy reflection-based Go tree. Neither is worth
//     shipping.
//  2. Compilation happens server-side at Qdrant. The Go client is
//     a wire shape; the DSL compiler lives in Qdrant itself.
//
// Future filter primitives that warrant Go types (a typed Filter
// builder, a strongly-typed Condition enum) belong in this file when
// they're introduced. Until then this file is a godoc marker
// pointing at the call sites where the inline filter maps land —
// same port as clips_statistics.go's marker in PR1 and
// client_snapshots.go's marker in PR2.
//
// Related decision trail:
//
//   - QDRANT-003 (June 2026): verified that keeping filters inline
//     does NOT impose measurable marshalling cost on /points/query.
//   - The convenience fields on SearchRequest / HybridSearchRequest
//     (Source / Category / MediaType / Language with `json:"-"`)
//     are assembled into the inline Filter map by SearchAdapter
//     before each request — they are convenience callers, not a
//     replacement for the inline map.
package qdrant
