// Command ner-eval evaluates one configured NER backend against an externally
// supplied, manually annotated JSON corpus. It never creates or mutates labels.
//
// Corpus format:
// {"version":"ner-corpus.v1","annotation_method":"human_double_annotation_adjudicated",
// "annotation_guidelines":"v1","annotators":["annotator-a","annotator-b"],
// "cases":[{"id":"...","language":"en","text":"...",
// "entities":[{"text":"...","label":"PERSON","start":0,"end":9}]}]}
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/nlp"
)

const (
	corpusVersion            = "ner-corpus.v1"
	defaultNERWarmupPasses   = 20
	defaultNERMeasuredPasses = 200
)

type corpus struct {
	Version              string       `json:"version"`
	AnnotationMethod     string       `json:"annotation_method"`
	AnnotationGuidelines string       `json:"annotation_guidelines"`
	Annotators           []string     `json:"annotators"`
	Cases                []corpusCase `json:"cases"`
}
type corpusCase struct {
	ID       string       `json:"id"`
	Language string       `json:"language"`
	Text     string       `json:"text"`
	Entities []goldEntity `json:"entities"`
}
type goldEntity struct {
	Text  string `json:"text"`
	Label string `json:"label"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}
type score struct {
	TruePositive  int     `json:"true_positive"`
	FalsePositive int     `json:"false_positive"`
	FalseNegative int     `json:"false_negative"`
	Precision     float64 `json:"precision"`
	Recall        float64 `json:"recall"`
	F1            float64 `json:"f1"`
}
type languageReport struct {
	Scenes   int              `json:"scenes"`
	Exact    score            `json:"exact_span_and_label"`
	Boundary score            `json:"boundary_only"`
	Labels   map[string]score `json:"labels"`
}
type report struct {
	Version              string                    `json:"version"`
	Backend              string                    `json:"backend"`
	CorpusVersion        string                    `json:"corpus_version"`
	CorpusSHA256         string                    `json:"corpus_sha256"`
	Scenes               int                       `json:"scenes"`
	GoldEntities         int                       `json:"gold_entities"`
	PredictedEntities    int                       `json:"predicted_entities"`
	SuccessfulScenes     int                       `json:"successful_scenes"`
	BackendErrors        int                       `json:"backend_errors"`
	FailedCaseIDs        []string                  `json:"failed_case_ids,omitempty"`
	EvaluationCalls      int                       `json:"evaluation_calls"`
	Exact                score                     `json:"exact_span_and_label"`
	Boundary             score                     `json:"boundary_only"`
	Labels               map[string]score          `json:"labels"`
	ByLanguage           map[string]languageReport `json:"by_language"`
	HallucinatedEntities int                       `json:"hallucinated_entities"`
	HallucinationRate    float64                   `json:"hallucination_rate"`
	InvalidOffsets       int                       `json:"invalid_offsets"`
	InvalidLabels        int                       `json:"invalid_labels"`
	InvalidOffsetRate    float64                   `json:"invalid_offset_rate"`
	UngroundedOutputs    int                       `json:"ungrounded_outputs"`
	ColdStartMS          float64                   `json:"cold_start_ms"`
	ColdStartErrors      int                       `json:"cold_start_errors"`
	WarmP50MS            float64                   `json:"warm_p50_ms"`
	WarmP95MS            float64                   `json:"warm_p95_ms"`
	WarmP99MS            float64                   `json:"warm_p99_ms"`
	ScenesPerSecond      float64                   `json:"scenes_per_second"`
	MaxRSSMB             float64                   `json:"max_rss_mb_process_and_children"`
	WarmupPasses         int                       `json:"warmup_passes"`
	WarmupCalls          int                       `json:"warmup_calls"`
	WarmupBackendErrors  int                       `json:"warmup_backend_errors"`
	Iterations           int                       `json:"measured_passes"`
	Model                string                    `json:"model,omitempty"`
	ModelLimitations     []string                  `json:"limitations,omitempty"`
	ModelInferenceP50MS  float64                   `json:"model_inference_p50_ms,omitempty"`
	ModelInferenceP95MS  float64                   `json:"model_inference_p95_ms,omitempty"`
	ModelInferenceP99MS  float64                   `json:"model_inference_p99_ms,omitempty"`
	ModelLoadMS          float64                   `json:"model_load_ms,omitempty"`
}

type spanKey struct{ start, end int }
type exactKey struct {
	start, end int
	label      string
}
type counter struct{ tp, fp, fn int }

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ner-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	corpusPath := flags.String("corpus", "", "path to manually labeled ner-corpus.v1 JSON (required)")
	backendName := flags.String("backend", "rust", "backend: rust or spacy")
	rustPath := flags.String("rust-binary", "bin/visualner", "VisualNER executable path")
	spacyURL := flags.String("spacy-url", "", "spaCy sidecar base URL; required for backend=spacy")
	warmup := flags.Int("warmup", defaultNERWarmupPasses, "number of full-corpus warm-up passes before measurement")
	iterations := flags.Int("iterations", defaultNERMeasuredPasses, "number of full-corpus measured passes")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(*corpusPath) == "" {
		return errors.New("--corpus is required; provide externally annotated gold data")
	}
	if *warmup < 0 || *warmup > 1000 {
		return fmt.Errorf("--warmup must be in 0..1000")
	}
	if *iterations < 1 || *iterations > 1000 {
		return fmt.Errorf("--iterations must be in 1..1000")
	}
	data, err := os.ReadFile(*corpusPath)
	if err != nil {
		return fmt.Errorf("read corpus: %w", err)
	}
	var gold corpus
	if err := json.Unmarshal(data, &gold); err != nil {
		return fmt.Errorf("decode corpus: %w", err)
	}
	if err := validateCorpus(gold); err != nil {
		return fmt.Errorf("invalid corpus: %w", err)
	}
	if (*warmup > 0 && len(gold.Cases) > math.MaxInt / *warmup) || len(gold.Cases) > math.MaxInt / *iterations {
		return errors.New("corpus size multiplied by pass count overflows evaluation call count")
	}
	for _, item := range gold.Cases {
		if utf8.RuneCountInString(item.Text) > 100_000 {
			return fmt.Errorf("invalid corpus: case %q text exceeds backend limit of 100000 Unicode codepoints", item.ID)
		}
	}
	*backendName = strings.ToLower(strings.TrimSpace(*backendName))
	var backend scriptgen.NERBackend
	var closeBackend func()
	switch *backendName {
	case "rust":
		adapter, adapterErr := rustexec.NewVisualNERAdapter(rustexec.NewExecutor(*rustPath, "", nil))
		if adapterErr != nil {
			return adapterErr
		}
		backend = adapter
		closeBackend = adapter.Close
	case "spacy":
		if strings.TrimSpace(*spacyURL) == "" {
			return errors.New("--spacy-url is required for --backend=spacy")
		}
		adapter, adapterErr := nlp.NewSpacyNERAdapter(*spacyURL, nil)
		if adapterErr != nil {
			return adapterErr
		}
		backend = adapter
	default:
		return fmt.Errorf("unsupported --backend %q (choose rust or spacy)", *backendName)
	}

	result := report{
		Version: "ner-evaluation.v1", Backend: *backendName, CorpusVersion: gold.Version,
		CorpusSHA256: digest.SHA256Bytes(data), Scenes: len(gold.Cases),
		WarmupPasses: *warmup, WarmupCalls: len(gold.Cases) * *warmup,
		Iterations: *iterations, EvaluationCalls: len(gold.Cases) * *iterations, Labels: map[string]score{},
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

	for pass := 0; pass < *warmup; pass++ {
		for _, item := range gold.Cases {
			if _, extractErr := backend.Extract(context.Background(), item.Language, item.Text, 1000); extractErr != nil {
				result.WarmupBackendErrors++
			}
		}
	}

	for iteration := 0; iteration < *iterations; iteration++ {
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
	if currentProcessRSS := maxRSSMB(*backendName == "rust"); currentProcessRSS > result.MaxRSSMB {
		result.MaxRSSMB = currentProcessRSS
	}
	result.ModelInferenceP50MS = percentile(inferenceLatencies, 0.50)
	result.ModelInferenceP95MS = percentile(inferenceLatencies, 0.95)
	result.ModelInferenceP99MS = percentile(inferenceLatencies, 0.99)
	if *backendName == "rust" {
		result.ModelLimitations = []string{"The selected rust backend is the deterministic VisualNER heuristic, not statistical NER."}
	} else {
		result.ModelLimitations = []string{"spaCy xx_ent_wiki_sm is trained on WikiNER; this report does not certify YouTube-script domain quality without a representative human-labeled corpus."}
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func validateCorpus(data corpus) error {
	if data.Version != corpusVersion {
		return fmt.Errorf("version must be %q", corpusVersion)
	}
	if data.AnnotationMethod != "human_double_annotation_adjudicated" {
		return errors.New("annotation_method must be human_double_annotation_adjudicated")
	}
	if strings.TrimSpace(data.AnnotationGuidelines) == "" {
		return errors.New("annotation_guidelines version is required")
	}
	annotators := make(map[string]struct{}, len(data.Annotators))
	for _, annotator := range data.Annotators {
		if id := strings.TrimSpace(annotator); id != "" {
			annotators[id] = struct{}{}
		}
	}
	if len(annotators) < 2 {
		return errors.New("at least two distinct human annotators are required")
	}
	if data.Cases == nil {
		return errors.New("cases must be provided as an array")
	}
	if len(data.Cases) < 600 {
		return fmt.Errorf("corpus must contain at least 600 human-adjudicated scenes; got %d", len(data.Cases))
	}
	if len(data.Cases) == 0 {
		return errors.New("cases must not be empty")
	}
	seen := make(map[string]struct{}, len(data.Cases))
	languageCounts := make(map[string]int, len(supportedNERLanguages))
	for index, item := range data.Cases {
		id := strings.TrimSpace(item.ID)
		if id == "" || strings.TrimSpace(item.Language) == "" || item.Text == "" || !utf8.ValidString(item.Text) {
			return fmt.Errorf("case[%d] requires id, language, and valid UTF-8 text", index)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate case id %q", id)
		}
		seen[id] = struct{}{}
		language := languageKey(item.Language)
		if _, ok := supportedNERLanguages[language]; !ok {
			return fmt.Errorf("case %q has unsupported language %q", id, item.Language)
		}
		languageCounts[language]++
		for entityIndex, entity := range item.Entities {
			label := normalizeLabel(entity.Label)
			if !validEvaluatorLabel(entity.Label) || label == "" || entity.Start < 0 || entity.End <= entity.Start || entity.End > len(item.Text) || !utf8.RuneStart(item.Text[entity.Start]) || entity.End < len(item.Text) && !utf8.RuneStart(item.Text[entity.End]) {
				return fmt.Errorf("case %q entity[%d] has invalid label or byte offsets", id, entityIndex)
			}
			if item.Text[entity.Start:entity.End] != entity.Text {
				return fmt.Errorf("case %q entity[%d] text does not match its gold span", id, entityIndex)
			}
		}
	}
	for language := range supportedNERLanguages {
		if languageCounts[language] < 100 {
			return fmt.Errorf("corpus requires at least 100 annotated scenes per supported language; %s has %d", language, languageCounts[language])
		}
	}
	return nil
}

var supportedNERLanguages = map[string]struct{}{"en": {}, "it": {}, "es": {}, "pt": {}, "fr": {}, "de": {}}

func validEvaluatorLabel(value string) bool { return normalizeLabel(value) != "" }

func normalizeLabel(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "PER", "PERSON":
		return "PERSON"
	case "ORG", "ORGANIZATION", "COMPANY", "CORP", "CORPORATION", "BUSINESS":
		return "ORG"
	case "GPE", "LOC", "LOCATION", "PLACE", "CITY", "COUNTRY":
		return "GPE"
	case "LOGO":
		return "LOGO"
	case "BRAND":
		return "ORG"
	case "DATE", "DATETIME", "CALENDAR_DATE":
		return "DATE"
	case "TIME", "CARDINAL", "NUMBER", "NUM", "METRIC", "METRICS", "STATISTIC", "STATISTICS", "QUANTITY", "ORDINAL", "MONEY", "PERCENT", "PERCENTAGE":
		return script.NormalizeAnnotationType(value)
	case "QUOTE":
		return "QUOTE"
	case "EVENT":
		return "EVENT"
	case "PRODUCT":
		return "PRODUCT"
	case "WORK", "WORK_OF_ART":
		return "WORK_OF_ART"
	case "CONCEPT", "VISUAL_CONCEPT", "MISC", "NORP", "LAW", "LANGUAGE":
		return "CONCEPT"
	default:
		return ""
	}
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
