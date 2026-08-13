// Package filesystem hosts the concrete workspace I/O adapters that
// implement the kernel's filesystem + network ports
// (internal/kernel/job/workspace.Fetcher and .FileSystem). It owns the
// os/net/http drivers — the kernel keeps only the semantic contract and
// the §5.4 path-containment logic.
//
// The single production composition point is NewManager, which wires an
// OS filesystem + an HTTP fetcher into the kernel's canonical
// WorkspaceManager implementation (workspace.NewManagerWithDeps). The
// composition root (internal/app) injects the returned manager into
// consumers; no business code constructs these adapters directly.
package filesystem
