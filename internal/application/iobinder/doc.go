// Package iobinder is a forward-prevention regression-guard for the
// PR-REFACTOR-P0-IO-BINDER spec: the application layer MUST NOT
// directly import `database/sql` or call `os.Open`, `os.OpenFile`, or
// `sql.Open`. All such I/O bindings must route through typed ports
// (Pattern 0) implemented in internal/infrastructure/.
//
// The canonical allowlist lives in allowlist.txt (same package). The
// key is "<path>:<symbol>" — not a line number — so the gate stays
// stable when unrelated code moves above the call site. Every entry
// must carry owner, deadline, and rationale. The CI gate fails on both
// new direct-IO bindings and stale allowlist entries (entries whose
// symbol no longer appears in the file).
//
// The scanner is AST-based (go/ast) and ignores comment references, so
// only real imports and selector calls count as hits.
//
// Sub-PRs that will shrink the allowlist:
//   - PR-REFACTOR-P0-IO-BINDER-SQLITE: relocate database/sql adapter
//     files to internal/infrastructure/database/sqlite/ and expose
//     typed Repository / Tx / Rows ports.
//   - PR-REFACTOR-P0-IO-BINDER-FS: replace inline os.Open / os.OpenFile
//     calls with a lightweight FileReader / FileWriter port.
//   - PR-REFACTOR-P0-IO-BINDER-FINALIZERS: address sql.Tx leaks via a
//     typed TxContext port.
package iobinder
