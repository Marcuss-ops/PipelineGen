// Package mediamemory — ranker_gates.go is the canonical home
// for the pre-rank gate surface: the seven mandatory gates
// (PreRankGate type + 7 closed-set constants), the unified gate
// runner (RunMandatoryGates), the well-formed guard
// (mediaCandidateIsWellFormed), the per-gate helpers
// (HasAvailableMedia / HasValidDuration / HasSupportedFormat),
// the canonical duration-fit score (ComputeDurationFit), and the
// godlike/07 fail-closed rights-penalty mapping
// (PopulateRightsPenalty).
//
// godlike/06 SSOT (single canonical home per concern): every
// pre-rank decision lives here so the gate vocabulary + the
// gate evaluation order stay grep-able in one place. Tests pin
// the closed-set gate-name vocabulary at the resolver seam;
// drift here surfaces as a dashboard audit key mismatch.
//
// godlike/06 SSOT (gate order, canonical): RunMandatoryGates
// evaluates gates in the canonical order below so log scans +
// dashboard diagnostics see a stable drop reason. Format
// precedes aspect because aspect ratio is a structural property
// of visual media only (audio has no aspect ratio).
//
// Mandatory pre-rank filters (godlike/07) — CANONICAL seven gates
// (Fase 1.5 SSOT, architecture-doc section 11):
//
//  1. valid_license        — RightsStatus == RightsVerified
//  2. available_media      — MaterializationStatus ∈ {Warm, Hot}
//  3. valid_duration       — DurationMs >= 0
//  4. supported_format     — MediaType ∈ {video, image, audio, music}
//     (empty MediaType is the legacy ambiguous
//     sentinel and bypasses the gate for
//     forward-compat)
//  5. compatible_aspect    — AspectRatioW/AspectRatioH ∈ canonical set
//  6. no_corruption        — IntegrityChecked == true
//  7. no_duplicates        — DuplicateOfAssetID == ""
//
// godlike/07 NO-FAKE-AVAILABILITY: Filter() is the canonical
// seam for the seven mandatory gates. A pass-through silently
// (without dropping) would be PR-REGRESSION — explicit drop-
// with-nil envelopes a no-op pass.
//
// File split ownership (godlike/06 SSOT):
//   - ranker.go               : Ranker port + defaultRanker + NewDefaultRanker + Score + Filter
//   - ranker_types.go         : RankingInput + RankingOutput + RankingVerdict + FilteredCandidate
//   - ranker_weights.go       : DefaultRankWeights + Weights() + VerdictThresholds + DefaultVerdictThresholds + classifyScore + verdictReason
//   - ranker_gates.go         : PreRankGate + 7 gates + RunMandatoryGates + HasAvailableMedia/HasValidDuration/HasSupportedFormat/mediaCandidateIsWellFormed + ComputeDurationFit + PopulateRightsPenalty  ← this file
//   - ranker_repetition.go    : AntiRepetitionHistoryLimit + 4 channel-recency/ceiling consts + DiversityFinalScoreDelta + RepetitionPenaltyWeights + DefaultRepetitionPenaltyWeights + PopulateRepetitionPenalty + PickTopFromRose + extractCandidateVideoID/ChannelID
package mediamemory

// ── Mandatory gates (Fase "Ranking & rights") ────────────────────

// PreRankGate is the canonical closed-set enum for the six
// mandatory pre-rank gates the architecture doc (section 11)
// requires. godlike/06 SSOT: every gate has a canonical name
// + reason so log scans + dashboard diagnostics can audit
// drops without parsing the error string.
type PreRankGate string

const (
	// GateValidLicense — RightsStatus MUST be RightsVerified.
	GateValidLicense PreRankGate = "valid_license"
	// GateAvailableMedia — MaterializationStatus MUST be Warm
	// or Hot (Cold / Failed drop).
	GateAvailableMedia PreRankGate = "available_media"
	// GateValidDuration — DurationMs MUST be non-negative
	// (DurationMs == 0 is the canonical image/document
	// binding sentinel per HasValidDuration so 0 is accepted).
	GateValidDuration PreRankGate = "valid_duration"
	// GateSupportedFormat — MediaType MUST be in the canonical
	// set {video, image, audio, music}. Empty MediaType is the
	// legacy ambiguous sentinel and bypasses the gate
	// (forward-compat for pre-Fase-1.5 fixtures).
	GateSupportedFormat PreRankGate = "supported_format"
	// GateCompatibleAspect — AspectRatioW / AspectRatioH MUST
	// be 16:9 / 4:3 / 1:1 / 9:16 (canonical set). Folds
	// MediaType out to GateSupportedFormat so each gate has a
	// single owner per godlike/06 SSOT.
	GateCompatibleAspect PreRankGate = "compatible_aspect"
	// GateNoCorruption — IntegrityChecked MUST be true.
	GateNoCorruption PreRankGate = "no_corruption"
	// GateNoDuplicates — DuplicateOfAssetID MUST be empty.
	GateNoDuplicates PreRankGate = "no_duplicates"
)

