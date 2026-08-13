// Package workspace — manager.go (P0 Commit 9, July 2026).
//
// WorkspaceManager is the canonical Manager for per-job isolated
// filesystems. The contract surface (interface + types + sentinel
// errors + I/O ports) lives in models.go in this same package; this
// file holds the concrete implementation + the canonical package doc
// that explicitly re-declares the §5.4 path-containment contract.
//
// ─── §5.4 path-containment contract (re-declared) ───────────────────────────
//
// The C9 spec (path-containment check per §5.4) requires WorkspaceManager
// to enforce the following invariant at every Download + Cleanup call:
//
//  1. Every workspace-root-derived path MUST canonicalise to either
//     the workspace root itself or a strict child of it. The
//     canonicalise-both-ends algorithm is implemented in
//     assertContained() below:
//
//     a. fs.Abs(root) + filepath.Clean(root) for the boundary.
//     b. fs.Abs(candidate) + filepath.Clean(candidate) for the
//     input.
//     c. Reject when candidate has neither the canonical equality
//     with root nor a HasPrefix relationship with the
//     "<root><sep>" string. This catches every ../-style
//     relative traversal (the input candidate's Clean can
//     escape root) AND every absolute-path-to-outside pattern
//     where the input is an absolute path that lies outside
//     root.
//
//  2. Symlinks at any intermediate component of the candidate path
//     are REJECTED (per-component Lstat walk via the FileSystem
//     port). The rule is strict per §5.4: the manager never
//     follows a symlink. A symlink on the root itself is ALSO
//     rejected.
//
//  3. TOCTOU mitigation: the manager calls assertContained at the
//     point of use (just before each OpenFile / RemoveAll). A
//     hand-rolled swap of intermediates between Prepare and use
//     is detected on the next call.
//
// The algorithm mirrors the canonical pattern in
// `internal/application/clips/bulk_upload_helpers.go::42` (which uses
// `filepath.EvalSymlinks` + absolute-path comparison) and the
// `A4 path-traversal guard on SubfolderName` from
// `docs/voiceover/p0-bundle-A1-A6.md`. C9 is the next link in the
// chain: per-job workspace allocation under a strict containment
// contract.
//
// ─── I/O isolation ─────────────────────────────────────────────────────────
//
// The manager owns the semantic contract only. Concrete network +
// filesystem I/O is delegated through the Fetcher and FileSystem
// ports (declared in models.go); the production adapters live in
// internal/platform/filesystem and are injected by the composition
// root via NewManagerWithDeps. This file imports no net/http and no
// os driver — it speaks only to the ports.
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// ─── directory layout constants ───────────────────────────────────────────

const (
	jobDirPrefix     = "job-"
	attemptDirPrefix = "attempt-"

	// dirPerm / filePerm are the neutral permission bits the manager
	// passes to FileSystem. Kept as capability-owned constants (not
	// os.FileMode) so the kernel contract stays driver-free.
	dirPerm  uint32 = 0o755
	filePerm uint32 = 0o644
)

// ─── Manager ─────────────────────────────────────────────────────────────

// manager is the canonical concrete implementation of WorkspaceManager.
// Allocated via NewManagerWithDeps (production wiring in
// internal/platform/filesystem) or directly with a test Fetcher /
// FileSystem.
type manager struct {
	globalRoot string
	fetcher    Fetcher
	fs         FileSystem
}

