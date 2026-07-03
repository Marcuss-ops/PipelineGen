// Package creator implements the Creator-side ArtifactUploader port
// (P0 Commit 6, July 2026). The Creator adapter is the typed
// validation layer between domain/remote.ArtifactUploader consumers
// and the concrete HTTP transport in internal/infrastructure/remote/
// jobbrokerclient.
//
// Architecture (per AGENTS.md Pattern 0 + Pattern 8 — thin transport +
// port abstraction):
//   - domain/remote.ArtifactUploader — canonical port interface
//   - creator.Adapter — typed validation + state-machine transition
//     gates (this file)
//   - jobbrokerclient.Client — concrete HTTP transport + file IO
//
// The Adapter is THIN: it composes the 3 protocol commands
// (PrepareArtifactUpload / UploadArtifactFile / FinalizeArtifactUpload)
// with state-machine transition gates + godlike/07 typed-error
// wrapping. File streaming IO lives in the HTTP client (mirrors the
// assettransferclient.Client.UploadFile precedent).
package creator

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	jobbrokerclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/jobbrokerclient"
)

// jobBrokerClient is the subset of *jobbrokerclient.Client methods
// the Creator adapter calls. Defined as a private interface so tests
// can inject a stub without running a real HTTP server (mirrors the
// Pattern 0 compile-time pinning pattern).
//
// Production: *jobbrokerclient.Client satisfies jobBrokerClient.
// Test: stub implementations satisfy the same interface, and the
// New constructor accepts the interface (not the concrete) for
// dependency-injection test convenience.
//
// godlike/06 SSOT: the parameter on all three methods is named
// `prepareCtx remote.PrepareContext` (NOT `ctx`) — naming it `ctx`
// would shadow the `context.Context` field on the struct and
// obscure the read site where the canonical threading happens
// (prepareCtx.Ctx). P1 #10 hardening.
type jobBrokerClient interface {
	PrepareArtifactUpload(prepareCtx remote.PrepareContext) (*remote.UploadSession, error)
	UploadArtifactFile(prepareCtx remote.PrepareContext, sessionID, localPath, idempotencyKey string) (*remote.UploadSession, error)
	FinalizeArtifactUpload(prepareCtx remote.PrepareContext, sessionID, sha256Hex, idempotencyKey string) (*remote.UploadSession, error)
}

// Compile-time pin: *jobbrokerclient.Client satisfies jobBrokerClient.
// Future drift in Client's signature is a build failure at this file.
var _ jobBrokerClient = (*jobbrokerclient.Client)(nil)

// Adapter is the Creator-side ArtifactUploader implementation.
// Owns the state-machine transition gates + typed-error wrapping.
// Streams from localPath via the underlying HTTP client's
// UploadArtifactFile method (which mirrors assettransferclient.Client.UploadFile).
type Adapter struct {
	broker jobBrokerClient
	logger *zap.Logger
}