// RunMandatoryGates applies the canonical seven pre-rank gates
// (Fase 1.5 SSOT) and returns the first failing gate (empty
// string = all passed). godlike/06 SSOT (narrow contract): the
// helper is math-only (no IO); the resolver pre-populates the
// four boolean flags on FilteredCandidate + the structural fields
// on MediaCandidate before calling this helper. godlike/07
// NO-FAKE-AVAILABILITY: a candidate failing ANY gate MUST NOT
// proceed to Score.
//
// Gate evaluation order is canonical (matches the constants'
// declaration order):
//  1. GateNoDuplicates    (IsDuplicate flag)
//  2. GateValidLicense    (MissingRights flag — set by resolver
//     when RightsStatus != RightsVerified)
//  3. GateNoCorruption    (Contaminated flag — set by the
//     integrity checker worker)
//  4. GateAvailableMedia  (MaterializationStatus, computed here)
//  5. GateValidDuration   (DurationMs, computed here)
//  6. GateSupportedFormat (MediaType, computed here; Fase 1.5)
//  7. GateCompatibleAspect (AspectMismatch flag — set by resolver)
//
// Flags 2, 6, 7 are resolver-set; flags 1, 3 are worker-set;
// fields 4, 5 are computed in this helper against MediaCandidate.
// godlike/06 SSOT (single owner per fact): every gate has exactly
// one computation site, here.
func RunMandatoryGates(fc FilteredCandidate) PreRankGate {
	if fc.IsDuplicate {
		return GateNoDuplicates
	}
	if fc.MissingRights {
		return GateValidLicense
	}
	if fc.Contaminated {
		return GateNoCorruption
	}
	if !HasAvailableMedia(fc.Candidate) {
		return GateAvailableMedia
	}
	if !HasValidDuration(fc.Candidate) {
		return GateValidDuration
	}
	if !HasSupportedFormat(fc.Candidate) {
		return GateSupportedFormat
	}
	if fc.AspectMismatch {
		return GateCompatibleAspect
	}
	return ""
}

// HasSupportedFormat reports whether a candidate's MediaType is in
// the canonical supported-set {video, image, audio, music}.
// godlike/06 SSOT (canonical format set): adding a new supported
// MediaType requires both a code change here AND an update to the
// IsKnownMediaType helper (per the architectural doc section 11).
//
// godlike/06 SSOT (forward-compat bypass): empty MediaType is the
// legacy ambiguous sentinel for candidates produced before the
// MediaType field was populated; the gate bypasses so historical
// fixtures remain testable until a Fase 1.x migration backfills
// MediaType. A future Fase draft may flip the empty-string branch
// to fail-closed once all upstream discovery workers emit MediaType.
//
// godlike/07 NO-FAKE-AVAILABILITY: a non-canonical MediaType (e.g.
// "vrm", "raw", "") used to be passed silently; now this helper
// gates it so the ranker never credits an unknown MediaType.
func HasSupportedFormat(c MediaCandidate) bool {
	if c.MediaType == "" {
		return true // legacy ambiguous sentinel bypass
	}
	switch c.MediaType {
	case "video", "image", "audio", "music":
		return true
	default:
		return false
	}
}

// mediaCandidateIsWellFormed enforces the minimal wire-shape
// guard before the ranker attempts math. A malformed envelope
// MUST short-circuit; downstream rendering cannot use a candidate
// without an AssetID.
//
// The list of "well-formed" fields is intentionally minimal —
// the type-level required fields are already enforced at compile
// time. Runtime guard exists only to catch empty-AssetID drift.
func mediaCandidateIsWellFormed(c MediaCandidate) bool {
	if c.AssetID == "" {
		return false
	}
	if !IsKnownMaterializationStatus(c.MaterializationStatus) {
		return false
	}
	if !IsKnownDiscoveryStatus(c.DiscoveryStatus) {
		return false
	}
	return true
}

// ── Ranking & rights: canonical helpers ────────────────────────────

