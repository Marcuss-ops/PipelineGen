// Package completion owns the canonical completion-domain typed contracts
// (godlike/06 one-canonical-owner-per-fact). FASE 2.2 (July 2026) closes
// the pre-FASE-2.2 split between the worker-side `*workspace.Workspace`
// (internal/application/jobs/worker/workspace.go) and the document-side
// `workspace.Manager` (internal/application/document/service.go) by
// introducing a single typed port — `WorkspaceStore` — that any
// completion-side caller (P0-COMPL atomic complete, document job
// artefact staging, outbox delivery, TTL-aware sweeper) consumes.
//
// godlike/06 SSOT rationale: the previous dual-surface (one struct in
// the worker package, one Manager interface in the document package)
// had no canonical owner for the workspace-path policy (P0-COMPL-7) or
// for the post-completion retention behaviour (P0-COMPL-6). Both
// capabilities would have drifted independently — a godlike/06
// SSOT violation per the canonical-doc rule. FASE 2.2 collapses the
// two overlapping surfaces into one typed port + an optional
// application-layer adapter (forward-pointer: the existing
// `*worker.Workspace` will be wrapped at
// `internal/application/jobs/completion/workspace_store_adapter.go` in
// a subsequent wire-closure PR — FASE 2.2 ships the canonical SSOT
// without breaking the concrete callers).
//
// godlike/07 typed-error contract: every WorkspaceStore method returns
// a typed sentinel from the package-private error list below so callers
// can dispatch via errors.Is without string matching (no internal
// error types leak to the wire).
//
// Design precedent: DRIVE-005 (4 canonical Pattern-0 ports collapsed
// from the raw `*gdrive.Service` reach-through surface) — the
// WorkspaceStore port follows the same "low-format / high-semantic"
// idiom (no Maeven-style CRUD primitives — calls are
// Prepare/Complete/Evict/MarkTTL, each expressing the capability, not
// the storage primitive).
package completion

import (
	"errors"
	"time"
)

// WorkspaceStore is the canonical high-level domain port for any
// caller that needs to provision, mark-complete, and eventually evict
// a job-scoped workspace directory.
//
// godlike/06 SSOT: this is the SOLE canonical owner of the workspace
// provision / completion / eviction lifecycle. The pre-FASE-2.2
// worker-side `*workspace.Workspace` and document-side `workspace.Manager`
// are both concrete adapters that may wrap this port — the port's
// method shape is the canonical contract, not the concrete impl.
//
// godlike/07 minimum-blast-radius: Prepare is called at job-claim time
// (returns a WorkspaceHandle that downstream consumers bind to via the
// embedded Path string). Complete is called at the post-Complete atomic
// TX seal (writes the sha256 + completion metadata; eviction is NOT
// inline — see godlike/07 SWEEPER rationale in retention.go). Evict
// is called by the canonical sweeper goroutine ONLY (not by
// inline-callers) so the eviction latency does not gate the
// atomic-Complete TX latency (per godlike/07 no-fake-availability
// inline-eviction is not silently elided — it is EXPLICITLY moved to
// the sweeper so the latency is observable and any eviction failure
// surfaces as a typed error).
type WorkspaceStore interface {
	// Prepare provisions a workspace for the given jobID. Callers pass a
	// TTL hint (MaxTTL upper-bound); the canonical retention policy
	// in retention.go caps it against Policy.MaxTTL. Returns
	// ErrWorkspaceAlreadyExists if a workspace for jobID is already open.
	Prepare(jobID string, ttlHint time.Duration) (WorkspaceHandle, error)

	// Complete seals the workspace with the canonical sha256 of the
	// post-completion artefact bundle (gates retry-races by the
	// P0-COMPL-5 atomic TX contract). Subsequent Evict calls honour
	// the canonical RetentionPolicy (retention.go). Returns
	// ErrWorkspaceNotFound if the workspace was never Prepared.
	Complete(jobID string, sha256 string) error

	// Evict removes a workspace. Called by the canonical sweeper
	// goroutine (retention.go) ONLY — production callers MUST NOT invoke
	// Evict directly (it would bypass the canonical observability layer
	// that emits the `voiceover.cleanup.requested` outbox event per
	// godlike/07 no-fake-availability). Returns ErrWorkspaceNotFound
	// if the workspace was already evicted or never Prepared.
	Evict(jobID string) error

	// MarkTTL extends (or shortens) the eviction-deadline of an
	// existing workspace. Used for retry-pending jobs that need their
	// workspace kept alive slightly longer than the canonical TTL.
	// Returns ErrWorkspaceNotFound if the workspace is absent.
	MarkTTL(jobID string, ttl time.Duration) error

	// Path returns the canonical filesystem path for jobID's workspace
	// directory. Per godlike/07 typed-error contract, the return is
	// `(string, error)` symmetric with the sibling methods:
	//   - On success: the absolute path of the workspace directory.
	//   - On absent: returns ("", ErrWorkspaceNotFound) — the empty
	//     string is NOT a valid sentinel (godlike/07 typed errors
	//     disallow empty-string sentinels that callers might miss).
	//   - The path format is owned by the adapter (P0-COMPL-7) and
	//     surfaced via this method so callers can compose subdirectories
	//     under the workspace root without leaking the layout.
	Path(jobID string) (string, error)
}

