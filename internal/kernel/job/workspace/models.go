package workspace

import (
	"context"
	"errors"
	"io"
	"time"
)

// Existing surface (preserved verbatim — DB-backed Workspace metadata).
// C9 does NOT extend this struct; it adds a separate type below.

type Workspace struct {
	ID        string
	Name      string
	Slug      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Repository interface {
	Create(ctx context.Context, ws *Workspace) error
	GetByID(ctx context.Context, id string) (*Workspace, error)
	GetBySlug(ctx context.Context, slug string) (*Workspace, error)
	Update(ctx context.Context, ws *Workspace) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]*Workspace, error)
}

// ── P0 Commit 9 (C9) — per-job workspace surface ────────────────────────
//
// Canonical surface added for the Creator/Sender workspace flow. Lives in
// the same file as the DB-backed types above so a future scrub can audit
// the file as a single source for the workspace package.
//
// REFERENCES:
//   - §5.4 path-containment spec (the spec was folded into AGENTS.md /
//     ARCHITECTURE.md during June 2026 docs/ removal; C9's package doc
//     in manager.go explicitly re-declares the rule set below).
//   - A4 path-traversal guard on SubfolderName (June 2026
//     docs/voiceover/p0-bundle-A1-A6.md): the canonical Go algorithm
//     pattern is canonicalise-both-ends + per-component os.Lstat walk.
//     C9's assertContained below follows this pattern.
//
// WHY ManagedWorkspace IS DISTINCT FROM Workspace:
//   The existing `Workspace` type above is a persistent, DB-backed
//   record (ID + Name + Slug + created/updated timestamps, plumbed
//   through the Repository port). C9 needs an ephemeral, filesystem-
//   scoped record (JobID + Attempt + Root — no ID/Slug/DB). Adding
//   those fields to the existing struct would break the Repository
//   wire-format and force a SQLite migration that is OUT of C9 scope.
//   The dual-type vocabulary mirrors the C5 ArtifactManifest /
//   RemoteArtifactManifest split: one shape for canonical-local
//   state, one for the runtime / on-disk surface.

// ManagedWorkspace is the filesystem-scoped record returned by
// WorkspaceManager.Prepare. It tracks the canonical on-disk path
// for a single (jobID, attempt) pair so a downstream handler can
// read/write files exclusively inside that boundary.
type ManagedWorkspace struct {
	// JobID is the producer's canonical job identifier (matches
	// job.Job.ID). Sanitised on disk to a path-safe segment
	// (see sanitizeSegment in manager.go).
	JobID string

	// Attempt is the per-job retry ordinal (1-indexed; first attempt
	// is 1, retry is 2, etc.). Used as the directory suffix so each
	// retry allocates its OWN tree (no in-place reuse).
	Attempt int

	// Root is the absolute path to the allocated directory.
	// Format: <globalRoot>/job-<jobID>/attempt-<n>/
	// Verified to satisfy the §5.4 path-containment contract via
	// assertContained(...) on every Cleanup + Download.
	Root string
}

// RemoteAssetRef is the wire-shape contract handed to Download. The
// hash + size fields are authoritative — Download WILL refuse the
// payload if the streamed bytes do not verify against them. This
// is the producer-side analogue of the artifact-manifest RemoteAsset
// (which carries the same SHA256 / SizeBytes dual verification).
type RemoteAssetRef struct {
	// URL is the canonical fetch target. http:// and https:// are
	// supported by the default Fetcher; scheme-extension is reserved
	// for future port additions (e.g. gs://, s3://).
	URL string

	// Filename is the leaf name written inside the workspace. The
	// manager joins this into ws.Root and then validates the result
	// passes the §5.4 path-containment check.
	Filename string

	// SHA256 is the canonical hex-encoded SHA-256 digest the
	// streamed bytes MUST match. If the stream produces a different
	// digest, Download returns ErrHashMismatch and the partial file
	// is removed (best-effort).
	SHA256 string

	// SizeBytes is the canonical byte count the streamed body MUST
	// match. If the byte count differs, Download returns
	// ErrSizeMismatch (which fires BEFORE the hash check when a
	// reliable Content-Length hint is available).
	SizeBytes int64

	// MIMEType is informational only — carries intent, not a
	// verify target. Future port surfaces may use it for content
	// sniffing or upload-time routing.
	MIMEType string
}

// LocalInputRef is the on-disk record returned by Download. The
// Path field is GUARANTEED to pass the §5.4 path-containment check
// against the originating ws.Root (the manager runs assertContained
// before returning; a future port that bypasses this check would
// itself be a §5.4 violation).
type LocalInputRef struct {
	// Path is the absolute filesystem path to the verified file.
	// Always inside the originating ManagedWorkspace.Root.
	Path string

	// SHA256 is the hex-encoded SHA-256 digest of the file contents
	// (computed during stream-to-disk; matches the RemoteAssetRef's
	// declared SHA256 by construction).
	SHA256 string

	// SizeBytes is the byte count of the verified file (matches the
	// RemoteAssetRef's declared SizeBytes by construction).
	SizeBytes int64
}

