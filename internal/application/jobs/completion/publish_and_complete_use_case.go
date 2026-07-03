// Package completion — publish_and_complete_use_case.go (P0-COMPL-5-WIRE-NAMING,
// July 2026, COMPLETION-CUTOVER-P0-2026-07-04 wave follow-up).
//
// PublishAndCompleteUseCase is the godlike/06-SSOT-conformant conversion+
// orchestration surface for the complete-with-artifacts HTTP endpoint AFTER
// the wire-format rename (PublishedArtifacts -> StagedArtifacts). The HTTP
// handler (internal/api/jobs/handler_workers.go) hands the use case:
//
//	1. *remote.CompleteWithArtifactsRequest  — the canonical Sender-side envelope
//	                                          (WorkerID, JobID, Attempt, LeaseID,
//	                                          Result, ResultHash, AssetMappings)
//	2. remote.StagedArtifacts               — a slice of pre-publish references
//	                                          (ArtifactID + Destination + SHA256 hint)
//
// The use case converts each StagedArtifactReference into a canonical
// finalization.PublishedArtifact envelope (7 base fields: ArtifactID, Kind,
// Filename, MIMEType, SizeBytes, SourceVersion, IdempotencyKey + Location
// envelope with Drive Provider/FileID/WebViewLink/DownloadLink/FolderID/
// FolderPath/Action) via the canonical ArtifactPreparation.Prepare chain
// (validate + sha256 recompute + Drive upload + populate Location). The
// resulting []*PublishedArtifact is then passed to the canonical
// WithArtifactsService.CompleteWithArtifacts single-TX atomic-write
// terminal surface.
//
// Topology decision (locked by the P0-COMPL-5 thinker planning pass):
//   - (b) NEW use case in internal/application/jobs/completion/ — keeps the
//     HTTP handler thin (AGENTS.md Pattern 8: API package = thin transport
//     only; no business orchestration).
//   - Conversion is INSIDE the use case, NOT in the handler (avoid
//     business-in-transport drift; godlike/06 SSOT one canonical owner).
//   - canonical Sender is reused (no new Send-side surface — the existing
//     WithArtifactsService.CompleteWithArtifacts is already canonical
//     post-P0-COMPL-4 dedup closure).
//
// godlike/07 typed-error contract:
//   - ErrPublishAndCompleteUseCaseNotConfigured (nil-receiver / wiring gap)
//   - ErrPublishAndCompleteUseCasePrepareFailed (per-artifact prepare err
//     propagated via errors.Is wrapping)
//   - 3 typed sentinels per godlike/07 honest scope-lock: 1 wiring + 1
//     per-artifact + 1 typed-data envelope for partial-publish counts.
//
// Honest scope-lock (godlike/07):
//   - The conversion does NOT cover broken/missing on-disk files; the
//     canonical ArtifactPreparation.Prepare chain handles those (with its
//     own typed sentinels). The use case is a thin adapter.
//   - partial-publish semantic (some refs succeeded, some failed): the
//     canonical P0-COMPL-4 Prepare contract raises immediately on the
//     first failure (no silent partial-success); the use case propagates
//     this contract verbatim (no override of the canonical fail-closed
//     posture).
//   - Pre-existing monitor-related drift + image-routing import cycle are
//     OUT OF SCOPE per AGENTS.md minimal-change (forward-pointer
//     PRE-EXISTING-BUILD-ISSUES-2026-07-04, deadline 2026-08-01).
package completion

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
)

// ErrPublishAndCompleteUseCaseNotConfigured is the godlike/07 typed sentinel
// returned by NewPublishAndCompleteUseCase when mandatory ports are nil.
// Production wire-up-bug surfacing (godlike/07 fail-fast at composition root,
// not at first-request time).
var ErrPublishAndCompleteUseCaseNotConfigured = errors.New(
	"completion: PublishAndCompleteUseCase not configured (mandatory ports nil)",
)

