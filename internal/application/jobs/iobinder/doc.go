// Package iobinder provides the forward-prevention regression-guard for
// synchronous file I/O in the jobs hot paths.
//
// # Spec
//
//	Remove synchronous os.ReadFile / os.Open / os.OpenFile in hot paths;
//	lift to eager-load at boot via an injected I/O binder. Verification
//	is AST-based (go/ast) so only real selector calls count; comments
//	are ignored.
//
// The canonical allowlist lives in allowlist.txt (same package). The
// key is "<path>:<symbol>" — not a line number. Every entry must carry
// owner, deadline, and rationale. The CI gate fails on both new
// sync-IO bindings and stale allowlist entries.
//
// # Sub-PR that will shrink the exception list to zero
//
//	PR-IOBINDER-P2-DOWNLOAD: migrate Service.Download's per-asset os.Open
//	to a typed port (internal/infrastructure/localasset); remove the
//	corresponding entry from allowlist.txt and ship a benchmark proving
//	the improvement.
package iobinder
