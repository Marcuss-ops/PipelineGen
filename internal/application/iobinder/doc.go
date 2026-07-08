// Package iobinder is a forward-prevention regression-guard for the
// PR-REFACTOR-P0-IO-BINDER spec: the application layer MUST NOT
// directly import `database/sql` or call `os.Open` / `sql.Open`.
// All such I/O bindings must route through typed ports (Pattern 0)
// implemented in internal/infrastructure/.
//
// The exception list in iobinder_test.go is the canonical baseline of
// the 52 known violations (16 os.Open-family + 0 sql.Open + 36
// database/sql) on origin/main at audit time (2026-08-10). The 16
// os.Open-family hits break down as 10 actual `os.Open(...)` call
// sites + 3 `os.OpenFile(...)` call sites + 3 comment references
// (the spec's `rg 'os\.Open'` is a substring match, so it catches
// all three). The sub-PRs below will
// migrate these violations to typed ports and shrink the list:
//
//   - PR-REFACTOR-P0-IO-BINDER-SQLITE: relocate the database/sql
//     adapter files (e.g. assets/artifacts/repository.go,
//     jobs/finalizer/*.go, jobs/outbox/*.go) to
//     internal/infrastructure/database/sqlite/, exposing only
//     typed Repository / Tx / Rows ports to the application layer.
//
//   - PR-REFACTOR-P0-IO-BINDER-FS: replace inline os.Open calls in
//     business logic (e.g. document/service.go,
//     assets/ingest/image.go, jobs/assets/service.go) with a
//     lightweight FileReader / FileHasher port injected via ctor.
//
//   - PR-REFACTOR-P0-IO-BINDER-FINALIZERS: address the sql.Tx leak
//     bleeding into the voiceover finalizer + parent-aggregator
//     transactional outbox (voiceover/ports.go,
//     voiceover/finalizer.go, etc.) via a TxContext typed port.
//
// As the list shrinks, the invariant tightens; when the list reaches
// zero, the unconditional "0 hits" assertion activates.
package iobinder
