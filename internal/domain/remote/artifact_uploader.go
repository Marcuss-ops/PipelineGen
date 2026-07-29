// Package remote defines the canonical domain contracts for remote-worker
// coordination: typed upload protocol surface, state-machine, and
// idempotency-key derivation. The interfaces here are implemented by
// concrete HTTP adapters in internal/infrastructure/remote/* and consumed
// by the Sender-side job runner via AGENTS.md Pattern 0 port abstractions.
//
// P0 Commit 6 (July 2026) introduces:
//   - ArtifactUploader — 3-method Prepare/Upload/Finalize protocol
//   - UploadSession — typed envelope (id, leaseId, artifactId, state)
//   - UploadState — 6-state machine (PREPARING→UPLOADING→UPLOADED→
//     VERIFIED→FINALIZED + FAILED sticky sink)
//   - ArtifactIdempotencyKey — deterministic SHA-256 triple-key helper
//   - 5 typed sentinel errors — godlike/07 typed-error contract (%w wrap +
//     errors.Is/As traversal)
//   - IllegalTransitionError — typed-error-data envelope exposing
//     {From, To} for caller-side routing
//
// Migration sequence per godlike/07: EXPAND (this commit, surface live)
// → BACKFILL (C7 migrates callers from pre-C6 worker.AssetClient.UploadFile)
// → CUTOVER (C8 retires the legacy surface) → CONTRACT.
package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ── UploadState (closed enum, 6 values) ────────────────────────────────

// UploadState is the canonical state of an in-flight artifact upload
// session. The 6 values form a directed progression with FAILED and
// FINALIZED as sticky-terminal sinks.
//
// Migration invariants:
//   - String values are UPPERCASE (mirrors LifecycleState convention
//     in internal/kernel/asset/asset_types.go).
//   - The canonical list is locked at 6 values; adding a new value
//     requires a godlike/07 4-phase migration.
//   - Self-loops are idempotent (s.IsValidTransition(s) returns true).
//   - FAILED and FINALIZED are sticky-terminal sinks (no transition
//     out of either; callers must invent a NEW session to retry).
//   - The zero-value (s == "") is INVALID — NewUploadSession is the
//     canonical constructor that initialises State to PREPARING so a
//     half-wired session cannot slip through Valid() as empty-but-
//     valid.
type UploadState string

const (
	// StateUploadPreparing — session is being initialized on the
	// remote-side. The remote-side has accepted the prepare-command
	// and is allocating a session slot.
	StateUploadPreparing UploadState = "PREPARING"

	// StateUploadUploading — content is being streamed to the
	// remote-side (the upload-file command is in flight).
	StateUploadUploading UploadState = "UPLOADING"

	// StateUploadUploaded — content has been received by the
	// remote-side but server-side verification has not yet completed.
	StateUploadUploaded UploadState = "UPLOADED"

	// StateUploadVerified — the remote-side has verified the uploaded
	// bytes against the declared sha256 (server-side hash check
	// passed). Pre-terminal hop on the way to FINALIZED.
	StateUploadVerified UploadState = "VERIFIED"

	// StateUploadFinalized — session is closed; the artifact is
	// catalogued in the remote-storage system. Sticky-terminal: the
	// session cannot be re-uploaded.
	StateUploadFinalized UploadState = "FINALIZED"

	// StateUploadFailed — session is in a non-recoverable failure
	// state. Sticky-terminal: any further transitions are rejected.
	// Callers must invent a NEW session to retry the upload.
	StateUploadFailed UploadState = "FAILED"
)

// CanonicalUploadStateValues returns the closed enumeration of
// canonical UploadState strings. Callers use this as the
// single-source-of-truth list for migrations, dashboards, and
// qdrant-payload validation (parallels CanonicalLifecycleStateValues
// in domain/asset).
func CanonicalUploadStateValues() []UploadState {
	return []UploadState{
		StateUploadPreparing,
		StateUploadUploading,
		StateUploadUploaded,
		StateUploadVerified,
		StateUploadFinalized,
		StateUploadFailed,
	}
}

// Valid returns true if s is a known canonical UploadState. The
// zero-value (s == "") is intentionally invalid — callers must
// initialise UploadSession.State to StateUploadPreparing via
// NewUploadSession so a half-wired session cannot slip through
// validation as valid-but-empty.
func (s UploadState) Valid() bool {
	switch s {
	case StateUploadPreparing, StateUploadUploading, StateUploadUploaded,
		StateUploadVerified, StateUploadFinalized, StateUploadFailed:
		return true
	}
	return false
}

