package scriptgeneration

import (
	"context"
)

// ── Visual semantics, one worker call per scene (RUST-02) ──
//
// attachVisualBullets and attachVisualChartValues used to drive two separate
// VisualNER analyze calls for the same scene (one through BulletCandidates,
// one through ChartCandidates, with different entity limits). The unified
// SceneSemanticAnalyzer port performs ONE analyze call per scene and returns
// every grounded projection:
//
//	SemanticCandidateBundle
//	 ├── Bullets   — phrase-impact-independent sentence bullets
//	 ├── Charts    — verified numeric chart candidates
//	 └── Temporal  — every source-grounded date fact (top-N independent)
//
// The older SceneBulletAnalyzer / SceneChartAnalyzer ports remain valid for
// backwards compatibility until parity is certified; the wired production
// chain uses the bundle.

// TemporalFactCandidate is one source-grounded date fact projected by the
// Rust worker: Text is the exact byte span (Start..End) of sourceText, so
// the planner can anchor the fact to voiceover timing without a second
// parser. NormalizedDate is the ISO form (YYYY-MM-DD / YYYY-MM / YYYY) or
// empty for unresolved surfaces — no ISO value is ever invented. Precision
// is one of day/month/year/unresolved.
type TemporalFactCandidate struct {
	Text           string
	NormalizedDate string
	Precision      string
	Start          int
	End            int
	Ambiguous      bool
	Era            string
}

// NumericFactCandidate is one source-grounded numeric occurrence projected
// by the Rust worker: Text is the exact byte span (Start..End) of
// sourceText. Kind is NUMBER, PERCENT or MONEY; Value carries the strict
// parse when HasValue is set (a scaled quantity such as "2 million" ships
// HasValue=false instead of a guessed value).
type NumericFactCandidate struct {
	Text     string
	Kind     string
	Value    float64
	HasValue bool
	Start    int
	End      int
}

// SemanticCandidateBundle carries the grounded projections from one analyze
// call.
type SemanticCandidateBundle struct {
	Bullets  []BulletCandidate
	Charts   []ChartValueCandidate
	Temporal []TemporalFactCandidate
	Numeric  []NumericFactCandidate
}

// SceneSemanticAnalyzer returns the unified VisualNER semantic bundle for
// one scene's text. It is optional; nil falls back to the legacy separate
// bullet and chart analyzers.
type SceneSemanticAnalyzer interface {
	SemanticCandidates(ctx context.Context, language, sourceText string, limit int) (*SemanticCandidateBundle, error)
}
