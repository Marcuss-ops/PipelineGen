// Package checkpoint — resume.go owns the resume decision: whether a
// recorded unit completion can be SKIPPED or must be recomputed. It is the
// durable counterpart of the runner's stage-skip logic: the runner skips
// stages from its own checkpoint, THIS logic decides whether a unit's
// recorded completion is still valid for the CURRENT inputs.
//
// Resume is never "status == COMPLETED". Every dimension must pass:
//
//	checkpoint exists
//	  ↓
//	input fingerprint == expected fingerprint?
//	  ↓
//	artifact exists?            (artifactless stages pass trivially)
//	  ↓
//	artifact SHA-256 still matches?
//	  ↓
//	processor version compatible?
//	  ↓
//	SKIP   — otherwise INVALIDATE → recompute
package checkpoint

import (
	"context"
	"fmt"
)

// Decision is the outcome of one unit resume decision.
type Decision string

const (
	// DecisionSkip means the recorded completion is still valid: the unit's
	// work is skipped and its recorded artifact reused.
	DecisionSkip Decision = "SKIP"
	// DecisionExecute means the unit must run (missing checkpoint, changed
	// inputs, or artifact no longer valid).
	DecisionExecute Decision = "EXECUTE"
)

// ExpectedInput pins what the unit WOULD be computed from right now. The
// recorded checkpoint is reusable only when both dimensions match.
type ExpectedInput struct {
	InputFingerprint string
	ProcessorVersion string
}

// ArtifactStatus is the verified state of a recorded artifact, produced by
// an ArtifactVerifier against the actual artifact bytes.
type ArtifactStatus struct {
	Exists        bool
	SHA256Matches bool
}

// ArtifactVerifier checks that a completed unit's artifact still exists and
// its bytes still hash to the recorded digest. sha256 is the recorded
// digest; uri is the storage reference (e.g. "cas://<sha256>", empty when
// the unit recorded none). The concrete adapter reads the actual bytes
// (CAS re-hash) — resume never trusts the recorded row alone.
type ArtifactVerifier interface {
	VerifyArtifact(ctx context.Context, sha256, uri string) (ArtifactStatus, error)
}

// CanResume is the pure resume predicate. It returns false with a
// human-readable reason whenever ANY dimension fails; it is deliberately
// NOT "cp.Status == COMPLETED". Artifactless checkpoints (recorded without
// an artifact) skip the artifact checks — nothing to verify.
func CanResume(cp *Checkpoint, expected ExpectedInput, artifact ArtifactStatus) (bool, string) {
	if cp == nil {
		return false, "no checkpoint"
	}
	if cp.Status != StatusCompleted {
		return false, fmt.Sprintf("checkpoint status %q is not %s", cp.Status, StatusCompleted)
	}
	if cp.InputFingerprint != expected.InputFingerprint {
		return false, "input fingerprint changed"
	}
	if cp.ProcessorVersion != expected.ProcessorVersion {
		return false, fmt.Sprintf("processor version changed: recorded %q != expected %q", cp.ProcessorVersion, expected.ProcessorVersion)
	}
	if cp.ArtifactSHA256 != "" {
		if !artifact.Exists {
			return false, "artifact missing"
		}
		if !artifact.SHA256Matches {
			return false, "artifact hash mismatch"
		}
	}
	return true, ""
}

// Resolver combines the durable Store with artifact verification into the
// single resume decision surface for every work unit (stage, unit):
//
//	Decide → SKIP  (reuse recorded completion)
//	       → EXECUTE (run the unit)
//
// A definitively stale checkpoint (fingerprint/processor changed, artifact
// missing or corrupt) is INVALIDATED before EXECUTE returns, so the
// recompute writes a fresh record. A checkpoint that cannot be JUDGED
// (verifier error or not configured) also yields EXECUTE but is NOT
// invalidated — the record may be fine, we just cannot verify it.
type Resolver struct {
	store    Store
	verifier ArtifactVerifier
}

// NewResolver builds the resolver. A nil store is fail-open at Decide time
// (always EXECUTE, never SKIP on an unwired store); a nil verifier blocks
// SKIP for any unit that recorded an artifact.
func NewResolver(store Store, verifier ArtifactVerifier) *Resolver {
	return &Resolver{store: store, verifier: verifier}
}

// Decide resolves one unit. Hard errors only for store failures (the
// decision cannot be made); all other conditions degrade to EXECUTE, never
// to an unverified SKIP.
func (r *Resolver) Decide(ctx context.Context, jobID, stage, unitID string, expected ExpectedInput) (Decision, string, error) {
	if r == nil || r.store == nil {
		return DecisionExecute, "checkpoint store not wired", nil
	}
	cp, err := r.store.Get(ctx, jobID, stage, unitID)
	if err != nil {
		return DecisionExecute, "", fmt.Errorf("checkpoint get %s/%s/%s: %w", jobID, stage, unitID, err)
	}
	if cp == nil {
		return DecisionExecute, "no checkpoint", nil
	}
	artifact := ArtifactStatus{Exists: true, SHA256Matches: true}
	if cp.ArtifactSHA256 != "" {
		if r.verifier == nil {
			return DecisionExecute, "artifact verifier not configured", nil
		}
		status, err := r.verifier.VerifyArtifact(ctx, cp.ArtifactSHA256, cp.ArtifactURI)
		if err != nil {
			return DecisionExecute, fmt.Sprintf("artifact verification failed: %v", err), nil
		}
		artifact = status
	}
	if ok, reason := CanResume(cp, expected, artifact); ok {
		return DecisionSkip, "checkpoint valid", nil
	} else {
		// Definitive staleness: remove the record so the unit is treated as
		// not completed; the recompute writes a fresh checkpoint.
		if invalidateErr := r.store.Invalidate(ctx, jobID, stage, unitID); invalidateErr != nil {
			return DecisionExecute, "", fmt.Errorf("checkpoint invalidate %s/%s/%s: %w", jobID, stage, unitID, invalidateErr)
		}
		return DecisionExecute, reason, nil
	}
}

// Complete records a unit completion through the resolver. Fail-closed: an
// invalid checkpoint is never persisted (validated here, before any store
// implementation), and an unwired store is a typed error.
func (r *Resolver) Complete(ctx context.Context, c Checkpoint) error {
	if r == nil || r.store == nil {
		return ErrNotWired
	}
	if err := c.Validate(); err != nil {
		return err
	}
	return r.store.Complete(ctx, c)
}