// IsValidTransition reports whether moving from `from` to `to` is
// one of the allowed edges of the upload state machine.
//
// Legal progression (forward chain):
//
//	PREPARING  → UPLOADING → UPLOADED → VERIFIED  → FINALIZED
//	   │            │           │          │
//	   └─→ FAILED   └─→ FAILED  └─→ FAILED └─→ FAILED
//
// Self-loops are IDEMPOTENT (s.IsValidTransition(s) returns true) —
// re-running the same-state handler is a no-op rather than a fatal
// transition error. Mirrors the LifecycleState convention in
// internal/kernel/asset/asset_types.go.
//
// Skip-ahead PREPARING→UPLOADED is REJECTED — the forward chain is
// strictly 1-step. The Creator-side adapter threads PREPARING→
// UPLOADING→UPLOADED via two separate commands on the remote-side.
//
// Skip-ahead UPLOADED→VERIFIED is LEGAL — the Creator adapter's
// Finalize impl skips the intermediate VERIFIED-on-command-return
// because the remote-side's finalize command atomically verifies +
// transitions to VERIFIED + FINALIZED in one call. The adapter
// returns the post-call session state from the server.
//
// All backward edges are REJECTED. FAILED and FINALIZED are
// sticky-terminal sinks.
func (s UploadState) IsValidTransition(to UploadState) bool {
	if s == to {
		// Idempotent self-loop (mirrors LifecycleState.IsValidTransition).
		return true
	}
	switch s {
	case StateUploadPreparing:
		// FAILED is the only legal off-ramp from PREPARING.
		return to == StateUploadUploading || to == StateUploadFailed
	case StateUploadUploading:
		// FAILED is the only legal off-ramp; must complete to
		// UPLOADED before VERIFIED is reachable.
		return to == StateUploadUploaded || to == StateUploadFailed
	case StateUploadUploaded:
		// Skip-ahead to VERIFIED is legal because finalize is atomic
		// on the remote-side. FAILED is the only other off-ramp.
		return to == StateUploadVerified || to == StateUploadFailed
	case StateUploadVerified:
		// VERIFIED -> FINALIZED is the terminal flip on success.
		return to == StateUploadFinalized || to == StateUploadFailed
	case StateUploadFailed, StateUploadFinalized:
		// Sticky-terminal sinks. No transition out.
		return false
	}
	// Zero-value ("") or any non-canonical state: no transitions.
	return false
}

// ── UploadSession ─────────────────────────────────────────────────────

// UploadSession is the typed envelope for a single artifact upload
// in-flight on the remote-side. Persisted by the Creator adapter in
// the session store (typically the local outbox-style row underneath
// the artifact_manifest entry), and resolved by the remote-side HTTP
// server.
//
// The canonical ID convention matches the artifact_manifest.go::Artifact
// ID format: "<jobID>:<kind>:<locale>". The LeaseID is the job-broker
// lease origin that owns this session — the upload-file and
// upload-finalize commands MUST carry the same LeaseID; mismatched
// leases surface as typed errors at the HTTP boundary.
type UploadSession struct {
	// ID is the canonical session identifier (UUID-v4 minted by the
	// remote-side after Prepare).
	ID string `json:"id"`

	// LeaseID is the job-broker lease that owns this session.
	// Verified by the remote-side's authorize middleware.
	LeaseID string `json:"lease_id"`

	// ArtifactID matches the canonical ArtifactManifest.Artifact.ID
	// (the Creator-side's identity for the file being uploaded).
	// Convention: "<jobID>:<kind>" or "<jobID>:<kind>:<locale>"
	// (mirrors domain/job/artifact_manifest.go::Artifact.ID).
	ArtifactID string `json:"artifact_id"`

	// State is the current position in the upload state machine.
	// Validated against UploadState.Valid() at every seam.
	State UploadState `json:"state"`
}

// NewUploadSession constructs an initialised UploadSession at the
// entry state (StateUploadPreparing). Refuses to construct with
// empty fields per godlike/07 no-fake-availability — a half-wired
// session must NOT come online because the caller forgot to hydrate.
func NewUploadSession(id, leaseID, artifactID string) (*UploadSession, error) {
	// Aggregate all missing fields into one diagnostic for the
	// operator — when 3 fields are missing simultaneously, listing
	// them all in a single error message is more actionable than 3
	// sequential errors (and matches the test invariant
	// TestUploadSession_NewUploadSession_DiagnosticClarity).
	var missing []string
	if id == "" {
		missing = append(missing, "id")
	}
	if leaseID == "" {
		missing = append(missing, "leaseID")
	}
	if artifactID == "" {
		missing = append(missing, "artifactID")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("new upload session: required fields missing (godlike/07 no-fake-availability): %s", strings.Join(missing, ", "))
	}
	return &UploadSession{
		ID:         id,
		LeaseID:    leaseID,
		ArtifactID: artifactID,
		State:      StateUploadPreparing,
	}, nil
}