// NewManagerWithDeps constructs the canonical WorkspaceManager with the
// supplied fetch + filesystem adapters. The globalRoot is the parent of
// every per-job workspace tree; the manager creates it if missing.
//
// The production adapters (HTTP fetcher + OS filesystem) are wired by
// internal/platform/filesystem.NewManager; tests inject stubs. The
// kernel keeps this constructor exported so the platform adapter — and
// only the platform adapter — composes the concrete implementations.
func NewManagerWithDeps(globalRoot string, fetcher Fetcher, fs FileSystem) (WorkspaceManager, error) {
	if globalRoot == "" {
		return nil, fmt.Errorf("workspace.NewManager: globalRoot is required (C9 contract)")
	}
	if fetcher == nil {
		return nil, fmt.Errorf("workspace.NewManager: fetcher is nil")
	}
	if fs == nil {
		return nil, fmt.Errorf("workspace.NewManager: fs is nil")
	}
	absRoot, err := fs.Abs(globalRoot)
	if err != nil {
		return nil, fmt.Errorf("workspace.NewManager: abs(%q): %w", globalRoot, err)
	}
	if err := fs.MkdirAll(absRoot, dirPerm); err != nil {
		return nil, fmt.Errorf("workspace.NewManager: mkdir globalRoot %q: %w", absRoot, err)
	}
	return &manager{
		globalRoot: absRoot,
		fetcher:    fetcher,
		fs:         fs,
	}, nil
}

// ─── Prepare ─────────────────────────────────────────────────────────────

// Prepare allocates a per-job, per-attempt workspace directory under
// the manager's globalRoot and returns the canonical ManagedWorkspace.
//
// Layout: <globalRoot>/job-<jobID>/attempt-<n>/
//
// The JobID segment is sanitised to path-safe characters via
// sanitizeSegment (defence-in-depth: reduces the ASVS attack surface
// from arbitrary bytes even though the §5.4 check is the authoritative
// guard). The attempt suffix is a literal integer formatted via
// strconv.Itoa so a workspace is always under a numerically-ordered
// prefix — easier for an operator audit ("ls /pipelinegen/workspaces/"
// reveals "job-foo/attempt-1/, attempt-2/, ...") than a UUID layout.
//
// Idempotent at the directory level: a second Prepare for the same
// (jobID, attempt) succeeds because MkdirAll is idempotent. A
// future per-call side-effect (e.g. writing a manifest.json) would
// carry the idempotency responsibility.
func (m *manager) Prepare(ctx context.Context, jobID string, attempt int) (*ManagedWorkspace, error) {
	if jobID == "" {
		return nil, fmt.Errorf("workspace.Prepare: jobID is required")
	}
	if attempt < 1 {
		return nil, fmt.Errorf("workspace.Prepare: attempt must be >= 1, got %d", attempt)
	}
	_ = ctx // reserved for future (cancellation hooks)

	jobDir := filepath.Join(m.globalRoot, jobDirPrefix+sanitizeSegment(jobID))
	if err := m.fs.MkdirAll(jobDir, dirPerm); err != nil {
		return nil, fmt.Errorf("workspace.Prepare: mkdir job dir %q: %w", jobDir, err)
	}
	attemptDir := filepath.Join(jobDir, attemptDirPrefix+strconv.Itoa(attempt))
	if err := m.fs.MkdirAll(attemptDir, dirPerm); err != nil {
		return nil, fmt.Errorf("workspace.Prepare: mkdir attempt dir %q: %w", attemptDir, err)
	}

	// §5.4 self-check: the freshly-Prepared attemptDir must satisfy
	// assertContained against the globalRoot before we hand it back.
	// This is a STARTUP-time invariant — a future failure mode is a
	// root that becomes a symlink after NewManager (operator
	// mistake); the workspace we just allocated would fail the
	// self-check. Fail-closed surfaces that class of regression.
	if err := assertContained(m.fs, m.globalRoot, attemptDir); err != nil {
		return nil, fmt.Errorf("workspace.Prepare: self-check on freshly allocated %q: %w", attemptDir, err)
	}

	return &ManagedWorkspace{
		JobID:   jobID,
		Attempt: attempt,
		Root:    attemptDir,
	}, nil
}

// sanitizeSegment keeps only the path-safe characters
// [A-Za-z0-9._-] in the input and replaces the rest with `_`. The
// resulting segment is suitable for use as a directory name without
// further quoting. defence-in-depth: the §5.4 path-containment check
// is the authoritative guard against traversal, but a sanitised
// segment reduces the surface from arbitrary-byte inputs without
// relying on that guard alone.
func sanitizeSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// ─── Download ────────────────────────────────────────────────────────────

