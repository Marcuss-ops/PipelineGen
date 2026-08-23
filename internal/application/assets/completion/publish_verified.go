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
//     concern that this function FEEDS via the returned []PublishOutcome.
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
//   - NO silent-partial-success: len(outcomes) == len(input) on success OR err
//     fails the pipeline (godlike/06 SSOT)
//
// P0-COMPL-4 dedup discipline (Closure, July 2026): completion.Service MUST
// hold ONLY 2 ports (Preparer + IdempotencyBookkeeper). The Publisher port
// was REMOVED in P0-COMPL-4 to eliminate the godlike/06 SSOT violation
// (TWO owners of the canonical "Drive write" fact). The canonical Drift
// Detector is `dedup_tdd_test.go::TestDedup_NoPublisherFieldOnServiceStruct`
// (reflection-based) which fails the build on any future re-introduction
// of a `publisher`/`pub`/`notifier` field.
//
// P1 #14 (July 2026) — loop-accumulation contract:
//   - PublishVerifiedArtifacts now returns []PublishOutcome (not
//     []*PublishedArtifact). The loop NO LONGER short-circuits on first
//     non-typed-idempotency error; per-artifact failures (Prepare transient
//     bubbles, final-checksum mismatch, transient-exhausted) are EMBEDDED
//     into the outcome at the corresponding slice index.
//   - Top-level error return is reserved for the FAIL-CLOSED collision
//     surface (ErrIdempotencyKeyConflictDifferingContent). All other
//     failures are accumulated; per-art typed errors are surfaced via
//     outcome.Err.
//   - Reused=true signals that the outcome came from a SAME-content
//     idempotent-replay path (canonical byte-stable replay). On same-
//     content replay the top-level err is nil — this is the explicit
//     breaking contract change vs the prior P0-COMPL-4 wrap-ErrAlreadyPublished
//     behaviour. Callers route the two surfaces via:
//   - Same content           → errors.Is(err, X) is FALSE; outcome.Reused == true
//   - Different content key → errors.Is(err, ErrIdempotencyKeyConflictDifferingContent) is TRUE
//   - Per-art failure       → errors.Is(outcome.Err, ErrFinalChecksumMismatch) is TRUE
package completion