// ── ArtifactUploader port (Pattern 0) ─────────────────────────────────

// ArtifactUploader is the typed port for the canonical 3-phase upload
// protocol. The Creator-side adapter implementation lives in
// internal/infrastructure/remote/creator/adapter.go.
//
// Wire mapping (single source of truth in
// internal/infrastructure/remote/jobbrokerclient/client.go):
//
//	Prepare(ctx, req)
//	        -> POST /internal/v1/jobs/<jobID>/uploads/prepare
//	           body: PrepareRequest; resp: UploadSession
//	Upload(ctx, session, fd)
//	        -> POST /internal/v1/jobs/<jobID>/uploads/<sid>/file?filename=<url-escaped>
//	           body: raw bytes (X-Filename + X-Idempotency-Key headers)
//	           resp: UploadSession (state=UPLOADING or UPLOADED on success)
//	Finalize(ctx, session)
//	        -> POST /internal/v1/jobs/<jobID>/uploads/<sid>/finalize
//	           body: FinalizeRequest; resp: UploadSession (state=FINALIZED on success)
//
// The Creator adapter threads ArtifactIdempotencyKey through every
// command so retries with the same (jobID, artifactID, sha256) triple
// collapse to the same remote-side dedup slot. The remote-side MUST
// reject a replayed key with a different (artifactID, sha256) with
// ErrArtifactIdempotencyKeyConflict (mirrors the Sender-side's
// godlike/06 SSOT pattern).
type ArtifactUploader interface {
	// Prepare initializes a new upload session on the remote-side.
	// Returns the typed UploadSession envelope (id + leaseID +
	// State = StateUploadPreparing). Refuses nil-receiver + empty
	// ctx.JobID per godlike/07 fail-closed.
	Prepare(ctx PrepareContext) (*UploadSession, error)

	// Upload streams the file content from localPath to the
	// remote-side, transitioning the session PREPARING → UPLOADING
	// → UPLOADED on the remote-side. Idempotent on retry via
	// X-Idempotency-Key header. localPath is the Creator-side
	// workspace path; the adapter opens + streams via os.Open +
	// io.Reader (no full-file materialisation).
	Upload(ctx PrepareContext, session UploadSession, localPath string) (*UploadSession, error)

	// Finalize closes the session and verifies the uploaded bytes
	// against the declared sha256 from Prepare. Transition path:
	// UPLOADED → VERIFIED → FINALIZED. After Finalize returns
	// StateUploadFinalized, the session is closed; further calls
	// against the same session are no-ops with self-loop acceptance.
	Finalize(ctx PrepareContext, session UploadSession) (*UploadSession, error)
}

