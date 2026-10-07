package main

import (
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

type languageAccumulator struct {
	scenes          int
	exact, boundary *counter
	labels          map[string]*counter
}

func languageKey(language string) string {
	return strings.ToLower(strings.Split(strings.TrimSpace(language), "-")[0])
}

func ensureLanguageAccumulator(languages map[string]*languageAccumulator, language string) *languageAccumulator {
	lang := languageKey(language)
	acc := languages[lang]
	if acc == nil {
		acc = &languageAccumulator{exact: &counter{}, boundary: &counter{}, labels: make(map[string]*counter)}
		languages[lang] = acc
	}
	return acc
}

func observe(item corpusCase, predicted []scriptgen.VisualEntity, exact, boundary *counter, labels map[string]*counter, result *report, languages map[string]*languageAccumulator) {
	acc := ensureLanguageAccumulator(languages, item.Language)
	result.PredictedEntities += len(predicted)
	acc.scenes++
	goldExact := make(map[exactKey]int)
	goldBoundary := make(map[spanKey]int)
	for _, entity := range item.Entities {
		label := normalizeLabel(entity.Label)
		goldExact[exactKey{entity.Start, entity.End, label}]++
		goldBoundary[spanKey{entity.Start, entity.End}]++
	}
	predExact := make(map[exactKey]int)
	predBoundary := make(map[spanKey]int)
	invalidPredictions := 0
	for _, entity := range predicted {
		valid := entity.Start >= 0 && entity.End > entity.Start && entity.End <= len(item.Text)
		if valid {
			valid = utf8.RuneStart(item.Text[entity.Start]) && (entity.End == len(item.Text) || utf8.RuneStart(item.Text[entity.End])) && utf8.ValidString(item.Text[entity.Start:entity.End]) && item.Text[entity.Start:entity.End] == entity.Text
		}
		if !valid {
			result.InvalidOffsets++
			result.UngroundedOutputs++
			result.HallucinatedEntities++
			invalidPredictions++
			continue
		}
		if entity.Evidence != "" && entity.Evidence != item.Text[entity.Start:entity.End] {
			result.UngroundedOutputs++
			result.HallucinatedEntities++
			invalidPredictions++
			continue
		}
		if !validEvaluatorLabel(string(entity.Type)) {
			result.InvalidLabels++
			invalidPredictions++
			continue
		}
		label := normalizeLabel(string(entity.Type))
		key := exactKey{entity.Start, entity.End, label}
		predExact[key]++
		predBoundary[spanKey{entity.Start, entity.End}]++
	}
	accExact, accBoundary := &counter{}, &counter{}
	compareMultisets(goldExact, predExact, accExact)
	compareMultisets(goldBoundary, predBoundary, accBoundary)
	accExact.fp += invalidPredictions
	accBoundary.fp += invalidPredictions
	if invalidPredictions > 0 {
		if labels["INVALID_OUTPUT"] == nil {
			labels["INVALID_OUTPUT"] = &counter{}
		}
		if acc.labels["INVALID_OUTPUT"] == nil {
			acc.labels["INVALID_OUTPUT"] = &counter{}
		}
		labels["INVALID_OUTPUT"].fp += invalidPredictions
		acc.labels["INVALID_OUTPUT"].fp += invalidPredictions
	}
	addCounter(exact, accExact)
	addCounter(boundary, accBoundary)
	addCounter(acc.exact, accExact)
	addCounter(acc.boundary, accBoundary)
	labelNames := make(map[string]struct{})
	for key := range goldExact {
		labelNames[key.label] = struct{}{}
	}
	for key := range predExact {
		labelNames[key.label] = struct{}{}
	}
	for label := range labelNames {
		labelGold, labelPred := make(map[exactKey]int), make(map[exactKey]int)
		for key, count := range goldExact {
			if key.label == label {
				labelGold[key] = count
			}
		}
		for key, count := range predExact {
			if key.label == label {
				labelPred[key] = count
			}
		}
		counts := &counter{}
		compareMultisets(labelGold, labelPred, counts)
		if labels[label] == nil {
			labels[label] = &counter{}
		}
		if acc.labels[label] == nil {
			acc.labels[label] = &counter{}
		}
		addCounter(labels[label], counts)
		addCounter(acc.labels[label], counts)
	}
}

// observeBackendFailure treats a failed extraction as zero predictions for its
// corpus case, so expected entities count as false negatives rather than
// vanishing from the quality denominator.
func observeBackendFailure(item corpusCase, exact, boundary *counter, labels map[string]*counter, languages map[string]*languageAccumulator) {
	acc := ensureLanguageAccumulator(languages, item.Language)
	acc.scenes++
	accExact, accBoundary := &counter{}, &counter{}
	for _, entity := range item.Entities {
		label := normalizeLabel(entity.Label)
		accExact.fn++
		accBoundary.fn++
		if labels[label] == nil {
			labels[label] = &counter{}
		}
		if acc.labels[label] == nil {
			acc.labels[label] = &counter{}
		}
		labels[label].fn++
		acc.labels[label].fn++
	}
	addCounter(exact, accExact)
	addCounter(boundary, accBoundary)
	addCounter(acc.exact, accExact)
	addCounter(acc.boundary, accBoundary)
}

func compareMultisets[K comparable](gold, predicted map[K]int, counts *counter) {
	for key, count := range predicted {
		tp := min(count, gold[key])
		counts.tp += tp
		counts.fp += count - tp
	}
	for key, count := range gold {
		if missing := count - predicted[key]; missing > 0 {
			counts.fn += missing
		}
	}
}
func addCounter(dst, src *counter) { dst.tp += src.tp; dst.fp += src.fp; dst.fn += src.fn }
func makeScore(value *counter) score {
	out := score{TruePositive: value.tp, FalsePositive: value.fp, FalseNegative: value.fn}
	if value.tp+value.fp > 0 {
		out.Precision = float64(value.tp) / float64(value.tp+value.fp)
	}
	if value.tp+value.fn > 0 {
		out.Recall = float64(value.tp) / float64(value.tp+value.fn)
	}
	if out.Precision+out.Recall > 0 {
		out.F1 = 2 * out.Precision * out.Recall / (out.Precision + out.Recall)
	}
	return out
}
func scores(counts map[string]*counter) map[string]score {
	out := make(map[string]score, len(counts))
	for label, count := range counts {
		out[label] = makeScore(count)
	}
	return out
}
func percentile(values []float64, q float64) float64 {
	if q < 0 || q > 1 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	if len(ordered) == 0 {
		return 0
	}
	index := int(float64(len(ordered)-1)*q + 0.5)
	return ordered[index]
}
func maxRSSMB(includeChildren bool) float64 {
	var self, children syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &self) != nil {
		return 0
	}
	peak := self.Maxrss
	if includeChildren && syscall.Getrusage(syscall.RUSAGE_CHILDREN, &children) == nil && children.Maxrss > peak {
		peak = children.Maxrss
	}
	return float64(peak) / 1024
}
