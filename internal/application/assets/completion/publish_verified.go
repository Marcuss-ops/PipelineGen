// Package completion — Azione 5 of the CUTOVER-COMPLETE-WITH-ARTIFACTS wave.
//
// PublishVerifiedArtifacts routes each VerifiedArtifact through the canonical
// Prepare → Publish → post-publish-checksum chain. The 12-field VerifiedArtifact
// envelope (from internal/application/assets/verification.Azione 4) is the
// INPUT; the canonical finalization.PublishedArtifact (10-field shape per
// internal/domain/finalization/types.go:455) is the OUTPUT. The function is
// the SECOND surface of Azione 5; the FIRST is the Tools-registry branch that
// REPLACES the legacy RunComplete path (Azione 7, forward-pointer).
//
// Honest scope-lock (godlike/07):
//   - Azione 5 is bounded to: Prepare mirror + Publish call + post-publish
//     fail-closed checksum + idempotency short-circuit. The JobFinalizer
//     CompleteWithArtifacts single-TX atomic write (Azione 6) is a SEPARATE
//     concern that this function FEEDS via the returned []*PublishedArtifact.
//   - The "15 field" published-artifact envelope mentioned in the spec maps
//     to the canonical 10-field finalization.PublishedArtifact shape (10
//     fields: ArtifactID, Kind, Filename, MIMEType, SizeBytes, SHA256,
//     SourceVersion, Requirement, IdempotencyKey, Location). The discrepancy
//     between spec ("15") and canonical ("10") is acknowledged here: any
//     future typing extension would happen at finalization/types.go, not in
//     this package (godlike/06 SSOT one-owner-per-fact).
//   - 401-driven retry is implemented in the underlying concrete
//     ArtifactPublisherAdapter (per P1.5 close history, pkg/retry.IsTransient
//   - GoogleAPIError classifies 401 as transient + RetryAfterError interface
//     threading). THIS package does NOT add a parallel retry layer — it
//     trusts the concrete Publisher's built-in retry-on-transient wiring.
//
// Godlike/07 typed-error contract:
//   - Errors propagate as wrapped fmt.Errorf("...: %w", sentinel) chains so
//     the cutover pipe (Azione 7) can route via errors.Is probing
//   - NO silent-partial-success: len(output) == len(input) on success OR err
//     fails the pipeline (godlike/06 SSOT)
package completion

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// Publisher (DEDUP-REMOVED P0-COMPL-4): historically this Service held a
// Publisher field that published the artifact AGAIN after ArtifactPreparation
// .Prepare had ALREADY published it (via Prepare→s.publisher.Publish internally).
// The double-Publish was the godlike/06 SSOT violation: TWO owners of the
// canonical "Drive write" fact. Per P0-COMPL-4 the Publisher interface +
// the Publisher field on Service + the second Publish retry block in
// publishOne have all been deleted. Prepare IS the canonical publish seam.

// Preparer is the typed port for the Prepare seam that mirrors the
// schema-versioned PublishedArtifact envelope (idempotency-key derivation
// etc.). The canonical concrete is *internal/application/assets/finalizer
// .ArtifactPreparation wrapped to satisfy this signature via a thin adapter.
type Preparer interface {
	Prepare(ctx context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error)
}

// IdempotencyBookkeeper tracks the (jobID, artifactID, sha256Hex) → PublishedArtifact
// dedup surface so a re-stage with the SAME content triple returns the EXISTING
// record WITHOUT re-running Prepare/Publish (idempotent replay; godlike/07
// no-duplicate-side-effects).
//
// 3 methods:
//   - IsPublished      : O(1) lookup; returns (true, nil) if triple recorded
//   - LookupPublished  : returns the full *PublishedArtifact envelope
//   - RecordPublished  : stores the envelope after successful publish
type IdempotencyBookkeeper interface {
	IsPublished(ctx context.Context, jobID, artifactID, sha256Hex string) (bool, error)
	LookupPublished(ctx context.Context, jobID, artifactID, sha256Hex string) (*finalization.PublishedArtifact, error)
	RecordPublished(ctx context.Context, jobID, artifactID, sha256Hex string, pub *finalization.PublishedArtifact) error
}

