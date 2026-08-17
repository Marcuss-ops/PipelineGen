// Package performance implements the read-only performance projection for the
// script generation pipeline. It maps the canonical observability contract
// (internal/kernel/observability.RunReport), the canonical audio timing
// contract (scriptgeneration.AudioPipelineMetrics), and the runner's execution
// steps into one comparable per-job performance report.
//
// The package is deliberately read-only: it never writes timings, never
// fabricates missing values, and never adds a competing timing system. A phase
// whose canonical source is absent is reported as unmeasured (Measured=false)
// instead of being estimated.
package performance

// PerformancePhase is the canonical identifier of one comparable pipeline
// phase. It is the single registry shared by reports, CLI, tests, and any
// future API surface — phase names must not be invented at call sites.
type PerformancePhase string

const (
	PhaseScriptGemma   PerformancePhase = "script_gemma"
	PhaseEdgeTTS       PerformancePhase = "edge_tts"
	PhaseAudioPrepare  PerformancePhase = "audio_prepare"
	PhaseAudioPlan     PerformancePhase = "audio_plan_compile"
	PhaseRustMix       PerformancePhase = "rust_mix"
	PhaseRustEncode    PerformancePhase = "rust_encode"
	PhaseProbe         PerformancePhase = "probe"
	PhaseHash          PerformancePhase = "hash"
	PhaseUpload        PerformancePhase = "upload"
	PhaseGoogleDoc     PerformancePhase = "google_doc"
	PhaseRenderEnqueue PerformancePhase = "render_enqueue"
)

// Phases returns the canonical phase list in report order. Consumers that
// need a stable cross-job ordering must read this list instead of inventing
// one locally.
func Phases() []PerformancePhase {
	return []PerformancePhase{
		PhaseScriptGemma,
		PhaseEdgeTTS,
		PhaseAudioPrepare,
		PhaseAudioPlan,
		PhaseRustMix,
		PhaseRustEncode,
		PhaseProbe,
		PhaseHash,
		PhaseUpload,
		PhaseGoogleDoc,
		PhaseRenderEnqueue,
	}
}
