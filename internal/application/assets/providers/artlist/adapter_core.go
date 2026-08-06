// Package artlist owns the Artlist application ports, orchestration policies,
// and compatibility surface. Concrete search responsibilities are split into
// focused files:
//
//	adapter.go          provider contract adapter
//	searcher_sqlite.go  compatibility local-search bridge
//	searcher_cache.go   cached search decorator
//	searcher_mapping.go shared candidate mapping helpers
//
// Production SQLite construction lives in
// infrastructure/database/sqlite/artlist_searcher.go.
package artlist
