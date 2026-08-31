// Package metadataexport — filesystem atomic-write primitives for the
// Split metadata-export handler.
//
// Step 2 of the post-architettura 2026 plan (June 2026): the legacy
// internal/capabilities/assets/metadataexport::atomicWrite
// helper inlined the POSIX-atomic .tmp + rename pattern. After split,
// the atomic primitive lives here (private helper) and is wrapped by
// the three format-specific writers in the sibling files of this
// package. All four files implement the metadataexport.ExportWriter
// port declared in
// internal/capabilities/assets/metadataexport/ports.go.
//
// Atomicity guarantee: write to a sibling .tmp file (same directory
// as the final destination) then os.Rename. On linux/macos the rename
// inside a single filesystem is a single inode swap (atomic). Any
// crash mid-rename leaves either the old file or the new file
// observable — never a partial.
//
// The canonical perm for sidecar JSONs is 0o644 (rw-r--r--). Future
// PRs that need a tighter perm (e.g. provenance exclusions) extend
// here rather than each call site.
package metadataexport

import (
	"os"
)

// ensureDir performs an idempotent mkdir -p with the canonical 0o755
// perm (matches the legacy implementation's MkdirAll call). Exists →
// no-op; missing → MkdirAll walks the path. The 0o755 perm keeps the
// parent-walk consistent with the existing data/asset_metadata path
// expectations across the rest of the codebase.
//
// Lives here (not in the application package) because os.MkdirAll is a
// filesystem-shaped concern — keeping it out of the application layer
// preserves AGENTS.md Pattern 8 ("internal/capabilities/** non deve
// contenere business orchestration, no concrete infrastructure
// imports").
func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// atomicWrite performs the POSIX-atomic rename:
//
//  1. Write body to absPath + ".tmp" with 0o644 perms.
//  2. os.Rename(tmp, final) — atomic on linux/macos when tmp and
//     final are on the same filesystem (always true here because
//     the caller writes the .tmp alongside the final path).
//  3. On rename failure: best-effort cleanup of the .tmp (so a
//     retry doesn't trip over a half-written tmp from the prior
//     attempt).
//
// Callers MUST ensure the directory exists before calling — see
// ensureDir above. The atomic guarantee covers the file itself, not
// the parent path.
func atomicWrite(absPath string, body []byte) error {
	tmpPath := absPath + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, absPath); err != nil {
		// Best-effort cleanup; ignore the error because the caller
		// will surface the original rename failure.
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