// ComputeDurationFit returns the canonical [0,1] duration_fit
// score for a (sceneDurationMs, bindingDurationMs) pair.
// godlike/06 SSOT (closed-form formula): the helper favours
// bindings whose duration matches the scene (ratio ≈ 1.0);
// bindings whose duration is wildly off (ratio < 0.5 or > 2.0)
// receive 0.0 so the ranker drops them at VerdictDownrank/Drop.
// godlike/07 NO-FAKE-AVAILABILITY: zero or negative durations
// return 0.0 (the ranker's mandatory duration_valid gate will
// independently reject DurationMs <= 0 candidates before this
// helper is consulted).
//
// Scale: ratio == 1.0 → 1.0; ratio == 0.5 or 2.0 → 0.0; linear
// interpolation between (0.5, 0.0) and (1.0, 1.0) on the
// short-clip side, and between (1.0, 1.0) and (2.0, 0.0) on the
// long-clip side. The formula is symmetric around ratio 1.0
// so a binding that is half the scene duration scores the same
// as a binding that is twice the scene duration.
func ComputeDurationFit(sceneDurationMs, bindingDurationMs int64) float64 {
	if sceneDurationMs <= 0 || bindingDurationMs <= 0 {
		return 0.0
	}
	ratio := float64(bindingDurationMs) / float64(sceneDurationMs)
	if ratio < 0.5 || ratio > 2.0 {
		return 0.0
	}
	// Linear interpolation: at ratio=1.0 → 1.0; at the
	// boundaries (0.5, 2.0) → 0.0.
	if ratio < 1.0 {
		return 1.0 - (1.0-ratio)*2.0
	}
	return 1.0 - (ratio-1.0)*2.0
}

// PopulateRightsPenalty applies the canonical per-RightsStatus
// penalty stamp onto RankingInput.RightsPenalty. godlike/06 SSOT
// (closed-set mapping) — godlike/07 fail-closed (Fase 1.5):
//
//	RightsVerified → 0.0  (no penalty; Resolver MissingRights gate
//	                    already rejects non-Verified upstream as
//	                    defense in depth — this path is the
//	                    happy-case)
//	RightsUnknown  → 1.0  (FAIL-CLOSED: unverified rights receive
//	                    full penalty, guaranteeing VerdictDrop.
//	                    The Hot-tier guard in batch_service.
//	                    MarkMaterialized already requires RightsVerified
//	                    for promotion; this penalty is the
//	                    ranker's second-line guarantee so a
//	                    candidate slipping past Filter still
//	                    cannot auto-promote to Hot.)
//	RightsDenied   → 1.0  (full penalty; guarantees VerdictDrop
//	                    — the MissingRights gate in Filter already
//	                    rejects this case upstream)
//	RightsExpired  → 1.0  (FAIL-CLOSED: expired rights also
//	                    receive full penalty per Fase 1.5
//	                    godlike/07 policy — the architecture
//	                    doc treats unverified/expired/denied
//	                    uniformly as "no positive rights
//	                    attestation" and the ranker must not
//	                    promote them. The caller MUST refresh
//	                    rights_status before the next render.)
//
// godlike/06 SSOT (immutable input contract, mirrors
// PopulateRepetitionPenalty): the function does NOT mutate the
// input slice — outputs are returned as a NEW slice with the
// input's candidates copied through and the penalty seat
// re-stamped. Callers MUST use the returned slice.
func PopulateRightsPenalty(inputs []RankingInput) []RankingInput {
	out := make([]RankingInput, 0, len(inputs))
	for _, in := range inputs {
		switch in.Candidate.RightsStatus {
		case RightsVerified:
			in.RightsPenalty = 0.0
		case RightsUnknown, RightsDenied, RightsExpired:
			// godlike/07 fail-closed (Fase 1.5): any
			// non-Verified rights status applies full
			// penalty. The MissingRights filter gate
			// already drops these upstream; this
			// penalty is the second-line guarantee
			// so no candidate with unverified/expired/
			// denied rights can sneak into Hot.
			in.RightsPenalty = 1.0
		}
		out = append(out, in)
	}
	return out
}

// HasAvailableMedia reports whether a candidate's materialization
// tier is sufficient for the ranker's mandatory media_available
// gate. godlike/06 SSOT (tier SSOT): Cold candidates are metadata-
// only (no bytes available), Warm candidates have bytes on Drive
// or segmentable on-demand, Hot candidates are staged locally.
// godlike/07 NO-FAKE-AVAILABILITY: a candidate at Cold tier
// cannot be rendered — the ranker MUST drop it before scoring.
func HasAvailableMedia(c MediaCandidate) bool {
	return c.MaterializationStatus == MaterializationHot ||
		c.MaterializationStatus == MaterializationWarm
}

// HasValidDuration reports whether the candidate's DurationMs is
// non-negative. godlike/06 SSOT (canonical gate): negative
// DurationMs is treated as a malformed envelope and is dropped.
// DurationMs == 0 is the canonical image/document binding sentinel
// and is accepted so image bindings and unmeasured clips are not
// silently discarded before the duration_fit scoring stage.
func HasValidDuration(c MediaCandidate) bool {
	return c.DurationMs >= 0
}