// PrepareContext is the typed saga context for an upload — keeps
// jobID + leaseID + idempotency-key handy for every protocol
// command. Mirrors the prepare-shape requirement that every command
// carries the jobID in the URL path AND the client should pre-bind
// the lease_id + idem-key at Prepare-time so retries across the
// 3-step protocol share the same remote-side dedup slot.
//
// The PrepareContext.IdempotencyKey MUST be derived via
// ArtifactIdempotencyKey(jobID, artifactID, sha256) at the seam
// once all three fields are hydrated; the Creator adapter asserts
// this invariant in its constructor.
// godlike/06 SSOT: Ctx is the canonical threading point for the
// ambient job-ctx (cancellation + lease-loss + shutdown drain).
// The Creator adapter (internal/infrastructure/remote/creator/
// adapter.go) and the jobbrokerclient.Client (internal/infrastructure/
// remote/jobbrokerclient/client.go) both fail-closed with a typed
// error when Ctx is nil — silent-degrade to context.Background()
// would mask the exact wiring bug this field was added to fix
// (P1 #10 hardening, formerly C6 NIT-1 logged by code-reviewer
// and gated in scripts/ci-architectural-checks.sh).
type PrepareContext struct {
	// Ctx is the ambient context.Context that the Creator adapter
	// and the HTTP transport thread through every protocol call
	// (Prepare / Upload / Finalize). It owns the cancellation
	// semantics: when the worker is cancelled — job-cancel signal,
	// lease-loss, shutdown drain — the Ctx is cancelled and the
	// HTTP transport aborts the in-flight request instead of
	// continuing against context.Background() (P1 #10 hardening).
	//
	// godlike/07 no-fake-availability: Ctx MUST be non-nil. The
	// Creator adapter and the jobbrokerclient.Client both reject a
	// nil Ctx with a typed error at the seam. Callers that hold a
	// *job.Job or a long-lived jobCtx typically wrap it via
	// context.WithCancel / context.WithTimeout / context.WithDeadline
	// before assigning to this field; a plain context.Background()
	// is acceptable ONLY for one-shot test harnesses where
	// cancellation is intentionally inert.
	Ctx context.Context

	// JobID is the canonical job-broker JobID (matches lease_id
	// origin in domain/job/job.go).
	JobID string

	// LeaseID is the job-broker lease ID assigned at Claim().
	// Threaded into X-Lease-Id header on upload-file + finalize
	// commands for remote-side authorization.
	LeaseID string

	// ArtifactID is the canonical ArtifactManifest.Artifact.ID
	// (the Creator-side's identity for the file being uploaded).
	// Convention: "<jobID>:<kind>" or "<jobID>:<kind>:<locale>"
	// (mirrors domain/job/artifact_manifest.go::Artifact.ID).
	// Required: when empty, the canonical ArtifactIdempotencyKey
	// derivation returns the empty marker (godlike/07 no-fake-
	// availability) — the Adapter returns ErrArtifactIdempotencyKeyConflict.
	ArtifactID string

	// ArtifactKind is the canonical kind identifier
	// (one of ArtifactKind* constants in domain/job/artifact_manifest.go).
	// Carried on the prepare command so the remote-side can route
	// the upload slot to the correct catalog bucket.
	ArtifactKind string

	// Filename is the leaf name for the artifact when transferred
	// (post-upload sanitisation applied). Must be percent-escaped
	// by the adapter for URL-safety in the upload-file command's
	// ?filename= query parameter.
	Filename string

	// MIMEType is the IANA media type (e.g. "audio/mpeg", "image/png").
	MIMEType string

	// SizeBytes is the on-disk size before upload. Verified by the
	// adapter's os.Stat call; mismatch is logged but does NOT fail
	// (the canonical sha256 verification is the authoritative gate).
	SizeBytes int64

	// SHA256 is the hex-encoded content digest that the remote-side
	// will verify against during Finalize. Caller pre-computes this
	// (typically via job.ComputeSHA256 in domain/job/artifact_manifest.go).
	SHA256 string

	// IdempotencyKey is computed via ArtifactIdempotencyKey at the
	// seam once the PrepareContext is fully hydrated
	// (jobID + artifactID + sha256). Threads through X-Idempotency-Key
	// header on every command.
	IdempotencyKey string
}

// ── UploadSessionStore (typed read-only query port) ────────────────────

// UploadSessionStore is the typed read-only session query surface.
// Used by Sender-side consumers (or Creator-side diagnostics) who
// need to resolve a session by ID without going through the full
// 3-phase protocol.
type UploadSessionStore interface {
	// GetSession returns the UploadSession for the given sessionID.
	// Returns ErrArtifactSessionNotFound (typed sentinel, errors.Is
	// probe) when the session does not exist on the remote-side.
	GetSession(ctx PrepareContext, sessionID string) (*UploadSession, error)
}

// ── Typed sentinel errors (godlike/07 typed-error contract) ───────────

// ErrArtifactUploaderNotConfigured is the typed sentinel returned
// when the ArtifactUploader is constructed without a fully-wired
// Creator adapter (composition-time invariant broken) or when a
// method is called on a nil receiver. Callers errors.Is against
// this sentinel to distinguish "wiring bug" from "wire-shape bug".
//
// Scope: covers ONLY composition/wiring failures (nil receiver,
// nil broker, empty JobID). Cancellation failures (Ctx nil) and
// lease/session failures are gated by their own typed sentinels
// so callers can disambiguate via errors.Is.
var ErrArtifactUploaderNotConfigured = errors.New("artifact uploader: not configured")

// ErrArtifactSessionExpired is the typed sentinel for session reuse
// past the lease window. Callers errors.Is against this sentinel
// to distinguish "retry with new session" from "session corrupted".
var ErrArtifactSessionExpired = errors.New("artifact uploader: session expired")

// ErrArtifactSessionNotFound is the typed sentinel for GetSession
// lookups that miss. Callers errors.Is against this sentinel.
var ErrArtifactSessionNotFound = errors.New("artifact uploader: session not found")

