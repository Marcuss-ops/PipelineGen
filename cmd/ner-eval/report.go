package main

// score is one precision/recall/F1 triple over a single matching mode (exact
// span+label, boundary-only span, or a single label).
type score struct {
	TruePositive  int     `json:"true_positive"`
	FalsePositive int     `json:"false_positive"`
	FalseNegative int     `json:"false_negative"`
	Precision     float64 `json:"precision"`
	Recall        float64 `json:"recall"`
	F1            float64 `json:"f1"`
}

// languageReport aggregates the per-language slice of a run.
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
