// Package sceneir — identity.go owns the IMMUTABLE source-identity
// boundary of a compiled SceneIR. It is the physical enforcement of the
// single rule:
//
//	SOURCE IDENTITY IS IMMUTABLE
//
// The LLM may rewrite NarrationText; it may NEVER touch SegmentID,
// Position, SourceText or SourceTextHash. The helpers here let MediaCert
// and downstream stages verify that contract without re-deriving the rules
// in multiple places.
package sceneir

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// IdentityViolation is a typed error describing exactly which immutable
// field of a SceneIR was tampered with. It is the value MediaCert surfaces
// when a downstream stage tried to rewrite source identity.
type IdentityViolation struct {
	// Field is the immutable field that was tampered with
	// ("segment_id", "position", "source_text", "source_text_hash").
	Field string
	// Expected is the value the compiler stamped at compile time.
	Expected string
	// Observed is the tampered value found at the check boundary.
	Observed string
}

func (e IdentityViolation) Error() string {
	return fmt.Sprintf("sceneir identity violation: %s expected %q, observed %q", e.Field, e.Expected, e.Observed)
}

// SourceIdentity is the immutable identity snapshot the compiler stamps at
// compile time. It is carried separately from the mutable NarrationText so
// any post-compile check can compare a SceneIR's current identity against
// the snapshot without trusting the SceneIR's own fields.
type SourceIdentity struct {
	SegmentID      string `json:"segment_id"`
	Position       int    `json:"position"`
	SourceText     string `json:"source_text"`
	SourceTextHash string `json:"source_text_hash"`
}

// Identity returns the immutable source-identity snapshot of a SceneIR.
// It is the value MediaCert compares against when checking that downstream
// rewriting did not contaminate source identity.
func (s SceneIR) Identity() SourceIdentity {
	return SourceIdentity{
		SegmentID:      s.SegmentID,
		Position:       s.Position,
		SourceText:     s.SourceText,
		SourceTextHash: s.SourceTextHash,
	}
}

// VerifyIdentity reports whether a SceneIR still carries the immutable
// source identity the compiler stamped. It is the fail-closed check used at
// persistence and provider boundaries. A non-nil error is an
// IdentityViolation describing the first tampered field.
func (s SceneIR) VerifyIdentity(expected SourceIdentity) error {
	if strings.TrimSpace(s.SegmentID) != expected.SegmentID {
		return IdentityViolation{Field: "segment_id", Expected: expected.SegmentID, Observed: s.SegmentID}
	}
	if s.Position != expected.Position {
		return IdentityViolation{Field: "position", Expected: fmt.Sprintf("%d", expected.Position), Observed: fmt.Sprintf("%d", s.Position)}
	}
	if s.SourceText != expected.SourceText {
		return IdentityViolation{Field: "source_text", Expected: expected.SourceText, Observed: s.SourceText}
	}
	if s.SourceTextHash != expected.SourceTextHash {
		return IdentityViolation{Field: "source_text_hash", Expected: expected.SourceTextHash, Observed: s.SourceTextHash}
	}
	return nil
}

// IsNarrationDivergence reports whether the SceneIR's NarrationText has
// diverged from its immutable SourceText. Divergence is NOT an error: the
// LLM is allowed to rewrite NarrationText. The check exists so MediaCert
// can distinguish "narration was rewritten (allowed)" from "source text was
// rewritten (forbidden)". When divergence is true, query planners MUST
// consume SourceText + Profile, never NarrationText.
func (s SceneIR) IsNarrationDivergence() bool {
	return s.NarrationText != "" && s.NarrationText != s.SourceText
}

// RecomputeSourceTextHash recomputes the canonical source-text hash from
// the SceneIR's current SourceText using the same algorithm the compiler
// used. A mismatch with the stamped SourceTextHash proves SourceText was
// tampered with after compilation. The canonical hasher lives in
// internal/kernel/script so SceneIR does not re-derive hashing.
func (s SceneIR) RecomputeSourceTextHash() string {
	return script.ComputeCanonicalSegmentTextHash(s.SourceText)
}

// SourceTextTampered reports whether the stamped SourceTextHash no longer
// matches a fresh hash of the current SourceText. This is the cryptographic
// proof that SourceText was mutated after compilation, independent of the
// identity-snapshot comparison.
func (s SceneIR) SourceTextTampered() bool {
	return s.RecomputeSourceTextHash() != s.SourceTextHash
}