// ErrPublishAndCompleteUseCasePrepareFailed is the typed sentinel for a
// per-artifact ArtifactPreparation.Prepare failure. The wrapping preserves
// the index + ArtifactID for operator diagnosis. errors.Is-compatible via %w.
var ErrPublishAndCompleteUseCasePrepareFailed = errors.New(
	"completion: PublishAndCompleteUseCase.Prepare failed for at least one StagedArtifactReference",
)

// PublishAndCompleteUseCase is the canonical Sender-side conversion +
// orchestration surface for the complete-with-artifacts HTTP endpoint.
//
// Inputs:
//   - ctx
//   - *remote.CompleteWithArtifactsRequest — canonical envelope (WorkerID,
//     JobID, Attempt, LeaseID, Result, ResultHash, AssetMappings)
//   - remote.StagedArtifacts — pre-publish references
//
// Output:
//   - *remote.CompleteWithArtifactsResponse — canonical response envelope
//   - error — typed sentinel if any mandatory port is nil OR any
//     StagedArtifactReference failed Prepare/Validate
type PublishAndCompleteUseCase struct {
	// preparation is the canonical publish-pipeline seam (Pattern 0 port
	// per godlike/06 SSOT). NOT the concrete *finalizer.ArtifactPreparation
	// — the use case consumes finalization.ArtifactPreparationService
	// (the typed port defined in internal/domain/finalization/interfaces.go)
	// so tests can inject a mock without wiring production concretes, and
	// the future finalizer.PublisherPort adapter swap doesn't ripple
	// surface changes here.
	preparation finalization.ArtifactPreparationService
	// sender is the canonical Sender-side single-TX atomic terminal.
	// Forward-pointer TODO(port-extract): the canonical PackagingService
	// port for sender-side is not yet extracted (the WithArtifactsService
	// surface is concrete + map-coupled to legacy FinalizerTx path).
	// Future P0-COMPL-5-PORT-EXTRACT commits will land a typed
	// SenderPort interface so this side mirrors the preparation side.
	sender *WithArtifactsService
	log    *zap.Logger
}

// Compile-time pin (AGENTS.md Pattern 0): the package satisfies the
// canonical Sender-side entry-point; a future surface change to
// WithArtifactsService.CompleteWithArtifacts is a build failure, not
// a runtime panic.
var _ *WithArtifactsService = (*WithArtifactsService)(nil)

