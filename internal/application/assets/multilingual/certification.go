package multilingual

// certification.go — canonical end-of-run certification report.
//
// The report is the agreed "10 clip parallel certification" schema: inputs /
// completed / validated / failed, then the five measured dimensions
// (parallelism, performance, resources, cache, operations) and a single
// result verdict. It is a pure projection of RunMetrics (the metrics already
// confluent in the canonical performance registry) — no second metrics system,
// no ad-hoc recomputation of facts the recorder already captured.

// CertificationReport is the final certification summary for one multilingual
// render run (one source clip fanned out to N language variants).
type CertificationReport struct {
	Test        string          `json:"test"`
	Inputs      int             `json:"inputs"`
	Completed   int             `json:"completed"`
	Validated   int             `json:"validated"`
	Failed      int             `json:"failed"`
	Parallelism ParallelismCert `json:"parallelism"`
	Performance PerformanceCert `json:"performance"`
	Resources   ResourcesCert   `json:"resources"`
	Cache       CacheCert       `json:"cache"`
	Operations  OperationsCert  `json:"operations"`
	Result      string          `json:"result"`
}

// ParallelismCert is the reconstructed real concurrency of the render fan-out
// (configured workers vs observed peak vs average parallelism).
type ParallelismCert struct {
	Configured  int     `json:"configured"`
	MaxObserved int     `json:"max_observed"`
	AvgObserved float64 `json:"avg_observed"`
}

// PerformanceCert separates wall time from summed per-operation work: their
// ratio is the measured parallel speedup, never inferred from one number.
type PerformanceCert struct {
	WallMS             int64   `json:"wall_ms"`
	SumOperationMS     int64   `json:"sum_operation_ms"`
	SpeedupVsSerial    float64 `json:"speedup_vs_serial"`
	ParallelEfficiency float64 `json:"parallel_efficiency"`
}

// ResourcesCert is the process-level resource footprint of the run.
type ResourcesCert struct {
	CPUUserMS    int64 `json:"cpu_user_ms"`
	CPUSystemMS  int64 `json:"cpu_system_ms"`
	PeakRSSBytes int64 `json:"peak_rss_bytes"`
}

// CacheCert is the cache reuse certificate. AvoidedWorkMS is 0 on a cold or
// forced run (nothing reused); on a warm run it is the render work the cache
// skipped (measured by the cold-vs-warm comparison in renderer_cache_test.go).
type CacheCert struct {
	Hits          int64 `json:"hits"`
	Misses        int64 `json:"misses"`
	AvoidedWorkMS int64 `json:"avoided_work_ms"`
}

// OperationsCert is the zero-duplicate-work certificate: shared work
// (download/probe/transcribe) runs at most once; fan-out work
// (translation/ass/render/validate/upload) runs once per language.
type OperationsCert struct {
	DownloadExecCount    int64 `json:"download_exec_count"`
	ProbeExecCount       int64 `json:"probe_exec_count"`
	TranscribeExecCount  int64 `json:"transcribe_exec_count"`
	TranslationExecCount int64 `json:"translation_exec_count"`
	AssExecCount         int64 `json:"ass_exec_count"`
	RenderExecCount      int64 `json:"render_exec_count"`
	ValidateExecCount    int64 `json:"validate_exec_count"`
	UploadExecCount      int64 `json:"upload_exec_count"`
}

// BuildCertification projects RunMetrics into the canonical certification
// report. validated is the number of language variants whose output-contract
// validation returned "ok"; avoidedWorkMS is the cache-avoided render work
// (0 for a cold/forced run). result is PASS only when every input completed
// and validated with zero failures.
func BuildCertification(summary RunMetrics, validated int, avoidedWorkMS int64) CertificationReport {
	rep := CertificationReport{
		Test:      "10_clip_parallel_certification",
		Inputs:    summary.ClipCount,
		Completed: summary.SuccessCount,
		Validated: validated,
		Failed:    summary.FailedCount,
		Parallelism: ParallelismCert{
			Configured:  summary.RenderConcurrency.Configured,
			MaxObserved: summary.RenderConcurrency.MaxObserved,
			AvgObserved: summary.RenderConcurrency.AvgObserved,
		},
		Performance: PerformanceCert{
			WallMS:         summary.WallMS,
			SumOperationMS: summary.SumOperationMS,
		},
		Resources: ResourcesCert{
			CPUUserMS:    summary.CPUUserMS,
			CPUSystemMS:  summary.CPUSystemMS,
			PeakRSSBytes: summary.PeakRSSBytes,
		},
		Cache: CacheCert{
			Hits:          summary.CacheHits,
			Misses:        summary.CacheMisses,
			AvoidedWorkMS: avoidedWorkMS,
		},
		Operations: OperationsCert{
			DownloadExecCount:    summary.Operations.Download,
			ProbeExecCount:       summary.Operations.Probe,
			TranscribeExecCount:  summary.Operations.Transcribe,
			TranslationExecCount: summary.Operations.Translate + summary.Operations.TranslateFullText,
			AssExecCount:         summary.Operations.ASS,
			RenderExecCount:      summary.Operations.Render,
			ValidateExecCount:    summary.Operations.Validate,
			UploadExecCount:      summary.Operations.Upload,
		},
	}

	// speedup_vs_serial = sum_operation_ms / wall_ms (the fan-out overlap gain).
	// parallel_efficiency = speedup / worker_limit (1.0 = perfect scaling).
	if summary.WallMS > 0 {
		rep.Performance.SpeedupVsSerial = float64(summary.SumOperationMS) / float64(summary.WallMS)
	}
	if rep.Performance.SpeedupVsSerial > 0 && summary.WorkerLimit > 0 {
		rep.Performance.ParallelEfficiency = rep.Performance.SpeedupVsSerial / float64(summary.WorkerLimit)
	}

	if rep.Failed == 0 && rep.Completed == rep.Inputs && rep.Validated == rep.Inputs {
		rep.Result = "PASS"
	} else {
		rep.Result = "FAIL"
	}
	return rep
}
