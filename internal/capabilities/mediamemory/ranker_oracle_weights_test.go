// ── FROZEN TEST ORACLE — NOT PART OF THE PRODUCTION BINARY ───────────
// This file is the Go implementation of the canonical media ranker as it
// stood at the TICKET-16 cutover: weights, seven gates, verdict bands,
// repetition penalty, sort and the diversity pick.
//
// It is compiled ONLY into test binaries and exists for one purpose: to
// generate and check the parity fixtures in contract/media-ranker/v1 against
// `mediasampler::rank`, the one authority production calls through
// mediamemory.PoolRanker. Because it lives in a _test.go file, non-test code
// cannot link it even by accident — the build failure is the guard.
//
// Do not "fix" this file to match a Rust change. When the ranker's behaviour
// changes, the Rust side changes first, the fixtures are regenerated from the
// Rust answer, and this oracle is updated to match the new canonical facts.
// Contents: Canonical weights, verdict thresholds, band classification.

package mediamemory

// ── Default weights (canonical) ─────────────────────────────────

// DefaultRankWeights is the canonical Phase 1.x weighting. Each
// field is the canonical value per the architecture doc section 11.
// ANY change requires a godlike/06 SSOT review (typing of new
// knobs is allowed, but the canonical defaults are immutable
// until the next version bump — bumping mirrors PhraseFingerprint
// so the cache invalidates cleanly).
type DefaultRankWeights struct {
	Semantic          float64 // 0.30
	ExactMatch        float64 // 0.20
	Visual            float64 // 0.15
	ManualApproval    float64 // 0.15
	Quality           float64 // 0.10
	HistoricalSuccess float64 // 0.05
	DurationFit       float64 // 0.05
}

// Weights returns the canonical Phase 1.x weights. Tests may
// construct a custom DefaultRankWeights struct in fixtures but
// production wiring reads ONLY from this function.
func Weights() DefaultRankWeights {
	return DefaultRankWeights{
		Semantic:          0.30,
		ExactMatch:        0.20,
		Visual:            0.15,
		ManualApproval:    0.15,
		Quality:           0.10,
		HistoricalSuccess: 0.05,
		DurationFit:       0.05,
	}
}

// VerdictThresholds bundles the canonical post-rank verdict
// thresholds. Open via struct for future tuning without changing
// the constants in Weights().
type VerdictThresholds struct {
	Accept float64 // > Accept → Accept
	Drop   float64 // < Drop   → Drop (between is Downrank)
}

// DefaultVerdictThresholds returns the canonical Phase 1.x
// thresholds: Accept > 0.05, Drop < 0.0 (downrank fills the gap).
func DefaultVerdictThresholds() VerdictThresholds {
	return VerdictThresholds{Accept: 0.05, Drop: 0.0}
}

// ── Verdict classifier (math-only) ───────────────────────────────

// classifyScore maps a final score to the canonical verdict via
// the supplied thresholds. Open function so tests + future
// per-strategy Rankers can plug their own thresholds in.
func classifyScore(final float64, t VerdictThresholds) RankingVerdict {
	switch {
	case final > t.Accept:
		return VerdictAccept
	case final < t.Drop:
		return VerdictDrop
	default:
		return VerdictDownrank
	}
}

// verdictReason returns the canonical godlike/06 reason string
// for a verdict. Surface stable for log scans + dashboard audit.
func verdictReason(v RankingVerdict) string {
	switch v {
	case VerdictAccept:
		return "score above accept threshold"
	case VerdictDownrank:
		return "score within downrank band"
	case VerdictDrop:
		return "score below drop threshold (penalty or malformed)"
	default:
		return "unknown verdict"
	}
}
