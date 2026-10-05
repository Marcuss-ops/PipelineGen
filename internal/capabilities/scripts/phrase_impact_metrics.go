// phrase_impact_metrics.go publishes the Rust extractive-summary worker's own
// stage breakdown. The extractive summary is an optional data product, so its
// telemetry is best-effort and never affects the run: a worker that reported no
// breakdown yields no samples at all, which keeps "not measured" distinct from
// a fabricated 0 second observation.
//
// The stage that matters is `embedding`: every other stage is local CPU work on
// the already-split sentences, while the embedding stage is the E5 passage
// embedder (a local model or an HTTP service). Without this series a deployment
// cannot tell whether the phrase-impact step costs microseconds or seconds.
package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"

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
