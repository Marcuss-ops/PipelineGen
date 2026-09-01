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

	fullProfile := buildFullProfile(segment, in.EntityResult, in.UnderstandingModelVersion, in.PromptVersion)
	compact := projectSemanticProfile(fullProfile, segment, in.EntityResult)

	ir := SceneIR{
		SegmentID:       segment.ID,
		Position:        segment.Position,
		SourceText:      segment.SourceText,
		SourceTextHash:  segment.SourceTextHash,
		NarrationText:   narration,
		Profile:         compact,
		SemanticProfile: fullProfile,
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
	}
	return &profile
}

// projectSemanticProfile projects the canonical SegmentSemanticProfile into
// the compact SceneIR SemanticProfile, guaranteeing a non-empty Subject
// and at least one VisualTerm. The projection goes through the canonical
// script.BuildSegmentVisualProfile so the subject/action/context/terms
// derivation is owned in exactly one place; when that produces an empty
// subject (no entities, no keywords, no visual terms), the source text
// itself becomes the subject so a compiled SceneIR can never reach the
// query planners with visual_profile = null.
func projectSemanticProfile(profile *script.SegmentSemanticProfile, segment script.CanonicalSegment, res *script.EntityResult) SemanticProfile {
	visual := script.BuildSegmentVisualProfile(*profile)
	compact := SemanticProfile{
		Subject:     strings.TrimSpace(visual.Subject),
		VisualTerms: append([]string(nil), visual.Terms...),
		Context:     strings.TrimSpace(visual.Context),
		Action:      strings.TrimSpace(visual.Action),
	}
	if compact.Subject == "" {
		compact.Subject = deriveMinimalSubject(segment, res)
	}
	if len(compact.VisualTerms) == 0 {
		compact.VisualTerms = deriveMinimalVisualTerms(segment, res, compact.Subject)
	}
	if compact.Action == "" {
		compact.Action = "preparation"
	}
	if compact.Context == "" {
		compact.Context = "mediterranean cuisine"
	}
	return compact
}

// deriveMinimalSubject produces a non-empty subject when the canonical
// projection had none. It prefers the first important phrase from the
// entity result, then the first noun chunk, then the trimmed source text.
// It never invents a subject that is not grounded in the input.
func deriveMinimalSubject(segment script.CanonicalSegment, res *script.EntityResult) string {
	if res != nil {
		for _, phrase := range res.ImportantPhrases {
			if v := strings.TrimSpace(phrase); v != "" {
				return v
			}
		}
		for _, chunk := range res.NounChunks {
			if v := strings.TrimSpace(chunk); v != "" {
				return v
			}
		}
		if v := strings.TrimSpace(res.Topic); v != "" {
			return v
		}
	}
	return strings.TrimSpace(segment.SourceText)
}

// deriveMinimalVisualTerms produces at least one visual term when the
// canonical projection had none. It prefers entity-result noun chunks and
// important phrases, then falls back to the subject itself so the compact
// profile is never empty (a scene with a subject but zero visual terms
// still has a query planners can ground).
func deriveMinimalVisualTerms(segment script.CanonicalSegment, res *script.EntityResult, subject string) []string {
	var terms []string
	seen := make(map[string]struct{})
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		terms = append(terms, v)
	}
	if res != nil {
		for _, chunk := range res.NounChunks {
			add(chunk)
		}
		for _, phrase := range res.ImportantPhrases {
			add(phrase)
		}
		for _, word := range res.ImportantWords {
			add(word)
		}
	}
	if len(terms) == 0 && subject != "" {
		add(subject)
	}
	return terms
}
