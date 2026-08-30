package jobs

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"go.uber.org/zap"
)

// Bundle is the minimal composition-time surface for the jobs capability.
//
// It deliberately exposes only the application ports required by the jobs
// HTTP module. Construction remains in this capability package, while the
// existing Build/Module implementation remains the single canonical owner of
// transport assembly and validation.
type Bundle struct {
	service     *Service
	stats       JobStatsReader
	history     HistoryReader
	enabledFunc func() bool
	logger      *zap.Logger
}

func NewBundleWithHistory(service *Service, stats JobStatsReader, history HistoryReader, enabledFunc func() bool, logger *zap.Logger) Bundle {
	return Bundle{service: service, stats: stats, history: history, enabledFunc: enabledFunc, logger: logger}
}

// NewBundle captures the narrow jobs capability dependencies without exposing
// the larger application composition root to capability consumers.
func NewBundle(service *Service, stats JobStatsReader, enabledFunc func() bool, logger *zap.Logger) Bundle {
	return Bundle{
		service:     service,
		stats:       stats,
		enabledFunc: enabledFunc,
		logger:      logger,
	}
}

// Name implements api.CapabilityModule.
func (b Bundle) Name() string { return "jobs" } // Build delegates to the existing canonical jobs module builder.
func (b Bundle) Build(ctx api.BuildContext) (api.RuntimeModule, error) {
	d, err := Build(Dependencies{
		Service:     b.service,
		Stats:       b.stats,
		History:     b.history,
		EnabledFunc: b.enabledFunc,
		Logger:      b.logger,
	})
	if err != nil {
		return api.RuntimeModule{}, err
	}
	return api.RuntimeModuleFor("jobs", "/api/jobs", d)
}

// Handler returns the canonical *JobsHandler constructed during Build.
// PG-M2M (Aug 2026): the composition root uses this to construct the
// M2M job surface (NewM2MJobsModule) on a SEPARATE RouterGroup
// (the /api/v1/jobs group protected by JobClientAuthMiddleware),
// while the admin module (Build above) stays on the /api/jobs admin
// group. Both surfaces share the SAME *JobsHandler so Enqueue/Get
// remain single-implementation.
//
// The handler is built lazily on first call (the admin module Build
// path constructs it via NewJobsHandler); callers that only need the
// M2M surface can call Handler() without going through Build. This
// keeps the M2M surface independent of the admin module's
// RuntimeModule plumbing.
func (b Bundle) Handler() *JobsHandler {
	return NewJobsHandler(b.service, b.stats, b.logger)
}