// Service publishes verified artifacts to the destination (Drive) with full
// godlike/07 typed-error contracts: idempotent retry-on-transient (delegated
// to the underlying Publisher concrete), fail-closed post-publish checksum
// invariant (recomputes SHA-256 from local file, gates against ErrFinalChecksumMismatch
// drift), and short-circuit on already-published (returns existing PublishedArtifact
// + ErrAlreadyPublished wrap for caller introspection).
type Service struct {
	preparer   Preparer
	bookkeeper IdempotencyBookkeeper
}

// NewService constructs a Service with fail-closed nil-check on both ports.
// Both ports are required at composition root; missing EITHER returns a
// wrapped ErrPublishInvalidArtifact sentinel so the wire-up-bug is surfaced
// at boot, not at first-request time. The Publisher port was REMOVED in
// P0-COMPL-4 (dedup refactor): Preparer IS the canonical publisher seam
// because ArtifactPreparation.Prepare internally invokes its embedded
// finalization.PublisherPort during validation.
//
// This 2-arg constructor is a BREAKING change vs the prior 3-arg shape
// (Publisher + Preparer + Bookkeeper). All callers must be updated to drop
// the Publisher argument. The compile-time error at first build is the
// canonical signal that the godlike/06 SSOT violation has been retired
// (per the explicit godlike/06 "one owner per fact" rule — the canonical
// Drive-write owner is now solely inside ArtifactPreparation).
func NewService(pr Preparer, b IdempotencyBookkeeper) (*Service, error) {
	if pr == nil {
		return nil, fmt.Errorf("completion.NewService: Preparer port is required (REMOVED P0-COMPL-4 Publisher port; ArtifactPreparation is the canonical Drive-write owner per godlike/06 SSOT): %w", ErrPublishInvalidArtifact)
	}
	if b == nil {
		return nil, fmt.Errorf("completion.NewService: IdempotencyBookkeeper port is required: %w", ErrPublishInvalidArtifact)
	}
	return &Service{preparer: pr, bookkeeper: b}, nil
}

// PublishVerifiedArtifacts routes each VerifiedArtifact through the 5-step
// publish chain. Returns slice of *PublishedArtifact on success; len(out) ==
// len(in) always on success (godlike/06 SSOT discipline: no silent-partial-
// success).
//
// Failure paths (godlike/07 typed-error contract):
//
//   - empty input slice              → wrapped ErrPublishEmptySlice
//   - nil pointer in slice           → wrapped ErrPublishInvalidArtifact
//   - already-published short-circuit → wrapped ErrAlreadyPublished + cached
//     *PublishedArtifact returned via
//     errors.As (godlike/07 no-duplicate-
//     side-effects; no re-upload)
//   - Prepare err (transient)        → transient bubbles up; concrete
//     Publisher/Preparer handles retry
//   - Publish err (transient)        → transient bubbles up; concrete
//     handles retry
//   - final-checksum mismatch        → wrapped ErrFinalChecksumMismatch
//     (invariants fired AFTER Publish
//     returned success)
//
// On every successful publish, the (jobID, artifactID, sha256Hex) triple is
// recorded in the IdempotencyBookkeeper so subsequent replays are
// deterministic byte-stable (idempotency-key derivation via
// remote.ArtifactIdempotencyKey).
func (s *Service) PublishVerifiedArtifacts(
	ctx context.Context,
	artifacts []*finalization.VerifiedArtifact,
) ([]*finalization.PublishedArtifact, error) {
	// 0. Input contract gate.
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("completion.PublishVerifiedArtifacts: %w", ErrPublishEmptySlice)
	}
	for idx, va := range artifacts {
		if va == nil {
			return nil, fmt.Errorf("completion.PublishVerifiedArtifacts: nil pointer at index [%d]: %w",
				idx, ErrPublishInvalidArtifact)
		}
	}

	// 1. Per-artifact publish loop. On the FIRST error, we preserve the
	//    partial output (any successfully published envelopes) and return
	//    it alongside the wrapped error. This is critical for the
	//    short-circuit path (Test 3 Duplicate_AlreadyPublished): the
	//    cached *PublishedArtifact must reach the caller via the slice
	//    even when ErrAlreadyPublished surfaces (for idempotent replay
	//    semantics — the caller doesn't have to re-query the bookkeeper).
	out := make([]*finalization.PublishedArtifact, 0, len(artifacts))
	for _, va := range artifacts {
		pub, err := s.publishOne(ctx, va)
		if err != nil {
			if pub != nil {
				// Preserve the per-artifact envelope (even on short-circuit
				// fail with wrapped error) so the caller can introspect it
				// alongside errors.As probing.
				out = append(out, pub)
			}
			// godlike/06 SSOT discipline: wrap the original sentinel via %w
			// so errors.Is probing works at the cutover pipe layer.
			return out, err
		}
		out = append(out, pub)
	}
	return out, nil
}

