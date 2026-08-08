package observability

import (
	"strings"

	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports/metrics"
)

// ScriptGenerationBranchRecorder is the application-owned metrics port
// implemented by this infrastructure adapter.
type ScriptGenerationBranchRecorder struct{}

// NewScriptGenerationBranchRecorder constructs the Prometheus-backed recorder.
func NewScriptGenerationBranchRecorder() *ScriptGenerationBranchRecorder {
	return &ScriptGenerationBranchRecorder{}
}

// RecordScriptGenerationBranch increments the canonical branch counter.
func (r *ScriptGenerationBranchRecorder) RecordScriptGenerationBranch(branch, bcp47 string) {
	if r == nil || branch == "" {
		return
	}
	country := scriptmetrics.ExtractCountryForTelemetry(bcp47)
	ScriptGenerationBranchTotal.WithLabelValues(strings.ToLower(branch), country).Inc()
}

var _ scriptmetrics.ScriptGenerationBranchRecorder = (*ScriptGenerationBranchRecorder)(nil)