// WorkspaceHandle is the typed envelope returned by Prepare. The
// embedded Path string is the canonical workspace root; the embedded
// ExpiresAt timestamp is the canonical eviction-deadline the sweeper
// reads on every cycle.
type WorkspaceHandle struct {
	Path      string
	ExpiresAt time.Time
}

// godlike/07 typed-error contract: 5 sentinels cover the typed
// failure modes. errors.Is probes are the canonical test surface.
//
// Note: ErrWorkspaceAlreadyExists + ErrWorkspaceNotFound are
// duplicates of the worker-side Workspace.Prepare return-value+
// error-state semantics so callers migrating from the pre-FASE-2.2
// surface do not need a second error-mapping table.
var (
	// ErrWorkspaceStoreNotConfigured is returned when no concrete
	// adapter has been wired into the composition root. godlike/07
	// fail-closed at composition time per DRIVE-005 precedent.
	ErrWorkspaceStoreNotConfigured = errors.New("completion: workspace store not configured (nil port at composition root)")

	// ErrWorkspaceAlreadyExists is returned by Prepare when a
	// workspace for the given jobID is already open. The caller can
	// either call Complete first (idempotent retry-safe) or call
	// Evict + Prepare (cold-start after eviction).
	ErrWorkspaceAlreadyExists = errors.New("completion: workspace already exists for jobID")

	// ErrWorkspaceNotFound is returned by Complete / Evict / MarkTTL
	// / Path when the workspace was never Prepared or already
	// Evicted. The canonical recovery is Prepare(jobID) + retry the
	// operation.
	ErrWorkspaceNotFound = errors.New("completion: workspace not found for jobID")

	// ErrWorkspaceMarkTTLFailed is returned by MarkTTL when the
	// requested ttl exceeds RetentionPolicy.MaxTTL (godlike/07
	// fail-closed — the caller cannot exceed the canonical MaxTTL
	// bound).
	ErrWorkspaceMarkTTLFailed = errors.New("completion: MarkTTL exceeds RetentionPolicy.MaxTTL")

	// ErrWorkspaceHandleExpired is returned when a WorkspaceHandle
	// has post-Eviction already removed its underlying directory;
	// the caller should Prepare a fresh handle and retry the
	// operation. This sentinel is added so a future adapter that
	// returns a struct strong-handle (vs the current string-only
	// surface) can route the error without leaking ad-hoc strings.
	ErrWorkspaceHandleExpired = errors.New("completion: workspace handle expired (post-eviction)")
)

// godlike/06 SSOT: the typed-error contract requires that callers
// compose via errors.Is (pointer-identity match). The compile-time
// assertions below pin each sentinel as a valid `error` implementor
// and pin the WorkspaceHandle shape so a future field addition
// surfaces as a struct-construction error at compile time rather
// than a silent JSON-wire drift.
var (
	_ error     = ErrWorkspaceStoreNotConfigured
	_ error     = ErrWorkspaceAlreadyExists
	_ error     = ErrWorkspaceNotFound
	_ error     = ErrWorkspaceMarkTTLFailed
	_ error     = ErrWorkspaceHandleExpired
	_ time.Time = WorkspaceHandle{}.ExpiresAt // field-shape pin
)