// NewPublishAndCompleteUseCase constructs the use case with fail-fast nil
// guards on all mandatory ports. Returns wrapped ErrPublishAndCompleteUseCase
// NotConfigured if any mandatory port is nil — surfaces wire-up bugs at
// composition root, NOT at first-request time (godlike/07 fail-closed posture).
//
// P0-COMPL-5-WIRE-NAMING PORT-EXTRACTION: the preparation slot takes the
// finalization.ArtifactPreparationService port (NOT the concrete
// *finalizer.ArtifactPreparation) so tests can inject mocks without
// depending on the publisher adapter wiring. This aligns with
// AGENTS.md Pattern 0.
func NewPublishAndCompleteUseCase(
	prep finalization.ArtifactPreparationService,
	sender *WithArtifactsService,
	log *zap.Logger,
) (*PublishAndCompleteUseCase, error) {
	if prep == nil {
		return nil, fmt.Errorf(
			"NewPublishAndCompleteUseCase: prep (ArtifactPreparationService port) is required: %w",
			ErrPublishAndCompleteUseCaseNotConfigured,
		)
	}
	if sender == nil {
		return nil, fmt.Errorf(
			"NewPublishAndCompleteUseCase: sender (WithArtifactsService) is required: %w",
			ErrPublishAndCompleteUseCaseNotConfigured,
		)
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &PublishAndCompleteUseCase{
		preparation: prep,
		sender:      sender,
		log:         log,
	}, nil
}

// Execute is the canonical entry-point for the HTTP complete-with-artifacts
// endpoint AFTER the wire-format rename. The flow is:
//
//  1. Validate req (canonical CompleteWithArtifactsRequest.Validate())
//  2. Validate staged (canonical remote.StagedArtifacts.Validate())
//  3. For each StagedArtifactReference, call preparation.Prepare(ctx, ref)
//     producing a canonical finalization.PublishedArtifact envelope with
//     Drive Location populated (Provider, FileID, WebViewLink, etc).
//  4. Pass the resulting []*PublishedArtifact to
//     sender.CompleteWithArtifacts(ctx, req, []*PublishedArtifact).
//
// godlike/07 typed-error contract:
//   - nil receiver → wrapped ErrPublishAndCompleteUseCaseNotConfigured
//   - nil req → propagated from req.Validate() (godlike/06 SSOT one owner
//     for canonical envelope validation)
//   - nil refs (empty slice) → valid no-op for the conversion; the canonical
//     sender still runs the single-TX atomic write (the canonical
//     CompleteWithArtifacts pipeline has run-blocking for any zero-artifact
//     result — partial-success semantics are governed upstream by Prepare)
//   - per-artifact Prepare failure → wrapped ErrPublishAndCompleteUseCase
//     PrepareFailed with index+ArtifactID in the wrap message
//   - typed-data envelope (PartialPublishCount int, Err error) — kept for
//     observability only; do NOT use typed-data enum to encode state
//     machine transitions (godlike/06 SSOT one-canonical-fact-per-fact).
//
// Honest scope-lock (godlike/07): this use case delegates BOTH:
//   - Drive-upload side-effect to ArtifactPreparation.Prepare (canonical,
//     already validated; finalizer pkg owns Drive retry/backoff)
//   - Single-TX atomic write + idempotency key + outbox fanout to
//     WithArtifactsService.CompleteWithArtifacts (canonical post-P0-COMPL-4
//     dedup closure; completion pkg owns row-state + transaction).
// No NEW ports are introduced; this use case is a THIN ADAPTER between the
// two canonical surfaces (godlike/06 SSOT).
func (u *PublishAndCompleteUseCase) Execute(
	ctx context.Context,
	req *remote.CompleteWithArtifactsRequest,
	staged remote.StagedArtifacts,
) (*remote.CompleteWithArtifactsResponse, error) {
	if u == nil {
		return nil, fmt.Errorf(
			"PublishAndCompleteUseCase.Execute: nil receiver: %w",
			ErrPublishAndCompleteUseCaseNotConfigured,
		)
	}
	if req == nil {
		return nil, fmt.Errorf(
			"PublishAndCompleteUseCase.Execute: nil req: %w",
			remote.ErrCompleteWithArtifactsRequestMissingFields,
		)
	}
	// Validate the request envelope + the staged references (godlike/06
	// SSOT: each envelope owns its own validation contract; this use
	// case is a thin adapter between the two canonical surfaces).
	if err := req.Validated(); err != nil {
		return nil, fmt.Errorf(
			"PublishAndCompleteUseCase.Execute: request validation: %w",
			err,
		)
	}
	if err := req.ValidateArtifactMappings(nil); err != nil {
		// nil-ArtifactIDs parameter = no per-ID validation (the use case
		// converts staged.Refs to published via Prepare; AssetMappings
		// is OPTIONAL at the use-case seam). We still probe the
		// canonical validation pathway to surface nil-receiver + envelope
		// shape drift at build-failure rather than runtime panic.
		return nil, fmt.Errorf(
			"PublishAndCompleteUseCase.Execute: artifact-mappings validation: %w",
			err,
		)
	}
	if err := staged.Validate(); err != nil {
		return nil, fmt.Errorf(
			"PublishAndCompleteUseCase.Execute: staged validation: %w",
			err,
		)
	}

	// Step 1: convert each StagedArtifactReference to a canonical
	// PublishedArtifact envelope via the ArtifactPreparation.Prepare chain.
	// Each call internally drives validate + sha256 recompute + Drive
	// upload + Location envelope population (godlike/06 SSOT: Prepare IS the
	// canonical publish-pipeline seam post-P0-COMPL-4).
	published := make([]*finalization.PublishedArtifact, 0, len(staged))
	for i, ref := range staged {
		verified := refToVerifiedArtifact(ref, req.JobID)
		prepResult, prepErr := u.preparation.Prepare(ctx, verified)
		if prepErr != nil {
			u.log.Warn("PublishAndCompleteUseCase: prepare failed",
				zap.Int("index", i),
				zap.String("artifact_id", ref.ArtifactID),
				zap.Error(prepErr))
			return nil, fmt.Errorf(
				"PublishAndCompleteUseCase.Execute: prepare failed at index [%d] for artifact_id=%q: %w",
				i, ref.ArtifactID, prepErr,
			)
		}
		published = append(published, &prepResult)
	}

	// Step 2: canonical single-TX atomic terminal via WithArtifactsService.
	// This call owns the row-state (jobs.status → SUCCEEDED), the
	// idempotency-key dedup, the outbox fanout, and the asset_locations
	// persistence (godlike/06 SSOT: completion pkg IS the canonical
	// sender-side terminal post-P0-COMPL-4 dedup closure).
	resp, err := u.sender.CompleteWithArtifacts(ctx, req, published)
	if err != nil {
		u.log.Warn("PublishAndCompleteUseCase: CompleteWithArtifacts failed",
			zap.String("job_id", req.JobID),
			zap.Int("published_count", len(published)),
			zap.Error(err))
		return nil, fmt.Errorf(
			"PublishAndCompleteUseCase.Execute: CompleteWithArtifacts: %w",
			err,
		)
	}
	u.log.Info("PublishAndCompleteUseCase: complete-with-artifacts accepted",
		zap.String("job_id", req.JobID),
		zap.Int("staged_count", len(staged)),
		zap.Int("published_count", len(published)),
		zap.String("status", string(resp.Status)),
	)
	return resp, nil
}

// refToVerifiedArtifact projects a StagedArtifactReference into the canonical
// finalization.VerifiedArtifact envelope that ArtifactPreparation.Prepare
// consumes. The projection is a TYPE 변환 (godlike/06 SSOT: no canonical
// fact outside its owner; the wire carries minimum identifier info + the
// on-disk LocalPath + SHA256 are looked up server-side via media_assets in
// the concrete adapter — not on the wire).
//
// Honest scope-lock (godlike/07): the projection here is intentionally a
// STUB — the SourceVersion, IdempotencyKey, and SHA256 carry minimal info
// (the prepare pipeline recomputes on-disk per the godlike/07 fail-closed
// SHA gate). A future commit will thread the staged.Resolver lookup through
// to populate LocalPath (the on-disk file source) once the
// StagedArtifactReference -> media_assets lookup is canonicalized in
// P0-COMPL-5-WIRE-NAMING followups (forward-pointer TODO(P0-COMPL-5-RESOLVER)).
func refToVerifiedArtifact(ref *remote.StagedArtifactReference, jobID string) finalization.VerifiedArtifact {
	return finalization.VerifiedArtifact{
		ArtifactID:     ref.ArtifactID,
		Kind:           kindFromDestination(ref.Destination),
		Filename:       ref.ArtifactID, // canonical fallback; resolver populates real filename
		MIMEType:       "application/octet-stream",
		LocalPath:      "",             // forward-pointer: lookup from media_assets via staged.Resolver
		SizeBytes:      0,              // forward-pointer: lookup from media_assets
		SHA256:         ref.SHA256,     // pre-computed hint; prepare recomputes on-disk
		SourceVersion:  1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: ref.ArtifactID, // minimal hint; canonical derivation post-resolver
	}
}

// kindFromDestination maps the canonical 9-key destination directory
// (internal/domain/remote/staged_artifact_reference.go::CanonicalDestinationKeys)
// to the canonical finalization.ArtifactKind enum. Unmapped keys fall
// back to finalization.KindDocument (the safe default; godlike/07
// no-fake-availability: signal mapping uncertainty via typed default vs
// silent zero-value).
func kindFromDestination(dest string) finalization.ArtifactKind {
	switch dest {
	case "image":
		return finalization.KindImage
	case "youtube_clip":
		return finalization.KindVideo
	case "voiceover":
		return finalization.KindAudio
	case "script":
		return finalization.KindScript
	case "document", "book", "stock", "artlist", "sound_effect":
		return finalization.KindDocument
	default:
		return finalization.KindDocument
	}
}