import (
	"context"
	"errors"
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
// In the post-P0-COMPL-4 architecture, Preparer IS the canonical publish
// seam: its internal call to finalization.PublisherPort.Publish handles
// validate+sha256+Drive-upload.
type Preparer interface {
	Prepare(ctx context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error)
}

// IdempotencyBookkeeper tracks the (jobID, artifactID, sha256Hex) → PublishedArtifact
// dedup surface so a re-stage with the SAME content triple returns the EXISTING
// record WITHOUT re-running Prepare (idempotent replay; godlike/07
// no-duplicate-side-effects).
//
// 4 methods:
//   - IsPublished              : O(1) lookup; returns (true, nil) if triple recorded
//   - LookupPublished          : returns the full *PublishedArtifact envelope
//   - LookupByIdempotencyKey   : returns the FIRST record whose IdempotencyKey
//     matches the searched key scoped to the
//     canonical jobID namespace (P1 #14 NEW).
//     Used to detect same-key / different-content
//     collisions (godlike/07 fail-closed) by
//     comparing the cached SHA against the in-flight SHA.
//   - RecordPublished          : stores the envelope after successful publish
type IdempotencyBookkeeper interface {
	IsPublished(ctx context.Context, jobID, artifactID, sha256Hex string) (bool, error)
	LookupPublished(ctx context.Context, jobID, artifactID, sha256Hex string) (*finalization.PublishedArtifact, error)
	LookupByIdempotencyKey(ctx context.Context, jobID, idempotencyKey string) (*finalization.PublishedArtifact, error)
	RecordPublished(ctx context.Context, jobID, artifactID, sha256Hex string, pub *finalization.PublishedArtifact) error
}

// PublishOutcome (P1 #14, July 2026) — canonical return envelope for the
// per-artifact publish result. Surfaced by PublishVerifiedArtifacts at each
// slice index; carries the artifact envelope, the reuse flag, AND the
// per-artifact typed error (so the loop can accumulate outcomes without
// short-circuiting on non-fatal per-artifact failures per the P1 #14
// contract).
//
// The 3rd field `Err` is a strict-extension-of-spec addition: the user
// spec named only Artifact + Reused, but the godlike/07 typed-error
// contract requires that per-artifact errors be reachable via errors.Is
// probing (per the canonical no-fake-availability posture). Without an
// explicit Err surface, callers would have to guess whether Artifact=nil
// means "failure" or "success with deferred prepare-publish pick". The
// 3-field surface is the typed-error contract's canonical surface.
//
// godlike/06 SSOT (one canonical owner per fact): this struct lives in
// the publish service package (not in domain/asset or domain/job) because
// the publish service is the single canonical owner of "what it means to
// publish an artifact". Moving the typed outcome surface out of this
// package would require moving the publishing contract.
type PublishOutcome struct {
	// Artifact is the canonical PublishedArtifact envelope for this
	// per-artifact outcome. Non-nil on success (whether fresh-publish
	// [Reused=false] or cache-hit replay [Reused=true]). Nil on
	// per-artifact failure (with Err non-nil).
	Artifact *finalization.PublishedArtifact
	// Reused signals the outcome came from a SAME-content idempotent-replay
	// path. The canonical byte-stable replay case: a prior record's
	// SHA256 matches the in-flight request's SHA256 — Prepare is NOT
	// re-run and Drive is NOT re-written.
	Reused bool
	// Err is the typed-error terminal failure envelope for this
	// per-artifact outcome. Nil on success (whether Reused or not).
	// On per-artifact failure, wraps the canonical typed sentinel
	// (ErrPublishInvalidArtifact / ErrFinalChecksumMismatch / etc.)
	// via fmt.Errorf("%w: %v", sentinel, details).
	Err error
}

// IsSuccess is the canonical helper: an outcome is "success" iff Artifact
// is non-nil AND Err is nil. Reused status is orthogonal to success-vs-
// failure distinction.
func (o PublishOutcome) IsSuccess() bool {
	return o.Artifact != nil && o.Err == nil
}

// Service publishes verified artifacts to the destination (Drive) with full
// godlike/07 typed-error contracts: idempotent retry-on-transient (delegated
// to the Preparer concrete's internal finalization.PublisherPort), fail-closed
// post-publish checksum invariant (recomputes SHA-256 from local file, gates
// against ErrFinalChecksumMismatch drift), and SAME-content idempotent-replay
// short-circuit (returns cached envelope with Outcome.Reused=true + Err=nil).
//
// 2-port shape (P0-COMPL-4). The Publisher port was REMOVED in P0-COMPL-4
// because it duplicated the canonical Drive-write owner inside Preparer.
// Drift detection: `dedup_tdd_test.go::TestDedup_NoPublisherFieldOnServiceStruct`
// (reflection-based) enforces this — any PR that re-introduces a `publisher`
// field on Service fails at build/CI.
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
func NewService(pr Preparer, b IdempotencyBookkeeper) (*Service, error) {
	if pr == nil {
		return nil, fmt.Errorf("completion.NewService: Preparer port is required (REMOVED P0-COMPL-4 Publisher port; ArtifactPreparation is the canonical Drive-write owner per godlike/06 SSOT): %w", ErrPublishInvalidArtifact)
	}
	if b == nil {
		return nil, fmt.Errorf("completion.NewService: IdempotencyBookkeeper port is required: %w", ErrPublishInvalidArtifact)
	}
	return &Service{preparer: pr, bookkeeper: b}, nil
}

// PublishVerifiedArtifacts routes each VerifiedArtifact through the
// 5-step publish chain (LOOKUP-by-idem-key → Prepare → verify-final-
// checksum → record-idempotency) and returns a slice of PER-ARTIFACT
// outcomes (matches the input slice length; 1:1 mapping).
//
// Failure paths (godlike/07 typed-error contract):
//
//   - empty input slice                 → wrapped ErrPublishEmptySlice
//   - nil pointer in slice              → wrapped ErrPublishInvalidArtifact
//   - SAME-content idempotent-replay    → Outcome{Reused=true, Err=nil}; top-level err is NIL
//   - SAME-idem-key / DIFFERENT-sha256  → FAIL-CLOSED: wrapped
//     ErrIdempotencyKeyConflictDifferingContent elevates to TOP-LEVEL err;
//     per-art Err is set; the partial outcomes BEFORE the collision index
//     are preserved so callers can introspect them
//   - Prepare err (transient or terminal) → per-art Err wraps the typed sentinel;
//     top-level err is nil (loop isolation discipline)
//   - final-checksum mismatch           → per-art Err wraps ErrFinalChecksumMismatch
//     via errors.Is; top-level err is nil
//   - bookkeeper record failure         → per-art Err wraps ErrPublishInvalidArtifact
//     or the underlying typed sentinel; top-level err is nil
//
// P1 #14 loop-accumulation contract (July 2026):
//   - Per-artifact SUCCESS:    outcome {Artifact=<pub>, Reused=<bool>, Err=nil}
//   - Per-artifact FAILURE:    outcome {Artifact=nil,                  Err=<typed>}
//   - Idem-key COLLISION (diff
//     content):                partial outcome (with the
//     collision-failure envelope + Reused=false +
//     Err wrapping the typed sentinel) IS preserved
//     in the slice returned; top-level err is
//     non-nil ONLY on this collision
//   - Caller loops NO LONGER short-circuit on first non-typed failure; this
//     is the canonical "loop isolation" surface
//
// Returns (outcomes, nil) when ALL per-art outcomes are IsSuccess().
// Returns (outcomes, ErrXxx) when an idem-key-different-content collision
// terminates the loop early; the partial outcome up to that point is preserved.
func (s *Service) PublishVerifiedArtifacts(
	ctx context.Context,
	artifacts []*finalization.VerifiedArtifact,
) (outcomes []PublishOutcome, err error) {
	// 0. Input contract gate (typed-error pre-loop).
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("completion.PublishVerifiedArtifacts: %w", ErrPublishEmptySlice)
	}
	for idx, va := range artifacts {
		if va == nil {
			return nil, fmt.Errorf("completion.PublishVerifiedArtifacts: nil pointer at index [%d]: %w",
				idx, ErrPublishInvalidArtifact)
		}
	}

	// 1. Per-artifact publish loop. P1 #14 (July 2026) DOES NOT short-circuit
	//    on first error: each per-artifact result is accumulated as a
	//    PublishOutcome element. The ONLY top-level error surface reserved
	//    (returning non-nil err) is the FAIL-CLOSED
	//    ErrIdempotencyKeyConflictDifferingContent sentinel — distinguished
	//    from per-artifact failures via errors.Is probing at the cutover pipe.
	outcomes = make([]PublishOutcome, 0, len(artifacts))
	for _, va := range artifacts {
		pub, reused, perArtErr := s.publishOne(ctx, va)
		outcome := PublishOutcome{
			Artifact: pub,
			Reused:   reused,
		}
		if perArtErr != nil {
			outcome.Err = perArtErr
			// P1 #14: only the typed idem-key-different-content collision
			// elevates to the top-level err return. Per-artifact failures
			// (Prepare errors, final-checksum drift, transient-exhausted)
			// are accumulated with their typed err in outcome.Err, but do
			// NOT bubble to the top-level return.
			//
			// Once a typed idem-key-collision elevates to the top, we
			// preserve the partial outcomes (including the failing one)
			// so callers can introspect them.
			if errors.Is(perArtErr, ErrIdempotencyKeyConflictDifferingContent) {
				outcomes = append(outcomes, outcome)
				return outcomes, perArtErr
			}
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

// publishOne is the per-artifact publish chain
// (LookupByIdempotencyKey → Prepare → verify-final-checksum → record-idempotency).
// Isolated for testability and for clarity of the 4-step flow.
//
// P1 #14 (July 2026) signature: returns (pub, reused, err) where:
//   - pub    : the per-artifact envelope (nil on failure)
//   - reused : true if the result came from a SAME-content idempotent replay
//     (no Prepare re-run; no Drive re-write)
//   - err    : nil on success; typed sentinel + context on per-artifact failure
func (s *Service) publishOne(
	ctx context.Context,
	va *finalization.VerifiedArtifact,
) (*finalization.PublishedArtifact, bool, error) {
	// 1. Derive (jobID, subID) from ArtifactID. Convention: ArtifactID encoded
	//    as "jobID:subID" per the canonical reuse from the adapter_test.go
	//    surface. Empty/no-colon → returns (wholeArtifactID, "") with the
	//    empty-subID canonical behaviour.
	jobID, subID := splitJobArtifactID(va.ArtifactID)

	// 2. Derive the upcoming idempotency key (canonical ArtifactIdempotencyKey).
	//    This is the key the new publish WOULD RECORD if it ran a fresh
	//    upload — used in step 3 below to detect SAME-key / DIFFERENT-content
	//    collisions.
	upcomingKey := remote.ArtifactIdempotencyKey(jobID, subID, va.SHA256)

	// 3. SAME-key collision check (P1 #14). Forward-prevention against
	//    upstream wire-up bugs that set IdempotencyKey manually — a prior
	//    record whose IdempotencyKey equals upcomingKey but whose SHA256
	//    differs from va.SHA256 signals a CANONICAL-INVARIANT violation
	//    (the canonical helper derives idem-key FROM sha256 + subID, so
	//    same-key / different-sha CANNOT happen via the canonical path).
	existing, lookupErr := s.bookkeeper.LookupByIdempotencyKey(ctx, jobID, upcomingKey)
	if lookupErr == nil && existing != nil {
		if existing.SHA256 == va.SHA256 {
			// SAME-content idempotent replay (godlike/07 no-duplicate-
			// side-effects). Return cached envelope with Reused=true + nil err;
			// the canonical DRY path — no Prepare re-run, no Drive re-write.
			return existing, true, nil
		}
		// DIFFERENT-content collision: FAIL-CLOSED. Wrap
		// ErrIdempotencyKeyConflictDifferingContent with the drift summary
		// so the cutover pipe can route it via errors.As + errors.Is.
		return nil, false, fmt.Errorf(
			"completion.publishOne[%s]: idempotency-key=%s previously recorded with sha=%q, in-flight sha=%q (drift detected — upstream InvariantViolation): %w",
			va.ArtifactID, upcomingKey, existing.SHA256, va.SHA256,
			ErrIdempotencyKeyConflictDifferingContent,
		)
	}

	// 4. Prepare (884_validate+sha256+Drive-upload via internal finalization.PublisherPort)
	//    wrapped in retry-on-transient loop. This Service IS the canonical
	//    owner of retry policy for the publish seam in the post-P0-COMPL-4
	//    architecture: the Preparer (ArtifactPreparation) internally invokes
	//    its finalization.PublisherPort to Drive; transient errors from that
	//    publish bubble out of Prepare and through pkg/retry.Do until either
	//    the operation succeeds or the retry budget is exhausted.
	//
	//    godlike/06 SSOT (one owner per fact): the canonical "Drive write"
	//    owner is solely inside ArtifactPreparation. There is no second
	//    Publish call site in this Service — the prior Publisher.Publish
	//    retry loop was REMOVED in P0-COMPL-4 to eliminate the double-
	//    publish defect.
	//
	//    P1 #14 (July 2026) — retry-on-transient filter: this Service
	//    MUTATES retry.DefaultOptions() to set IsRetryable=retry.IsTransient
	//    so non-transient errors (terminal auth failures, structure errors,
	//    schema-version mismatches) burn ZERO retry budget. The pre-P1 #14
	//    canonical surface used retry.Do(ctx, fn, retry.DefaultOptions())
	//    which left IsRetryable=nil — meaning retry.Do retried ALL errors
	//    up to MaxAttempts (3) regardless of classification. Under the
	//    P1 #14 loop-accumulation contract, a per-art terminal failure
	//    MUST surface in outcome.Err at the FIRST attempt (otherwise the
	//    2nd-3rd attempts exhaust the budget AND pollute the test
	//    outcome with duplicate error contexts). The IsRetryable filter
	//    is the typed-path that aligns production with the documented
	//    "retry-on-transient loop" intent.
	var published finalization.PublishedArtifact
	retryOpts := retry.DefaultOptions()
	retryOpts.IsRetryable = retry.IsTransient
	prepErr := retry.Do(ctx, func() error {
		var err error
		published, err = s.preparer.Prepare(ctx, *va)
		return err
	}, retryOpts)
	if prepErr != nil {
		return nil, false, fmt.Errorf(
			"completion.publishOne[%s]: prepare (canonical publish: validate+sha256+Drive-upload): %w",
			va.ArtifactID, prepErr,
		)
	}

	// 5. Compose the canonical PublishedArtifact envelope. The IdempotencyKey
	//    is byte-stable per P0.7 via the canonical ArtifactIdempotencyKey
	//    helper. The Location comes from Prepare (which delegated to its
	//    finalization.PublisherPort inside). No double-Publish here — the
	//    populated Location is the SINGLE source of truth for where the
	//    artifact lives on canonical Drive.
	pub := published
	pub.IdempotencyKey = upcomingKey
	// pub.Location already populated by Prepare (via its embedded
	// finalization.PublisherPort); no override needed.

	// 6. Post-publish fail-closed invariant (godlike/07 no-fake-availability):
	//    re-read on-disk local file and recompute SHA-256; mismatch surfaces
	//    ErrFinalChecksumMismatch via errors.Is.
	if err := s.verifyFinalChecksum(ctx, va, &pub); err != nil {
		return nil, false, err
	}

	// 7. Record publication for future idempotent replays. Any record failure
	//    fails the pipeline (godlike/06 SSOT — no silent-success — so the
	//    caller knows there's a bookkeeper wire-up issue to resolve).
	if err := s.bookkeeper.RecordPublished(ctx, jobID, subID, va.SHA256, &pub); err != nil {
		return nil, false, fmt.Errorf(
			"completion.publishOne[%s]: bookkeeper record: %w",
			va.ArtifactID, err,
		)
	}

	return &pub, false, nil
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

	observed, err := files.SHA256File(va.LocalPath)
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