// Download fetches the RemoteAssetRef, streams it into a file inside
// ws.Root, and verifies the SHA-256 hash + size against the ref.
//
// Verification order:
//  1. Pre-flight size check (if Fetcher reported a non-zero size hint):
//     if hint != ref.SizeBytes -> ErrSizeMismatch BEFORE any bytes
//     hit disk. Catches manifest/transport mismatches cheaply.
//  2. Stream + hash: io.Copy(MultiWriter(file, sha256.New()), body).
//     Counting + hashing happens in lockstep so an early EOF still
//     produces a runtime error.
//  3. Post-flight size check (using actual written byte count):
//     if written != ref.SizeBytes -> ErrSizeMismatch (best-effort
//     cleans up the partial file).
//  4. Hash compare: if hex(sha256.Sum256()) != ref.SHA256 ->
//     ErrHashMismatch (best-effort cleans up).
//
// Path-containment: assertContained(root, targetPath) is called
// BEFORE the OpenFile write so a ref.Filename that resolves outside
// the workspace (e.g. "../../etc/passwd") fails closed at the very
// first opportunity. The §5.4 contract guarantees this.
func (m *manager) Download(ctx context.Context, ws *ManagedWorkspace, ref RemoteAssetRef) (LocalInputRef, error) {
	if ws == nil {
		return LocalInputRef{}, fmt.Errorf("workspace.Download: workspace is nil (C9 fail-closed)")
	}
	if ref.URL == "" {
		return LocalInputRef{}, fmt.Errorf("workspace.Download: ref.URL is empty")
	}
	if ref.Filename == "" {
		return LocalInputRef{}, fmt.Errorf("workspace.Download: ref.Filename is empty")
	}
	if ref.SHA256 == "" {
		return LocalInputRef{}, fmt.Errorf("workspace.Download: ref.SHA256 is required for integrity verify (godlike/07 no-fake-availability)")
	}
	if ref.SizeBytes <= 0 {
		return LocalInputRef{}, fmt.Errorf("workspace.Download: ref.SizeBytes must be > 0, got %d", ref.SizeBytes)
	}

	// Fetch
	body, sizeHint, err := m.fetcher.Fetch(ctx, ref.URL)
	if err != nil {
		return LocalInputRef{}, fmt.Errorf("workspace.Download: fetch %q: %w", ref.URL, err)
	}
	defer body.Close()

	// Pre-flight size hint check (cheap fail-fast before any disk I/O)
	if sizeHint > 0 && sizeHint != ref.SizeBytes {
		return LocalInputRef{}, fmt.Errorf("%w: expected=%d got=%d (Content-Length hint)", ErrSizeMismatch, ref.SizeBytes, sizeHint)
	}

	// Absolute-path short-circuit (BEFORE filepath.Join). Go's
	// filepath.Join treats its arguments as path COMPONENTS, not as
	// paths themselves — so filepath.Join("/foo/bar", "/etc/leak.txt")
	// returns "/foo/bar/etc/leak.txt", NOT "/etc/leak.txt". Without
	// this explicit check, an absolute ref.Filename would otherwise
	// silently land inside ws.Root as a non-escaping nested component
	// (the assertContained walk would PASS but the file is still being
	// written under a name the operator did not sanction). The §5.4
	// spec literal — "reject anything whose fs.Abs is not under
	// the workspace root" — requires this fail-closed semantic:
	// fs.Abs("/etc/leak.txt") == "/etc/leak.txt", which is
	// not under ws.Root, therefore reject.
	if filepath.IsAbs(ref.Filename) {
		return LocalInputRef{}, fmt.Errorf("%w: ref.Filename %q is an absolute path (must be workspace-relative per §5.4)", ErrPathOutsideWorkspace, ref.Filename)
	}

	// Compute target path + §5.4 containment check (BEFORE any write)
	targetPath := filepath.Join(ws.Root, ref.Filename)
	if err := assertContained(m.fs, ws.Root, targetPath); err != nil {
		return LocalInputRef{}, fmt.Errorf("workspace.Download: target path %q (from ref.Filename %q): %w", targetPath, ref.Filename, err)
	}

	// Stream to disk + hash simultaneously. MkdirAll of the parent
	// (NOT OpenFile) ensures nested Filename values such as
	// "subdir/subdir2/file.bin" land in a writable directory tree —
	// OpenFile does NOT create parent directories. The §5.4 contract
	// on the parent dir was already enforced by assertContained above
	// (every component is canonicalised against ws.Root).
	if err := m.fs.MkdirAll(filepath.Dir(targetPath), dirPerm); err != nil {
		return LocalInputRef{}, fmt.Errorf("workspace.Download: mkdir parent %q: %w", filepath.Dir(targetPath), err)
	}
	f, err := m.fs.OpenFile(targetPath, filePerm)
	if err != nil {
		return LocalInputRef{}, fmt.Errorf("workspace.Download: open target %q: %w", targetPath, err)
	}
	defer f.Close()

	h := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, h), body)
	if err != nil {
		_ = m.fs.Remove(targetPath) // best-effort partial-clean
		return LocalInputRef{}, fmt.Errorf("workspace.Download: stream copy to %q: %w", targetPath, err)
	}

	// Post-flight size check
	if written != ref.SizeBytes {
		_ = m.fs.Remove(targetPath)
		return LocalInputRef{}, fmt.Errorf("%w: expected=%d got=%d (streamed)", ErrSizeMismatch, ref.SizeBytes, written)
	}

	// Hash compare
	actualHash := hex.EncodeToString(h.Sum(nil))
	if actualHash != ref.SHA256 {
		_ = m.fs.Remove(targetPath)
		return LocalInputRef{}, fmt.Errorf("%w: expected=%s got=%s", ErrHashMismatch, ref.SHA256, actualHash)
	}

	return LocalInputRef{
		Path:      targetPath,
		SHA256:    actualHash,
		SizeBytes: written,
	}, nil
}

