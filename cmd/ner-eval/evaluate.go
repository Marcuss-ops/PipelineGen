package main

import (
	"context"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/nlp"
)

// evaluationConfig carries the immutable inputs of one benchmark run.
type evaluationConfig struct {
	backendName  string
	warmup       int
	iterations   int
	corpusSHA256 string
}

// evaluate runs the cold-start probe, the warm-up passes and the measured
// passes against backend, then assembles the scored report. closeBackend is
// invoked after measurement but before the peak-RSS sample so the backend's
// resident footprint is still attributed to the run.
func evaluate(backend scriptgen.NERBackend, closeBackend func(), gold corpus, cfg evaluationConfig) report {
	result := report{
		Version: "ner-evaluation.v1", Backend: cfg.backendName, CorpusVersion: gold.Version,
		CorpusSHA256: cfg.corpusSHA256, Scenes: len(gold.Cases),
		WarmupPasses: cfg.warmup, WarmupCalls: len(gold.Cases) * cfg.warmup,
		Iterations: cfg.iterations, EvaluationCalls: len(gold.Cases) * cfg.iterations, Labels: map[string]score{},
		ByLanguage: map[string]languageReport{},
	}
	labels := make(map[string]*counter)
	exactAll, boundaryAll := &counter{}, &counter{}
	perLanguage := make(map[string]*languageAccumulator)
	var latencies []float64
	var totalDuration time.Duration
	var inferenceLatencies []float64
	var selectedSpacy *nlp.SpacyNERAdapter
	if adapter, ok := backend.(*nlp.SpacyNERAdapter); ok {
		selectedSpacy = adapter
	}
	// The cold-start request measures startup/model-load latency separately;
	// it is excluded from warm-up and measured quality/timing passes.
	coldStarted := time.Now()
	_, coldErr := backend.Extract(context.Background(), gold.Cases[0].Language, gold.Cases[0].Text, 1000)
	result.ColdStartMS = float64(time.Since(coldStarted).Microseconds()) / 1000
	if coldErr != nil {
		result.ColdStartErrors++
	}
	if selectedSpacy != nil {
		timing := selectedSpacy.LastInferenceTiming()
		result.Model = timing.Model
		if timing.ModelLoadMS > 0 {
			result.ModelLoadMS = timing.ModelLoadMS
		}
		if timing.SidecarMaxRSSMB > result.MaxRSSMB {
			result.MaxRSSMB = timing.SidecarMaxRSSMB
		}
	}

	for pass := 0; pass < cfg.warmup; pass++ {
		for _, item := range gold.Cases {
			if _, extractErr := backend.Extract(context.Background(), item.Language, item.Text, 1000); extractErr != nil {
				result.WarmupBackendErrors++
			}
		}
	}

	for iteration := 0; iteration < cfg.iterations; iteration++ {
		for _, item := range gold.Cases {
			started := time.Now()
			predicted, extractErr := backend.Extract(context.Background(), item.Language, item.Text, 1000)
			elapsed := time.Since(started)
			latencies = append(latencies, float64(elapsed.Microseconds())/1000)
			totalDuration += elapsed
			if extractErr != nil {
				result.BackendErrors++
				if iteration == 0 {
					result.FailedCaseIDs = append(result.FailedCaseIDs, item.ID)
					result.GoldEntities += len(item.Entities)
					observeBackendFailure(item, exactAll, boundaryAll, labels, perLanguage)
				}
				continue
			}
			if selectedSpacy != nil {
				timing := selectedSpacy.LastInferenceTiming()
				if timing.InferenceMS > 0 {
					inferenceLatencies = append(inferenceLatencies, timing.InferenceMS)
				}
				if timing.SidecarMaxRSSMB > result.MaxRSSMB {
					result.MaxRSSMB = timing.SidecarMaxRSSMB
				}
			}
			if iteration == 0 {
				result.SuccessfulScenes++
				result.GoldEntities += len(item.Entities)
				observe(item, predicted, exactAll, boundaryAll, labels, &result, perLanguage)
			}
		}
	}
	result.Exact = makeScore(exactAll)
	result.Boundary = makeScore(boundaryAll)
	if result.PredictedEntities > 0 {
		result.HallucinationRate = float64(result.HallucinatedEntities) / float64(result.PredictedEntities)
		result.InvalidOffsetRate = float64(result.InvalidOffsets) / float64(result.PredictedEntities)
	}
	for label, counts := range labels {
		result.Labels[label] = makeScore(counts)
	}
	for lang, acc := range perLanguage {
		result.ByLanguage[lang] = languageReport{Scenes: acc.scenes, Exact: makeScore(acc.exact), Boundary: makeScore(acc.boundary), Labels: scores(acc.labels)}
	}
	result.WarmP50MS = percentile(latencies, 0.50)
	result.WarmP95MS = percentile(latencies, 0.95)
	result.WarmP99MS = percentile(latencies, 0.99)
	if totalDuration > 0 {
		result.ScenesPerSecond = float64(len(latencies)) / totalDuration.Seconds()
	}
	if closeBackend != nil {
		closeBackend()
	}
	if currentProcessRSS := maxRSSMB(cfg.backendName == "rust"); currentProcessRSS > result.MaxRSSMB {
		result.MaxRSSMB = currentProcessRSS
	}
	result.ModelInferenceP50MS = percentile(inferenceLatencies, 0.50)
	result.ModelInferenceP95MS = percentile(inferenceLatencies, 0.95)
	result.ModelInferenceP99MS = percentile(inferenceLatencies, 0.99)
	if cfg.backendName == "rust" {
		result.ModelLimitations = []string{"The selected rust backend is the deterministic VisualNER heuristic, not statistical NER."}
	} else {
		result.ModelLimitations = []string{"spaCy xx_ent_wiki_sm is trained on WikiNER; this report does not certify YouTube-script domain quality without a representative human-labeled corpus."}
	}
	return result
}
