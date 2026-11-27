package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/dbhealth"
)

// DBHealthFindings exposes the last DB-08 doctor run per plane, split by
// severity. An error series above zero is the alertable condition; warnings
// are visible without failing the boot.
var DBHealthFindings = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "db_health_findings",
	Help: "Findings from the most recent DB health doctor run, by plane and severity.",
}, []string{"plane", "severity"})

// RecordDBHealthFindings replaces the gauge values for one plane with the
// counts from the current run. Missing severities are explicitly zeroed so a
// cleared condition does not leave a stale non-zero series behind.
func RecordDBHealthFindings(plane string, findings []dbhealth.Finding) {
	if plane == "" {
		return
	}
	counts := map[dbhealth.Severity]int{
		dbhealth.SeverityError:   0,
		dbhealth.SeverityWarning: 0,
		dbhealth.SeverityInfo:    0,
	}
	for _, f := range findings {
		counts[f.Severity]++
	}
	for severity, n := range counts {
		DBHealthFindings.WithLabelValues(plane, string(severity)).Set(float64(n))
	}
}