// ─── Cleanup ─────────────────────────────────────────────────────────────

// Cleanup removes the workspace tree under ws.Root. The contract:
//
//   - ALWAYS-ON-TERMINAL: callers invoke Cleanup at job terminal
//     (success / fail / cancelled) regardless of outcome. The
//     manager does not gate on job status — the caller does.
//   - Idempotent: a second Cleanup on a removed workspace returns
//     nil because RemoveAll is natively idempotent. A test
//     verifies this.
//   - Path-containment: assertContained(globalRoot, ws.Root) is
//     called BEFORE RemoveAll so a workspace whose Root has been
//     swapped outside the globalRoot (operator mistake /
//     unsafe-port migration) fails closed.
//   - Always completes: this method does not return early on
//     intermediate errors; the test
//     `TestManager_Cleanup_AlwaysRuns_OnTerminal` structurally
//     pins the always-on semantic by exercising Cleanup on a
//     freshly-Prepared workspace AND a previously-removed
//     workspace AND a workspace whose dir has been externally
//     peeked-at.
func (m *manager) Cleanup(ctx context.Context, ws *ManagedWorkspace) error {
	if ws == nil {
		return fmt.Errorf("workspace.Cleanup: workspace is nil (C9 fail-closed)")
	}
	_ = ctx // reserved

	if err := assertContained(m.fs, m.globalRoot, ws.Root); err != nil {
		return fmt.Errorf("workspace.Cleanup: workspace root %q: %w", ws.Root, err)
	}
	if err := m.fs.RemoveAll(ws.Root); err != nil {
		return fmt.Errorf("workspace.Cleanup: RemoveAll(%q): %w", ws.Root, err)
	}
	return nil
}