// WorkspaceManager is the canonical port for per-job isolated
// workspacing. It is consumed by Creator-side handlers (which write
// artefacts into the workspace then hash+upload them via the Sender)
// and by Sender-side job runners (which Download remote inputs into
// the workspace then run them through the postprocessor pipeline).
//
// Pattern 0 contract (godlike/07 no-fake-availability): the interface
// is the canonical surface; the concrete impl lives in manager.go and
// is injected by composition root via NewManager(...).
type WorkspaceManager interface {
	// Prepare allocates a per-job, per-attempt workspace directory
	// under the manager's globalRoot and returns a ManagedWorkspace
	// whose Root points at the new directory. The allocation is
	// idempotent at the directory level (a second Prepare for the
	// same (jobID, attempt) succeeds; canonical reuse permitted).
	Prepare(ctx context.Context, jobID string, attempt int) (*ManagedWorkspace, error)

	// Download fetches the RemoteAssetRef, streams it into a file
	// inside ws.Root, and verifies the SHA-256 hash + SizeBytes
	// against the ref. Returns a LocalInputRef whose Path is
	// inside ws.Root (verified by the §5.4 path-containment check
	// before return). A hash mismatch returns ErrHashMismatch;
	// a size mismatch returns ErrSizeMismatch. Both are wrapped so
	// callers probe via errors.Is.
	Download(ctx context.Context, ws *ManagedWorkspace, ref RemoteAssetRef) (LocalInputRef, error)

	// Cleanup removes the workspace tree under ws.Root. Idempotent:
	// returns nil when the directory is already externally removed.
	// Always-on-terminal: callers invoke Cleanup at job terminal
	// (success, fail, cancelled) regardless of outcome. A symlink
	// in the workspace tree is NOT followed; the cleanup walks
	// only directory entries the manager itself created.
	Cleanup(ctx context.Context, ws *ManagedWorkspace) error
}

// ── Sentinel errors (godlike/07 contract — exported for errors.Is) ─────

// ErrPathOutsideWorkspace is returned by the canonical assertContained
// helper when a candidate path canonicalises to a location that is
// not the manager's globalRoot or a strict child of it. This covers
// the `../`-style relative-traversal AND the `abs-path-to-outside`
// cases. The §5.4 spec requires both shapes to fail closed.
var ErrPathOutsideWorkspace = errors.New("workspace: path is outside the workspace root (path containment §5.4)")

// ErrSymlinkRejected is returned when an intermediate component of a
// candidate path is a symlink (or, for the root, when the root
// itself is a symlink). The §5.4 rule is strict: symlinks are not
// followed by the manager, regardless of where they resolve to.
var ErrSymlinkRejected = errors.New("workspace: symlink in path is rejected (path containment §5.4)")

// ErrHashMismatch is returned by Download when the streamed body's
// SHA-256 does not match RemoteAssetRef.SHA256. The partial file is
// removed best-effort before the error returns.
var ErrHashMismatch = errors.New("workspace: downloaded SHA-256 does not match RemoteAssetRef")

// ErrSizeMismatch is returned by Download when the streamed body's
// byte count does not match RemoteAssetRef.SizeBytes. The size hint
// from the Fetcher (typically Content-Length) is consulted FIRST, so
// a known-mismatch fails without writing any bytes.
var ErrSizeMismatch = errors.New("workspace: downloaded SizeBytes does not match RemoteAssetRef")

// ── I/O ports (godlike/06 SSOT — contract only, no drivers) ─────────────
//
// The kernel owns the semantic contract; concrete network + filesystem
// I/O lives behind these ports and is wired by the composition root via
// internal/platform/filesystem. The manager never imports net/http or
// os directly — it speaks only to Fetcher and FileSystem.

// Fetcher is the network-transport port the manager delegates asset download
// to. Concrete HTTP transport belongs to the platform adapter.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (body io.ReadCloser, sizeHint int64, err error)
}

// FileEntry is the neutral filesystem-entry view surfaced by
// FileSystem.Lstat. It carries only the facts the §5.4 path-containment
// contract needs — existence + symlink flag — not the full os.FileInfo
// (which would leak an os-driver type into the kernel contract).
type FileEntry struct {
	// Exists is true when the path resolves to a real entry. A
	// not-exist path returns Exists=false with a nil error so the
	// caller can branch on existence without probing os.ErrNotExist.
	Exists bool

	// IsSymlink is true when the entry is a symbolic link. The §5.4
	// contract rejects symlinks at any intermediate component.
	IsSymlink bool
}

// FileSystem is the filesystem port the manager delegates concrete
// disk I/O to. All operations use neutral, capability-owned parameter
// types (uint32 permission bits, FileEntry) rather than os.FileMode /
// os.FileInfo so the kernel stays free of the os driver.
type FileSystem interface {
	// Abs resolves path to its absolute, cleaned canonical form.
	Abs(path string) (string, error)

	// MkdirAll creates the directory (and parents) with the given
	// permission bits. Idempotent like os.MkdirAll.
	MkdirAll(path string, perm uint32) error

	// OpenFile opens path for writing, creating/truncating it with
	// the given permission bits. The write/truncate flags are owned
	// by the adapter (the manager's only use case is "stream a fresh
	// download into place").
	OpenFile(path string, perm uint32) (io.WriteCloser, error)

	// Remove deletes a single file (best-effort partial-clean paths).
	Remove(path string) error

	// RemoveAll removes a path recursively. Idempotent like
	// os.RemoveAll.
	RemoveAll(path string) error

	// Lstat returns a FileEntry for path WITHOUT following symlinks.
	// A not-exist path returns FileEntry{Exists:false} and a nil error.
	Lstat(path string) (FileEntry, error)
}
