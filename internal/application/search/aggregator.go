package search

import "context"

// Aggregator fans out a Query across registered SearchBackends and
// merges the results. Wave 21 (Fase 4) ships it incrementally across
// three commits — all three remain GREEN on origin/main:
//
//   PR 8 (this commit) ships the NOOP stub. The struct, constructor,
//     and Search method signature are stable; behaviour is "return
//     empty Result with no Error and no Partial flag".
//   PR 9 (BACKFILL): replace Search with the real fanout —
//     pkg/concurrent errgroup, per-backend timeout (5s provider /
//     2s local / 8s semantic), Partial flag if any backend errors,
//     ProviderErrors populated per backend name, dedup.go + rank.go
//     wrap the merge step. Cursor stability: skip items already in
//     q.Cursor's fingerprint.
//   PR 10 (CUTOVER): switch handler dependencies and delete legacy
//     clipssearch + assets/search cross-provider packages.
//
// Wave 19 invariants that this file respects:
//
//   - Aggregator does NOT import internal/infrastructure/* (no SQLite,
//     no pgx, no Qdrant client — ports only). Vector store + SQLite
//     adapters live under internal/app.
//   - Aggregator does NOT import another capability. The semantic
//     backend's *mediasearch.Service is injected via composition root
//     (internal/app/search_backends.go) implementing SearchBackend —
//     mediasearch is never imported here. Capability-crossing wiring
//     lives exclusively in internal/app.
type Aggregator struct {
	backends *BackendRegistry
	log      Logger
}

// NewAggregator constructs the Aggregator. backends may be nil (a
// fresh in-memory registry is substituted inside the constructor);
// log may be nil (a noopLogger is substituted, matching the
// pattern used by mediasearch.NewService).
func NewAggregator(backends *BackendRegistry, log Logger) *Aggregator {
	if log == nil {
		log = noopLogger{}
	}
	if backends == nil {
		backends = NewBackendRegistry()
	}
	return &Aggregator{backends: backends, log: log}
}

// Backends returns the registry the aggregator was constructed
// with. Useful for diagnostics (route_health.go) and tests.
func (a *Aggregator) Backends() *BackendRegistry {
	return a.backends
}

// Search is the NOOP stub (PR 8). It accepts any Query, never errors,
// and returns an empty, non-partial Result. The real implementation
// lands in PR 9 (BACKFILL) and is committed in a separate commit
// that keeps the type signature stable.
//
// The stub faithfully initialises ProviderErrors as a non-nil empty
// map so handler code that iterates over Result.ProviderErrors
// without a nil-guard already compiles and runs (returns zero hits).
// This is what "GREEN at every commit" looks like for an additive
// capability.
func (a *Aggregator) Search(ctx context.Context, q Query) (*Result, error) {
	a.log.Debug("search.Aggregator.Search: NOOP — PR 8 stub",
		"text_len", len(q.Text),
		"sources", len(q.Sources),
		"media_types", len(q.MediaTypes),
		"limit", q.Limit,
		"has_cursor", q.Cursor != "",
	)
	return &Result{
		Items:          []Candidate{},
		NextCursor:     "",
		ProviderErrors: map[string]string{},
		Partial:        false,
	}, nil
}