// New constructs a Creator-side Adapter. Refuses nil broker per
// godlike/07 no-fake-availability; defaults nil logger to no-op
// (test-friendly fallback, mirrors critical_handler_validator.go).
func New(broker *jobbrokerclient.Client, logger *zap.Logger) (*Adapter, error) {
	if broker == nil {
		return nil, fmt.Errorf("creator adapter: jobbrokerclient.Client is nil: %w",
			remote.ErrArtifactUploaderNotConfigured)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Adapter{broker: broker, logger: logger}, nil
}

// newWithClient is the test-friendly constructor that accepts the
// private interface (NOT the concrete). Production code MUST use New.
// This split mirrors AGENTS.md Pattern 0's compile-time-only surface.
func newWithClient(broker jobBrokerClient, logger *zap.Logger) *Adapter {
	if broker == nil {
		// Test-side: return nil-receiver adapter for nil-receiver
		// path tests (mirrors production's nil-receiver reject).
		return &Adapter{broker: nil, logger: logger}
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Adapter{broker: broker, logger: logger}
}

// Compile-time pin: Adapter implements remote.ArtifactUploader.
// Future drift in remote.ArtifactUploader's signature is a build
// failure at this file (per AGENTS.md Pattern 0 contract).
var _ remote.ArtifactUploader = (*Adapter)(nil)

// Prepare initializes the upload session on the remote-side.
// Returns the typed UploadSession envelope (id + leaseID +
// State = StateUploadPreparing). Validates state-machine invariant
// post-call (server MUST return PREPARING; any other state is a
// typed transition error per godlike/07).
func (a *Adapter) Prepare(prepareCtx remote.PrepareContext) (*remote.UploadSession, error) {
	if a == nil || a.broker == nil {
		return nil, fmt.Errorf("creator adapter: not configured (nil receiver or broker): %w",
			remote.ErrArtifactUploaderNotConfigured)
	}
	// godlike/07 fail-closed: PrepareContext.Ctx MUST be non-nil.
	// The HTTP transport dereferences prepareCtx.Ctx to thread
	// cancellation through to net/http.NewRequestWithContext; a nil
	// Ctx would silently behave like context.Background(), which is
	// the exact wiring bug P1 #10 was added to prevent (former C6
	// NIT-1). Reject at the Adapter seam BEFORE any state-machine
	// gate so the error is unambiguous (no broker call attempted).
	// Distinct sentinel ErrArtifactCtxRequired (vs
	// ErrArtifactUploaderNotConfigured) lets callers disambiguate
	// cancellation-missing from wiring-missing via errors.Is.
	if prepareCtx.Ctx == nil {
		return nil, fmt.Errorf("creator adapter: Prepare: %w", remote.ErrArtifactCtxRequired)
	}
	if prepareCtx.JobID == "" {
		return nil, fmt.Errorf("creator adapter: PrepareContext.JobID required: %w",
			remote.ErrArtifactUploaderNotConfigured)
	}
	// Compute idempotency key at the seam once PrepareContext's
	// required fields are hydrated. The contract: key MUST be
	// derived from (JobID, ArtifactID, SHA256) via the canonical
	// helper so retries are byte-stable.
	derivedKey := remote.ArtifactIdempotencyKey(prepareCtx.JobID, prepareCtx.ArtifactID, prepareCtx.SHA256)
	if derivedKey == "" {
		return nil, fmt.Errorf("creator adapter: idempotency-key derivation returned empty (godlike/07 no-fake-availability): %w",
			remote.ErrArtifactIdempotencyKeyConflict)
	}
	prepareCtx.IdempotencyKey = derivedKey

	session, err := a.broker.PrepareArtifactUpload(prepareCtx)
	if err != nil {
		return nil, fmt.Errorf("creator adapter: prepare: %w", err)
	}
	// Validate state-machine invariant: server MUST return PREPARING.
	// Any other state is a typed IllegalTransitionError so callers
	// can probe + log the (from, to) pair.
	if session.State != remote.StateUploadPreparing {
		return nil, fmt.Errorf("creator adapter: prepare returned state %q (want %q): %w",
			session.State, remote.StateUploadPreparing,
			remote.NewIllegalTransitionError(remote.StateUploadPreparing, session.State))
	}
	return session, nil
}

// Upload streams the file content from localPath to the remote-side,
// transitioning the session PREPARING → UPLOADING → UPLOADED on the
// remote-side. Idempotent on retry via X-Idempotency-Key header.
// State-machine gate: session must be at PREPARING (or UPLOADING
// for a retry) before invoking upload-file.
func (a *Adapter) Upload(prepareCtx remote.PrepareContext, session remote.UploadSession, localPath string) (*remote.UploadSession, error) {
	if a == nil || a.broker == nil {
		return nil, fmt.Errorf("creator adapter: not configured: %w",
			remote.ErrArtifactUploaderNotConfigured)
	}
	// godlike/07 fail-closed: PrepareContext.Ctx MUST be non-nil
	// (mirrors Prepare seam — see Prepare() comment for the rationale).
	if prepareCtx.Ctx == nil {
		return nil, fmt.Errorf("creator adapter: Upload: %w", remote.ErrArtifactCtxRequired)
	}
	if session.ID == "" {
		return nil, remote.ErrArtifactSessionExpired
	}
	if localPath == "" {
		return nil, fmt.Errorf("creator adapter: localPath required (godlike/07 no-fake-availability)")
	}
	// Pre-call state-machine gate: server-side requires session at
	// PREPARING (or UPLOADING for a retry). Reject anything else
	// with a typed IllegalTransitionError.
	if !session.State.IsValidTransition(remote.StateUploadUploading) {
		// Note: FAILED + FINALIZED are sticky-terminal in transition
		// gate too — failing here mirrors the server-side rejection.
		// The exception is FAILED itself, which has its own sentinel
		// rejection pattern.
		if session.State == remote.StateUploadFailed {
			return nil, remote.NewIllegalTransitionError(session.State, remote.StateUploadUploading)
		}
		return nil, remote.NewIllegalTransitionError(session.State, remote.StateUploadUploading)
	}
	// Defensive idempotency-key derivation: if prepareCtx.IdempotencyKey
	// wasn't pre-hydrated by a prior Prepare() call (retry path that
	// bypasses Prepare), derive it from the canonical
	// (JobID, ArtifactID, SHA256) triple. Byte-stable across all 3
	// protocol calls because ArtifactIdempotencyKey is deterministic.
	idemKey := prepareCtx.IdempotencyKey
	if idemKey == "" {
		derived := remote.ArtifactIdempotencyKey(prepareCtx.JobID, prepareCtx.ArtifactID, prepareCtx.SHA256)
		if derived == "" {
			return nil, fmt.Errorf("creator adapter: idempotency-key derivation returned empty (godlike/07 no-fake-availability): %w",
				remote.ErrArtifactIdempotencyKeyConflict)
		}
		idemKey = derived
	}
	updatedSession, err := a.broker.UploadArtifactFile(prepareCtx, session.ID, localPath, idemKey)
	if err != nil {
		// Any upload-file failure transitions session to FAILED on
		// the remote-side — surface the error to the caller so they
		// can decide retry-or-invent-new-session per godlike/07.
		a.logger.Warn("creator adapter: upload-file failed (server will transition to FAILED)",
			zap.String("session_id", session.ID),
			zap.String("local_path", localPath),
			zap.Error(err),
		)
		return nil, fmt.Errorf("creator adapter: upload-file: %w", err)
	}
	// Validate state-machine invariant: server MUST return UPLOADING
	// (mid-call progress) or UPLOADED (post-call completion) on success.
	// Any other state is a typed IllegalTransitionError.
	if updatedSession.State != remote.StateUploadUploading && updatedSession.State != remote.StateUploadUploaded {
		return nil, fmt.Errorf("creator adapter: upload-file returned state %q (want UPLOADING or UPLOADED): %w",
			updatedSession.State,
			remote.NewIllegalTransitionError(remote.StateUploadUploading, updatedSession.State))
	}
	return updatedSession, nil
}

// Finalize closes the session and verifies the uploaded bytes against
// the declared sha256 from Prepare. Transition path: UPLOADED →
// VERIFIED → FINALIZED. The remote-side's finalize command atomically
// verifies + transitions to VERIFIED + FINALIZED in one call; the
// Creator adapter's IsValidTransition(VERIFIED → FINALIZED) gate runs
// only on subsequent invocations (self-loop-skipped on first call).
//
// After Finalize returns StateUploadFinalized, the session is closed;
// further calls are no-ops with self-loop acceptance.
func (a *Adapter) Finalize(prepareCtx remote.PrepareContext, session remote.UploadSession) (*remote.UploadSession, error) {
	if a == nil || a.broker == nil {
		return nil, fmt.Errorf("creator adapter: not configured: %w",
			remote.ErrArtifactUploaderNotConfigured)
	}
	// godlike/07 fail-closed: PrepareContext.Ctx MUST be non-nil
	// (mirrors Prepare + Upload seams — see those comments for the
	// rationale).
	if prepareCtx.Ctx == nil {
		return nil, fmt.Errorf("creator adapter: Finalize: %w", remote.ErrArtifactCtxRequired)
	}
	if session.ID == "" {
		return nil, remote.ErrArtifactSessionExpired
	}
	// Pre-call state-machine gate: server-side requires session at
	// UPLOADED (or VERIFIED for a retry) before invoking finalize.
	if !session.State.IsValidTransition(remote.StateUploadVerified) {
		return nil, remote.NewIllegalTransitionError(session.State, remote.StateUploadVerified)
	}
	// Defensive idempotency-key derivation (same rationale as Upload).
	idemKey := prepareCtx.IdempotencyKey
	if idemKey == "" {
		derived := remote.ArtifactIdempotencyKey(prepareCtx.JobID, prepareCtx.ArtifactID, prepareCtx.SHA256)
		if derived == "" {
			return nil, fmt.Errorf("creator adapter: idempotency-key derivation returned empty (godlike/07 no-fake-availability): %w",
				remote.ErrArtifactIdempotencyKeyConflict)
		}
		idemKey = derived
	}
	// Pass SHA256 + Idempotency-Key to the server-side verify step.
	updatedSession, err := a.broker.FinalizeArtifactUpload(prepareCtx, session.ID, prepareCtx.SHA256, idemKey)
	if err != nil {
		// Finalize-side failures typically surface as ErrArtifactRemoteSchemaVersionUnsupported
		// (sha256 mismatch) OR ErrArtifactSessionExpired (concurrent
		// finalize on the same session). Thread the error verbatim
		// per godlike/07 typed-error contract.
		return nil, fmt.Errorf("creator adapter: upload-finalize: %w", err)
	}
	// Validate state-machine invariant: server MUST return VERIFIED
	// (mid-call progress) or FINALIZED (post-call completion).
	if updatedSession.State != remote.StateUploadVerified && updatedSession.State != remote.StateUploadFinalized {
		return nil, fmt.Errorf("creator adapter: upload-finalize returned state %q (want VERIFIED or FINALIZED): %w",
			updatedSession.State,
			remote.NewIllegalTransitionError(remote.StateUploadVerified, updatedSession.State))
	}
	return updatedSession, nil
}
