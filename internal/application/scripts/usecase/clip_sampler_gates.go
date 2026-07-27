// Package scripts \u2014 clip_sampler_gates.go defines the 10 audit
// gates evaluated per candidate by the FASE-8 single ClipSampler
// impl.
//
// godlike/06 SSOT (one canonical owner per fact): each gate is
// implemented as a separate type with a single Evaluate method.
// The 10 gates map 1:1 to the user's spec:
//
//  1. topic_relevance          \u2014 candidate's transcript/visual
//     summary contains slot.Topic
//  2. source_anchor_coverage   \u2014 anchor coverage \u2265 threshold
//  3. duration                 \u2014 [TargetDurationMs * 0.8,
//     TargetDurationMs * 2.0]
//  4. diversity                \u2014 cosine sim to previous
//     selections < 0.92
//  5. chronological_order      \u2014 SourceAnchor.StartOffset is
//     monotonic ascending across
//     slots
//  6. quality                  \u2014 Score \u2265 0.5
//  7. availability             \u2014 DriveLink OR AvailableByIngest
//  8. no_duplicates           \u2014 ClipID not in previous slots
//  9. transcript_visual_summary_present
//     \u2014 transcript \u2265 10 words
//     AND visual summary \u2265 20 runes
//  10. format_compatible      \u2014 candidate has a non-empty
//     MediaType
//
// All 10 write one GateProvenanceRecord per candidate (pass or
// fail) so the resulting SamplerProvenance is the FULL audit
// trail. fail-fast is explicitly avoided: every gate evaluates,
// every record is written.
package usecase

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// \u2500\u2500 Compile-time gate thresholds (audit-stable constants) \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500
//
// godlike/06 SSOT: thresholds live in ONE place. Operators tune
// them by editing this file. No environment-variable injection for
// now; FASE-9 may promote these to per-call params if needed.

const (
	// MinAnchorCoverageRatio: the audit-gate "source-text anchor
	// coverage" requires the candidate's
	// SourceAnchorCoverageRatio >= this value.
	MinAnchorCoverageRatio = 0.50

	// DurationFloorRatio / DurationCeilingRatio: the "duration"
	// gate requires the candidate's DurationMs to fall within
	// [slot.TargetDurationMs * DurationFloorRatio,
	// slot.TargetDurationMs * DurationCeilingRatio]. The 0.8 / 2.0
	// factors mirror the planner contract pin in
	// ports.ClipPrePlanner.Plan godoc.
	DurationFloorRatio   = 0.8
	DurationCeilingRatio = 2.0

	// MinTranscriptWords: minimum token count for the
	// transcript/visual-summary-present gate. Tokens are
	// strings.Fields(word-count) per slot context.
	MinTranscriptWords = 10

	// MinVisualSummaryLength: minimum rune count for candidate's
	// VisualSummary.
	MinVisualSummaryLength = 20

	// MinQualityScore: cosine-similarity floor for the quality
	// gate.
	// nomic-embed-text cosine scores for the canonical stock-clip index
	// are in the 0.40+ range. Keep the audit floor aligned with that
	// embedding contract; source-specific MinScore remains an additional
	// operator-controlled floor.
	MinQualityScore = 0.4

	// DiversityMaxCosine: max cosine sim between candidate and
	// any previously-selected candidate. Cross-slot governance.
	DiversityMaxCosine = 0.92
)

// \u2500\u2500 SamplerGate interface \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

// SamplerGate is the canonical port for one audit-gate evaluation.
// Each implementation is independently testable (positive +
// negative). The sampler runs all enabled gates per candidate and
// accumulates a GateProvenanceRecord regardless of pass/fail.
type SamplerGate interface {
	// Name returns a stable, snake_case identifier for the gate.
	// Used as GateName in GateProvenanceRecord.
	Name() string

	// Evaluate runs the gate on one candidate. Returns (passed,
	// reason). Reason is always non-empty when Passed is false;
	// may be empty when Passed is true (for trivially-pass gates).
	Evaluate(in ClipSamplerGateInput) (passed bool, reason string)
}

// ClipSamplerGateInput is the per-candidate evaluation context.
// godlike/06 SSOT: this is the canonical envelope; new fields
// belong here, not scattered into per-gate signatures.
type ClipSamplerGateInput struct {
	// Candidate under evaluation.
	Candidate ports.ClipSamplerCandidate

	// Slot context (the pre-planned slot the candidate must satisfy).
	Slot scriptpkg.ClipSearchSlot

	// PreviousSelections: candidates chosen in EARLIER slots
	// (cross-slot governance for diversity, no_duplicates,
	// chronological_order).
	PreviousSelections []scriptpkg.SlotClipBinding

	// CallingSource:
	//   "search" | "catalog" | "curate" for audit log context.
	CallingSource string

	// SourceText length in bytes (for anchor-coverage ratio
	// normalisation). Zero is allowed; the coverage gate will
	// trivially pass on zero coverage ratio only if the candidate
	// has no SourceAnchor set.
	SourceTextLength int
}

