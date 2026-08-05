package observability

import "context"

// Collector is the in-process sink for completed run reports. Infrastructure
// adapters can translate a report into Prometheus observations, logs, or
// another low-cardinality telemetry system without making the kernel depend on
// those implementations.
//
// Collection is best-effort: implementations must not make a job fail when a
// metrics backend is unavailable. The report is already finalized when it is
// passed to Collect and must be treated as immutable.
type Collector interface {
	Collect(ctx context.Context, report *RunReport) error
}

// NoopCollector discards reports. It is useful when only the durable recorder
// is configured or when a caller wants an explicit no-op metrics sink.
type NoopCollector struct{}

// Collect implements Collector.
func (NoopCollector) Collect(context.Context, *RunReport) error { return nil }
