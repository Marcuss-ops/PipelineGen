// Package scriptgeneration — runner_nlp_concurrency.go owns the Runner's
// NLP/entity-extraction fan-out setter. Split out of runner_deps.go per the
// max_lines_per_file_strict gate.
package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"

// SetNLPConcurrency sets the NLP/entity-extraction fan-out width used by the
// SceneReadyCoordinator and the translated-NLP pass. Values <= 0 fall back to
// the certified default (DefaultNLPConcurrency).
//
// It must mirror the Ollama NLP gate capacity: the gate bounds the provider
// calls while this bound sizes the coordinator's own slot pool. Wiring only the
// gate (the pre-existing behaviour) left the coordinator hard-coded at the
// certified default, so lowering scripts.nlp_concurrency to protect the TTS/LLM
// inference budget had no effect on the fan-out that actually issues the work.
func (r *Runner) SetNLPConcurrency(concurrency int) {
	if r == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultNLPConcurrency
	}
	r.nlpConcurrency = concurrency
}

// ── Phrase-impact (extractive summary) stage metrics ────────────────────
//
// Consolidated from phrase_impact_metrics.go (2026-10-11) to keep the
// internal/capabilities/scripts production-file count inside the registered
// package_hotspots.json baseline. Both files report the research/NLP worker
// surface, so they share one home.
func observePhraseImpactTimings(timings PhraseImpactTimings) {
	stages := [...]struct {
		name string
		ms   float64
	}{
		{"split", timings.SplitMS},
		{"embedding", timings.EmbeddingMS},
		{"similarity", timings.SimilarityMS},
		{"ranking", timings.RankingMS},
		{"summary", timings.SummaryMS},
		{"bullet", timings.BulletMS},
		{"total", timings.TotalMS},
	}
	for _, stage := range stages {
		if stage.ms > 0 {
			observability.ScriptPhraseImpactStageSeconds.WithLabelValues(stage.name).Observe(stage.ms / 1000)
		}
	}
}