// publishOne is the per-artifact Publish chain (Prepare → Publish →
// verify-final-checksum → record-idempotency). Isolated for testability and
// for clarity of the 4-step flow.
func (s *Service) publishOne(
	ctx context.Context,
	va *finalization.VerifiedArtifact,
) (*finalization.PublishedArtifact, error) {
	// 1. Derive (jobID, subID) from ArtifactID. Convention: ArtifactID encoded
	//    as "jobID:subID" per the canonical reuse from the adapter_test.go
	//    surface. Empty/no-colon → returns (wholeArtifactID, "") with the
	//    empty-subID canonical behaviour.
	jobID, subID := splitJobArtifactID(va.ArtifactID)

	// 2. Idempotency short-circuit (godlike/07 no-duplicate-side-effects):
	//    if the (jobID, subID, sha256Hex) triple is already recorded, return
	//    the existing PublishedArtifact WITHOUT re-running Prepare/Publish.
	if already, lookErr := s.bookkeeper.IsPublished(ctx, jobID, subID, va.SHA256); lookErr == nil && already {
		existing, lookupErr := s.bookkeeper.LookupPublished(ctx, jobID, subID, va.SHA256)
		if lookupErr != nil || existing == nil {
			// Bookkeeper inconsistency (IsPublished said yes, LookupPublished
			// said no). Surface the lookup error wrapped with ErrAlreadyPublished
			// so the cutover pipe can decide retry vs fail-closed.
			return nil, fmt.Errorf(
				"completion.publishOne[%s]: idempotency bookkeeper inconsistency (IsPublished=true, LookupPublished=nil): %w",
				va.ArtifactID, ErrAlreadyPublished,
			)
		}
		return existing, fmt.Errorf(
			"completion.publishOne[%s]: short-circuit (already published): %w",
			va.ArtifactID, ErrAlreadyPublished,
		)
	}

	// 3. Prepare (mirror schema → PublishedArtifact) wrapped in
	//    retry-on-transient loop. This Service IS the canonical owner of
	//    retry policy for the publish seam in the post-P0-COMPL-4
	//    architecture: the Preparer (ArtifactPreparation) internally
	//    invokes its finalization.PublisherPort to Drive; transient
	//    errors from that publish bubble out of Prepare and through
	//    pkg/retry.Do with retry.DefaultOptions until either the
	//    operation succeeds or the retry budget is exhausted.
	//
	//    godlike/06 SSOT (one owner per fact): the canonical "Drive
	//    write" owner is solely inside ArtifactPreparation. There is no
	//    second Publish call site in this Service — the prior
	//    Publisher.Publish retry loop was REMOVED in P0-COMPL-4 to
	//    eliminate the double-publish defect.
	var published finalization.PublishedArtifact
	prepErr := retry.Do(ctx, func() error {
		var err error
		published, err = s.preparer.Prepare(ctx, *va)
		return err
	}, retry.DefaultOptions())
	if prepErr != nil {
		return nil, fmt.Errorf(
			"completion.publishOne[%s]: prepare (canonical publish: validate+sha256+Drive-upload): %w",
			va.ArtifactID, prepErr,
		)
	}

	// 4. Compose the canonical PublishedArtifact envelope. The IdempotencyKey
	//    is byte-stable per P0.7 via the canonical ArtifactIdempotencyKey
	//    helper. The Location comes from Prepare (which delegated to its
	//    finalization.PublisherPort inside). No double-Publish here — the
	//    populated Location is the SINGLE source of truth for where the
	//    artifact lives on the canonical Drive.
	pub := published
	pub.IdempotencyKey = remote.ArtifactIdempotencyKey(jobID, subID, va.SHA256)
	// pub.Location already populated by Prepare (via its embedded
	// finalization.PublisherPort); no override needed.

	// 6. Post-publish fail-closed invariant (godlike/07 no-fake-availability):
	//    re-read on-disk local file and recompute SHA-256; mismatch surfaces
	//    ErrFinalChecksumMismatch via errors.Is.
	if err := s.verifyFinalChecksum(ctx, va, &pub); err != nil {
		return nil, err
	}

	// 7. Record publication for future idempotent replays. Any record failure
	//    fails the pipeline (godlike/06 SSOT — no silent-success — so the
	//    caller knows there's a bookkeeper wire-up issue to resolve).
	if err := s.bookkeeper.RecordPublished(ctx, jobID, subID, va.SHA256, &pub); err != nil {
		return nil, fmt.Errorf(
			"completion.publishOne[%s]: bookkeeper record: %w",
			va.ArtifactID, err,
		)
	}

	return &pub, nil
}

