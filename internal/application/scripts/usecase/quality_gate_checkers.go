// Package usecase — quality_gate_checkers.go
//
// The editorial rules of the quality gate, modelled as an ordered
// registry of single-purpose checkers. evaluateQualityGate computes the
// GenerationQuality metrics first and then runs every rule in registry
// order, concatenating the failure reasons (the order matches the
// historical reason ordering surfaced in QualityGateError).
package usecase

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

// qualityGateInput is the context a single rule needs to emit its
// failure reasons. Metrics (q) are computed by the orchestrator before
// the rules run; thresholds are resolved per grounding policy.
type qualityGateInput struct {
	result       *scriptpkg.GenerationResult
	plan         scriptpkg.ResolvedGenerationPlan
	q            *scriptpkg.GenerationQuality
	sourceText   string
	minSourceCov float64
	minClipCov   float64
}

// qualityChecker is a single editorial rule in the quality-gate
// registry. Check returns the failure reasons for the rule, or nil when
// the rule passes; the orchestrator concatenates them in registry order.
type qualityChecker interface {
	// Name is the canonical rule identifier (registry tests + logs).
	Name() string
	// Check evaluates the rule against the computed metrics.
	Check(in qualityGateInput) []string
}

// qualityGateRules is the ordered rule registry. Adding a rule here
// automatically gates every /api/script/generate run; rules live in
// their domain files (language, coverage, claims, segments, words).
var qualityGateRules = []qualityChecker{
	languageMatchChecker{},
	sourceCoverageChecker{},
	clipCoverageChecker{},
	unsupportedClaimsChecker{},
	researchCandidateCoverageChecker{},
	segmentNarrativeChecker{},
	targetWordsChecker{},
}