// ErrArtifactRemoteSchemaVersionUnsupported is the typed sentinel
// for remote-side schema_version mismatches. Mirrors
// ErrRemoteSchemaVersionUnsupported in domain/job/artifact_manifest.go
// for the Sender-side projection — both sentinels are part of the
// godlike/06 SSOT wire-shape contract.
var ErrArtifactRemoteSchemaVersionUnsupported = errors.New("artifact uploader: remote schema_version is not supported")

// ErrArtifactCtxRequired is the typed sentinel returned when
// PrepareContext.Ctx is nil at any of the 3 protocol commands
// (Prepare / Upload / Finalize) on either the Creator-side Adapter
// or the HTTP-side jobbrokerclient.Client. P1 #10 hardening
// (formerly C6 NIT-1 logged by code-reviewer).
//
// Forward-pointer (review nit): Check 52 in scripts/ci-architectural-checks.sh
// bans raw `.PrepareArtifactUpload(` / `.UploadArtifactFile(` / `.FinalizeArtifactUpload(`
// callers in application/ + api/. The companion Check-52-extension (rg-based CI gate in scripts/ci-architectural-checks.sh, not
// yet filed as a forward-pointer CI workitem) would ban raw-string
// `remote.PrepareContext{...Ctx: nil...}` literals the same way Check 51 bans
// `.Enqueue(<ctx>, "<literal>")` — the gating rule is "if a caller
// constructs PrepareContext without setting Ctx, fail-fast at the literal
// site rather than relying on the seam check". Owner: architecture,
// deadline: 2026-08-15.
//
// godlike/07 no-fake-availability: a nil Ctx would silently behave
// like context.Background() because net/http's NewRequestWithContext
// only enforces the supplied ctx, NOT a default. Such a silent
// degrade would let the upload continue running after a worker
// cancellation (job-cancel, lease-loss, shutdown drain) — the
// exact bug this sentinel was added to surface. Distinct from
// ErrArtifactUploaderNotConfigured (wiring bug) so callers can
// errors.Is(err, ErrArtifactCtxRequired) for cancellation-driven
// failures specifically (e.g., to log
// `zap.String("ctx_status", "missing")` separately from `wiring`).
var ErrArtifactCtxRequired = errors.New("artifact uploader: PrepareContext.Ctx required (godlike/07 no-fake-availability — silent-degrade to context.Background() is the P1 #10 bug)")

// ErrIllegalUploadStateTransition is the typed sentinel for
// IsValidTransition rejections. Callers errors.Is against the
// sentinel AND errors.As against *IllegalTransitionError to
// extract the {From, To} pair (godlike/07 dual-traversal pattern).
var ErrIllegalUploadStateTransition = errors.New("illegal upload state transition")

// IllegalTransitionError is the typed-error-data envelope that
// exposes From + To state at the seam so callers can route on the
// specific (from → to) pair. Constructed by
// NewIllegalTransitionError at the rejection site (the Adapter's
// transition-gate checks). Implements errors.Is compatibility
// with the canonical sentinel so callers can probe EITHER the
// sentinel (for catch-all logic) OR the typed-data struct (for
// pair-specific routing).
type IllegalTransitionError struct {
	From UploadState
	To   UploadState
}

// Error implements the error interface (godlike/07 typed-error
// contract). Includes both From and To for human-readable logs.
func (e *IllegalTransitionError) Error() string {
	return fmt.Sprintf("illegal upload state transition: %s -> %s", e.From, e.To)
}

// Is enables errors.Is(err, ErrIllegalUploadStateTransition) on
// chains wrapping *IllegalTransitionError. Per godlike/07, both
// the sentinel AND the typed-data error are reachable in a single
// probe path — callers can write:
//
//	if errors.Is(err, remote.ErrIllegalUploadStateTransition) {
//	    var ite *remote.IllegalTransitionError
//	    if errors.As(err, &ite) {
//	        log.Warn("transition rejected", zap.String("from", string(ite.From)), ...)
//	    }
//	}
func (e *IllegalTransitionError) Is(target error) bool {
	return target == ErrIllegalUploadStateTransition
}

// NewIllegalTransitionError is the canonical constructor for
// *IllegalTransitionError at rejection sites. Mirrors the
// fmt.Errorf+%w wrap pattern but exposes From + To so the
// canonical pattern above probes both surfaces.
func NewIllegalTransitionError(from, to UploadState) error {
	return &IllegalTransitionError{From: from, To: to}
}