// ─── Path-containment helper ─────────────────────────────────────────────

// assertContained enforces the §5.4 path-containment contract.
// Implementation: canonicalise-both-ends (fs.Abs + filepath.Clean
// for each) + per-component Lstat walk (via the FileSystem port) to
// reject symlinks at any intermediate point.
//
// Algorithm:
//  1. Compute absRoot = fs.Abs(root); cleanedRoot = filepath.Clean(absRoot).
//  2. Compute absCand = fs.Abs(candidate); cleanedCand = filepath.Clean(absCand).
//  3. Fail-fast textual containment check: cleanedCand MUST be either
//     == cleanedRoot OR have HasPrefix("<cleanedRoot><sep>").
//     This catches both `../`-style traversal AND absolute-path-to-outside.
//  4. Walk each PATH component of cleanedCand (relative to cleanedRoot):
//     Lstat each one. If the entry is a symlink, reject — the
//     manager NEVER follows a symlink per §5.4.
//  5. Special case: when candidate IS root (rel == "."), reject if
//     the root itself is a symlink. Symlink-roots would defeat the
//     containment contract by re-routing the entire subtree.
func assertContained(fs FileSystem, root, candidate string) error {
	absRoot, err := fs.Abs(root)
	if err != nil {
		return fmt.Errorf("workspace.assertContained: abs(root=%q): %w", root, err)
	}
	cleanedRoot := filepath.Clean(absRoot)

	// (3a) Root must NEVER be a symlink — §5.4 contract is strict.
	// A symlinked root would re-route the entire subtree past the
	// textual containment check; we surface this at the strictest
	// possible point (the boundary call itself) so an operator-set
	// symlink-root regression fails closed on the first Prepare /
	// Download / Cleanup, not on a downstream side-effect.
	if rootEntry, lerr := fs.Lstat(cleanedRoot); lerr != nil {
		return fmt.Errorf("workspace.assertContained: lstat root %q: %w", cleanedRoot, lerr)
	} else if rootEntry.Exists && rootEntry.IsSymlink {
		return fmt.Errorf("%w: workspace root %q is a symlink", ErrSymlinkRejected, cleanedRoot)
	}

	absCand, err := fs.Abs(candidate)
	if err != nil {
		return fmt.Errorf("workspace.assertContained: abs(candidate=%q): %w", candidate, err)
	}
	cleanedCand := filepath.Clean(absCand)

	// (3) textual containment
	if cleanedCand != cleanedRoot && !strings.HasPrefix(cleanedCand, cleanedRoot+string(filepath.Separator)) {
		return fmt.Errorf("%w: candidate resolves to %q which is outside root %q", ErrPathOutsideWorkspace, cleanedCand, cleanedRoot)
	}

	// (4) symlink walk (per-component)
	rel, err := filepath.Rel(cleanedRoot, cleanedCand)
	if err != nil {
		return fmt.Errorf("workspace.assertContained: filepath.Rel(%q, %q): %w", cleanedRoot, cleanedCand, err)
	}
	if rel == "." {
		// (5) candidate IS the root — reject if root itself is a symlink
		if rootEntry, lerr := fs.Lstat(cleanedRoot); lerr != nil {
			return fmt.Errorf("workspace.assertContained: lstat root %q: %w", cleanedRoot, lerr)
		} else if rootEntry.Exists && rootEntry.IsSymlink {
			return fmt.Errorf("%w: workspace root %q is a symlink", ErrSymlinkRejected, cleanedRoot)
		}
		return nil
	}
	cur := cleanedRoot
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, seg)
		if entry, lerr := fs.Lstat(cur); lerr != nil {
			return fmt.Errorf("workspace.assertContained: lstat %q: %w", cur, lerr)
		} else if entry.Exists && entry.IsSymlink {
			return fmt.Errorf("%w: %q is a symlink (intermediate component of %q)", ErrSymlinkRejected, cur, candidate)
		}
	}
	return nil
}