// \u2500\u2500 10 gate implementations \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

// 1. topicRelevanceGate fails when NO meaningful (>3-char) token
// from the slot's topic appears in (Transcript + VisualSummary).
// Token-set-overlap scoring is used (vs strict substring) because
// natural language rarely contains literal substring matches for
// full topic strings (e.g. "Pacquiao Broner recap" doesn't appear
// verbatim in transcript prose containing "Pacquiao" and "Broner"
// separately). The scoring rule is deterministic and the gate's
// reason string names the missing tokens so the audit can replay
// the decision.
type topicRelevanceGate struct{}

func (topicRelevanceGate) Name() string { return "topic_relevance" }

func (topicRelevanceGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	topicTokens := tokensGreaterThanThree(in.Slot.Topic)
	if len(topicTokens) == 0 {
		return false, "slot topic has no meaningful (>3 char) tokens"
	}
	haystack := strings.ToLower(strings.Join([]string{
		in.Candidate.Transcript, in.Candidate.VisualSummary,
	}, " "))
	if haystack == "" {
		return false, fmt.Sprintf("candidate transcript+visual_summary empty; lacks tokens %v", topicTokens)
	}
	for _, tok := range topicTokens {
		if strings.Contains(haystack, tok) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("candidate lacks any-token match from %v", topicTokens)
}

// tokensGreaterThanThree splits a topic string into lowercase
// tokens of length > 3, ignoring stopwords and punctuation. The
// 3-character threshold keeps prepositions ("the", "and", "for")
// from masking the gate's intent.
func tokensGreaterThanThree(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == ';' || r == ':' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) > 3 {
			out = append(out, f)
		}
	}
	return out
}

// 2. sourceAnchorCoverageGate fails when
// AnchorCoverageRatio < MinAnchorCoverageRatio.
type sourceAnchorCoverageGate struct{}

func (sourceAnchorCoverageGate) Name() string { return "source_anchor_coverage" }

func (sourceAnchorCoverageGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	if in.Candidate.AnchorCoverageRatio < MinAnchorCoverageRatio {
		return false, fmt.Sprintf("anchor coverage %.2f below threshold %.2f",
			in.Candidate.AnchorCoverageRatio, MinAnchorCoverageRatio)
	}
	return true, ""
}

// 3. durationGate fails when DurationMs is outside
// [TargetDurationMs * DurationFloorRatio, * DurationCeilingRatio].
type durationGate struct{}

func (durationGate) Name() string { return "duration" }

func (durationGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	target := in.Slot.TargetDurationMs
	if target <= 0 {
		// No target set \u2014 trivially pass; this is a per-slot
		// forward-contract that the planner always sets a target.
		return true, ""
	}
	floor := int64(math.Round(float64(target) * DurationFloorRatio))
	ceiling := int64(math.Round(float64(target) * DurationCeilingRatio))
	if in.Candidate.DurationMs < floor {
		return false, fmt.Sprintf("candidate duration %dms < floor %dms (=%v*%v)",
			in.Candidate.DurationMs, floor, target, DurationFloorRatio)
	}
	if in.Candidate.DurationMs > ceiling {
		return false, fmt.Sprintf("candidate duration %dms > ceiling %dms (=%v*%v)",
			in.Candidate.DurationMs, ceiling, target, DurationCeilingRatio)
	}
	return true, ""
}

// 4. diversityGate fails when the candidate has cosine sim >=
// DiversityMaxCosine against any previously-selected candidate.
type diversityGate struct{}

func (diversityGate) Name() string { return "diversity" }

func (diversityGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	if len(in.PreviousSelections) == 0 || len(in.Candidate.Embedding) == 0 {
		return true, ""
	}
	for i, prev := range in.PreviousSelections {
		if len(prev.Embedding) != len(in.Candidate.Embedding) {
			continue
		}
		sim := cosineSim(in.Candidate.Embedding, prev.Embedding)
		if sim >= DiversityMaxCosine {
			return false, fmt.Sprintf("cosine=%.2f vs previous[%d] > %.2f",
				sim, i, DiversityMaxCosine)
		}
	}
	return true, ""
}

