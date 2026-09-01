// Package sceneir — compiler.go is the SINGLE canonical producer of a
// SceneIR from a canonical VidRush segment. No other code may construct a
// SceneIR value: doing so would bypass the immutable-source-identity
// contract this package enforces.
//
// The compiler is the place where the LIVE-test bugs are fixed at the root:
//
//   - segment_id is preserved verbatim (mediterranean-* is never replaced
//     by scene-N): copied from CanonicalSegment.ID and stamped into the
//     immutable SourceIdentity snapshot.
//   - source_text is separated from narration_text: the LLM narration
//     override is accepted only into NarrationText; SourceText stays the
//     verbatim canonical source.
//   - a SemanticProfile is ALWAYS populated: when the entity extractor /
//     small LLM produced no profile, the compiler derives a minimal but
//     non-empty profile from the source text so visual_profile is never
//     null (the LIVE test had visual_profile = null for 5/5 segments).
package sceneir

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// CompileInput is the canonical input to the SceneIR compiler. It carries
// the canonical segment (immutable identity + source text) and the optional
// downstream surfaces the compiler is allowed to fold in:
//
//   - NarrationOverride: the creative, speakable narration the LLM MAY
//     rewrite. When nil/empty, NarrationText defaults to SourceText.
//   - EntityResult: the deterministic extraction surface. When nil, the
//     compiler derives a minimal SemanticProfile from the source text so
//     the SceneIR still has a non-empty profile.
//   - UnderstandingModelVersion / PromptVersion: stamped onto the full
//     SegmentSemanticProfile when present (part of its cache fingerprint).
type CompileInput struct {
	Segment                   script.CanonicalSegment
	NarrationOverride         string
	EntityResult              *script.EntityResult
	UnderstandingModelVersion string
	PromptVersion             string
}

// ErrCompileInputInvalid is the fail-closed error for a CompileInput whose
// canonical segment is not valid before compilation. The compiler never
// silently fixes up an invalid segment because doing so would hide an
// upstream identity bug.
var ErrCompileInputInvalid = errors.New("sceneir compile: invalid canonical segment input")

// Compile is the SINGLE canonical producer of a SceneIR. It:
//
//  1. Normalizes the input canonical segment (trims, fills SourceText +
//     hashes deterministically — the existing script.NormalizeCanonicalSegment
//     contract).
//  2. Validates the normalized segment (identity + hash contract).
//  3. Stamps the immutable source identity verbatim into the SceneIR.
//  4. Accepts the LLM narration override into NarrationText ONLY, defaulting
//     to SourceText when no override is provided. The override NEVER touches
//     SourceText/SegmentID/Position/SourceTextHash.
//  5. Builds the full canonical SegmentSemanticProfile from the entity
//     result (when present) and projects a compact SemanticProfile from it
//     that is guaranteed non-empty (subject + at least one visual term),
//     so a compiled SceneIR can never reach the query planners with a null
//     visual_profile.
func Compile(in CompileInput) (SceneIR, error) {
	segment := script.NormalizeCanonicalSegment(in.Segment)
	if err := segment.Validate(); err != nil {
		return SceneIR{}, fmt.Errorf("%w: %s", ErrCompileInputInvalid, err.Error())
	}

	narration := strings.TrimSpace(in.NarrationOverride)
	if narration == "" {
		narration = segment.SourceText
	}

	profile := buildFullProfile(segment, in.EntityResult, in.UnderstandingModelVersion, in.PromptVersion)

	ir := SceneIR{
		SegmentID:      segment.ID,
		Position:       segment.Position,
		SourceText:     segment.SourceText,
		SourceTextHash: segment.SourceTextHash,
		NarrationText:  narration,
		Profile:        *profile,
	}
	return ir, nil
}

// MustCompile is the convenience wrapper that panics on a compile error.
// Use only in tests where an invalid input is itself a test failure.
func MustCompile(in CompileInput) SceneIR {
	ir, err := Compile(in)
	if err != nil {
		panic(err)
	}
	return ir
}

// buildFullProfile constructs the canonical script.SegmentSemanticProfile
// from the segment + optional entity result. When the entity result is
// present it is projected through the canonical
// script.BuildSegmentSemanticProfile (the single canonical point); when
// absent, a minimal but valid profile is synthesized from the segment
// identity so the SceneIR still carries a non-null profile for MediaCert.
func buildFullProfile(segment script.CanonicalSegment, res *script.EntityResult, modelVersion, promptVersion string) *script.SegmentSemanticProfile {
	if res != nil {
		profile := script.BuildSegmentSemanticProfile(segment, *res, modelVersion, promptVersion)
		return &profile
	}
	profile := script.SegmentSemanticProfile{
		SegmentID:                 segment.ID,
		TextHash:                  segment.TextHash,
		ExecutionMode:             segment.ExecutionMode.Normalize(),
		UnderstandingModelVersion: modelVersion,
		PromptVersion:             promptVersion,
		Topic:                     strings.TrimSpace(segment.SourceText),
		VisualTerms:               []script.WeightedKeyword{{Value: strings.TrimSpace(segment.SourceText), Confidence: 1}},
	}
	return &profile
}
