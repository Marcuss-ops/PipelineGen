package delivery

import (
	capdelivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// NewDriveValidatorMetrics builds the platform metrics contract with the
// canonical observability collectors wired in.
func NewDriveValidatorMetrics() *DriveValidatorMetrics {
	return &DriveValidatorMetrics{
		Probes:    observability.DriveRootsValidatorProbesTotal,
		Duration:  observability.DriveRootsValidatorProbeDuration,
		LastRunTS: observability.DriveRootsValidatorLastRunTimestamp,
		LastRunOK: observability.DriveRootsValidatorLastRunSucceeded,
	}
}

// NewDriveValidatorMetricsFromCollectors builds the platform metrics
// contract from caller-owned collectors, primarily for composition tests.

var _ capdelivery.Publisher = (Publisher)(nil)
