package ingest

// stockPipelineRuntimeMode records which constructor owns a Service. Keeping
// this distinction on the service makes it impossible for a fixture service
// to silently enter the strict production orchestrator path.
type stockPipelineRuntimeMode uint8

const (
	stockPipelineProductionMode stockPipelineRuntimeMode = iota
	stockPipelineTestMode
)

// NewProductionStockPipeline constructs the service used by the live
// composition root. Its dependency validation is deliberately fail-closed:
// no publisher, finalizer, dispatcher, source stager, probe, projection, or
// durable state dependency is replaced with a fixture default.
func NewProductionStockPipeline(deps Deps) (*Service, error) {
	service, err := newStockPipelineService(deps)
	if err != nil {
		return nil, err
	}
	service.runtimeMode = stockPipelineProductionMode
	return service, nil
}

// NewTestStockPipeline constructs a service for fixture and unit-test
// composition. The service keeps the same typed dependency contract, while
// its run path deliberately uses NewTestStockOrchestrator and its in-memory /
// noop fixture defaults. No test fixture can accidentally execute the strict
// production orchestrator.
func NewTestStockPipeline(deps Deps) (*Service, error) {
	if deps.Runtime.Cfg == nil {
		return nil, ErrStockPipelineNilCfg
	}
	if deps.Runtime.Log == nil {
		return nil, ErrStockPipelineNilLog
	}
	if deps.Media.Cutter == nil {
		return nil, ErrStockPipelineNilCutter
	}
	if deps.Media.Renderer == nil {
		return nil, ErrStockPipelineNilRenderer
	}
	service := serviceFromDeps(deps)
	service.runtimeMode = stockPipelineTestMode
	return service, nil
}

func newStockPipelineService(deps Deps) (*Service, error) {
	return newService(deps)
}