// cosineSim returns a deterministic float64 cosine similarity
// between two equal-length float32 vectors.
func cosineSim(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// 5. chronologicalOrderGate fails when the candidate's
// SourceAnchor.StartOffset is BEFORE the last selected candidate's
// offset (i.e., the chronological order would be backwards).
type chronologicalOrderGate struct{}

func (chronologicalOrderGate) Name() string { return "chronological_order" }

func (chronologicalOrderGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	if len(in.PreviousSelections) == 0 {
		return true, ""
	}
	last := in.PreviousSelections[len(in.PreviousSelections)-1]
	if in.Candidate.SourceAnchor == nil || last.SourceAnchor == nil {
		return true, ""
	}
	if in.Candidate.SourceAnchor.StartOffset < last.SourceAnchor.StartOffset {
		return false, fmt.Sprintf("start=%d < last=%d (backwards narrative order)",
			in.Candidate.SourceAnchor.StartOffset, last.SourceAnchor.StartOffset)
	}
	return true, ""
}

// 6. qualityGate fails when Score < MinQualityScore.
type qualityGate struct{}

func (qualityGate) Name() string { return "quality" }

func (qualityGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	if in.Candidate.Score < MinQualityScore {
		return false, fmt.Sprintf("cosine score %.2f below quality floor %.2f",
			in.Candidate.Score, MinQualityScore)
	}
	return true, ""
}

// 7. availabilityGate fails when the candidate has no DriveLink
// AND is not flagged AvailableByIngest.
type availabilityGate struct{}

func (availabilityGate) Name() string { return "availability" }

func (availabilityGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	if in.Candidate.DriveLink == "" && !in.Candidate.AvailableByIngest {
		return false, "no DriveLink and not flagged AvailableByIngest"
	}
	return true, ""
}

// 8. noDuplicatesAcrossSlotsGate fails when the candidate's ClipID
// already exists in PreviousSelections.
type noDuplicatesAcrossSlotsGate struct{}

func (noDuplicatesAcrossSlotsGate) Name() string { return "no_duplicates" }

func (noDuplicatesAcrossSlotsGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	for i, prev := range in.PreviousSelections {
		if prev.ClipID == in.Candidate.ClipID {
			return false, fmt.Sprintf("clip_id=%s already in previous[%d]",
				in.Candidate.ClipID, i)
		}
	}
	return true, ""
}

// 9. transcriptVisualSummaryPresentGate fails when transcript's
// word count < MinTranscriptWords OR visual_summary's rune length
// < MinVisualSummaryLength.
type transcriptVisualSummaryPresentGate struct{}

func (transcriptVisualSummaryPresentGate) Name() string {
	return "transcript_visual_summary_present"
}

func (transcriptVisualSummaryPresentGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	words := len(strings.Fields(in.Candidate.Transcript))
	if words < MinTranscriptWords {
		return false, fmt.Sprintf("transcript words=%d < %d",
			words, MinTranscriptWords)
	}
	runes := len([]rune(in.Candidate.VisualSummary))
	if runes < MinVisualSummaryLength {
		return false, fmt.Sprintf("visual summary runes=%d < %d",
			runes, MinVisualSummaryLength)
	}
	return true, ""
}

// 10. formatCompatibleGate fails when MediaType is empty.
type formatCompatibleGate struct{}

func (formatCompatibleGate) Name() string { return "format_compatible" }

func (formatCompatibleGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	if in.Candidate.MediaType == "" {
		return false, "candidate MediaType is empty"
	}
	return true, ""
}

var SamplerDB *sql.DB

func SetSamplerDB(db *sql.DB) {
	SamplerDB = db
}

// 11. subtitleReadyGate fails when the asset requires subtitles but does not have a READY ASS artifact.
type subtitleReadyGate struct{}

func (subtitleReadyGate) Name() string { return "subtitle_ready" }

func (subtitleReadyGate) Evaluate(in ClipSamplerGateInput) (bool, string) {
	if SamplerDB == nil {
		return true, ""
	}
	var source string
	var hasReadySubtitle int
	err := SamplerDB.QueryRowContext(context.Background(), `
		SELECT COALESCE(source, '') AS source,
		       (SELECT COUNT(*) FROM asset_subtitle_artifacts
				WHERE asset_id = media_assets.id
				  AND format = 'ass'
				  AND status = 'READY'
				  AND drive_file_id <> ''
				  AND drive_url <> ''
				  AND is_current = 1)
		FROM media_assets
		WHERE id = ?`, in.Candidate.ClipID).Scan(&source, &hasReadySubtitle)
	if err != nil {
		return false, fmt.Sprintf("database lookup failed for %s: %v", in.Candidate.ClipID, err)
	}

	if asset.RequiresSubtitles(source) {
		if hasReadySubtitle == 0 {
			return false, fmt.Sprintf("clip source %q requires subtitles but no READY ASS artifact exists", source)
		}
	}
	return true, ""
}

// defaultGates returns the canonical 11-gate list in evaluation
// order. The order is itself part of the audit contract: every
// sampler run evaluates gates in this sequence (deterministic).
func defaultGates() []SamplerGate {
	return []SamplerGate{
		topicRelevanceGate{},
		sourceAnchorCoverageGate{},
		durationGate{},
		diversityGate{},
		chronologicalOrderGate{},
		qualityGate{},
		availabilityGate{},
		noDuplicatesAcrossSlotsGate{},
		transcriptVisualSummaryPresentGate{},
		formatCompatibleGate{},
		subtitleReadyGate{},
	}
}
