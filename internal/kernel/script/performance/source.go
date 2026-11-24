// Package performance owns the read-only performance aggregation port. The
// scripts capability consumes it; the SQLite adapter implements it without
// importing the scripts business package.
package performance

import (
	"context"

	kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ReportSource loads the canonical inputs for one job. It is the read-only
// seam the aggregator depends on; production wiring supplies an adapter that
// reads the persisted RunReport and its audio-operation projection, plus the
// recorded execution steps. No implementation in the capability performs I/O.
type ReportSource interface {
	Load(ctx context.Context, jobID string) (kernobs.RunReport, kernelaudio.AudioPipelineMetrics, []scriptpkg.ExecutionStep, error)
}
