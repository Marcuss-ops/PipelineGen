package usecase

import scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"

// ExtractCountryForTelemetry preserves the usecase-facing helper while
// delegating the policy to the application-owned metrics port package.
func ExtractCountryForTelemetry(bcp47 string) string {
	return scriptmetrics.ExtractCountryForTelemetry(bcp47)
}
