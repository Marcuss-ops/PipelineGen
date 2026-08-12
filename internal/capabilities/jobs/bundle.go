package jobs

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// Bundle is the minimal composition-time surface for the jobs capability.
//
// It deliberately exposes only the application ports required by the jobs
// HTTP module. Construction remains in this capability package, while the
// existing Build/Module implementation remains the single canonical owner of
// transport assembly and validation.
type Bundle struct {
	service     job.Service
	stats       appjobs.JobStatsReader
	history     appjobs.HistoryReader
	enabledFunc func() bool
	logger      *zap.Logger
}

func NewBundleWithHistory(service job.Service, stats appjobs.JobStatsReader, history appjobs.HistoryReader, enabledFunc func() bool, logger *zap.Logger) Bundle {
	return Bundle{service: service, stats: stats, history: history, enabledFunc: enabledFunc, logger: logger}
}

// NewBundle captures the narrow jobs capability dependencies without exposing
// the larger application composition root to capability consumers.
func NewBundle(service job.Service, stats appjobs.JobStatsReader, enabledFunc func() bool, logger *zap.Logger) Bundle {
	return Bundle{
		service:     service,
		stats:       stats,
		enabledFunc: enabledFunc,
		logger:      logger,
	}
}

// Name implements api.CapabilityModule.
func (b Bundle) Name() string { return "jobs" }

// Build delegates to the existing canonical jobs module builder. Bundle is the
// composition-root facade; NewModule remains the lower-level canonical builder
// used by this delegation and by compatibility callers. Keeping this single
// implementation path prevents duplicate transport composition or a parallel
// migration of the jobs transport.
func (b Bundle) Build(ctx api.BuildContext) (api.RuntimeModule, error) {
	return NewModule(Dependencies{
		Service:     b.service,
		Stats:       b.stats,
		History:     b.history,
		EnabledFunc: b.enabledFunc,
		Logger:      b.logger,
	}).Build(ctx)
}

var _ api.CapabilityModule = Bundle{}
