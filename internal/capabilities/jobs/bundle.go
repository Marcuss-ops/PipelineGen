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
func (b Bundle) Name() string { return "jobs" }

// Build delegates to the existing canonical jobs module builder.
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

func (b Bundle) buildOld(ctx api.BuildContext) (api.Descriptor, error) {
	return Build(Dependencies{
		Service:     b.service,
		Stats:       b.stats,
		History:     b.history,
		EnabledFunc: b.enabledFunc,
		Logger:      b.logger,
	})
}