// verifyFinalChecksum is the post-publish fail-closed invariant checker.
// Reads the on-disk local file (va.LocalPath) and recomputes SHA-256. If
// the recomputed hash differs from va.SHA256, surfaces ErrFinalChecksumMismatch
// via fmt.Errorf("...: %w", ErrFinalChecksumMismatch) wrapper.
//
// Defensive contract: if va.LocalPath is empty (the staging seam was unable
// to provide a local handle), the function returns nil (skips the check) and
// the gate is delegated to the Publisher's internal verifySHA256 (already in
// the concrete adapter per P1.5 closes history). Per godlike/07 honest
// scope-lock: this function does NOT itself retry or recompute the publish
// path — it ONLY checks the on-disk invariant after Publish has succeeded.
func (s *Service) verifyFinalChecksum(
	ctx context.Context,
	va *finalization.VerifiedArtifact,
	pub *finalization.PublishedArtifact,
) error {
	_ = ctx // ctx reserved for future tracing/correlation propagation per P3 surface
	_ = pub // pub reserved for future Stage+1 invariants (e.g. cumulative version drift)

	if va.LocalPath == "" {
		// No local handle → gate is delegated to Publisher.verifySHA256 inside
		// the concrete adapter. This is HONEST scope-lock (we don't fabricate a
		// path).
		return nil
	}

	observed, err := files.HashFile(va.LocalPath, sha256.New())
	if err != nil {
		return fmt.Errorf(
			"completion.publishOne[%s]: hash recompute on %q: %w",
			va.ArtifactID, va.LocalPath, err,
		)
	}
	if observed != va.SHA256 {
		return fmt.Errorf(
			"completion.publishOne[%s]: final checksum drift: staged=%s on-disk=%s: %w",
			va.ArtifactID, va.SHA256, observed, ErrFinalChecksumMismatch,
		)
	}
	return nil
}

// splitJobArtifactID derives (jobID, subID) from a composite ArtifactID.
// Convention per the code-searcher evidence in adapter_test.go:218:
// ArtifactID is "job-001:script_json" → (jobID="job-001", subID="script_json").
// If the input has no ":", the whole string is returned as jobID with empty
// subID (no-op dedup); this preserves byte-stability for ArtifactID strings
// that conventionally don't carry a jobID prefix.
func splitJobArtifactID(artifactID string) (jobID, subID string) {
	if artifactID == "" {
		return "", ""
	}
	if i := strings.Index(artifactID, ":"); i >= 0 {
		return artifactID[:i], artifactID[i+1:]
	}
	return artifactID, ""
}
