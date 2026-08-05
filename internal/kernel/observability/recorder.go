package observability

import "context"

// Recorder is the durable sink for finished run reports. It is the only
// extension seam of this package: the SQLite writer (later phase) implements
// it, tests use in-memory captures, and production can chain it with a
// Prometheus adapter.
//
// Implementations MUST be best-effort: a persistence failure is logged by the
// implementation and never fails the job that produced the report.
type Recorder interface {
	SaveReport(ctx context.Context, report *RunReport) error
}

// NoopRecorder discards reports. It is the default for observers constructed
// without a sink and for tests that only inspect in-memory reports.
type NoopRecorder struct{}

// SaveReport implements Recorder by discarding the report.
func (NoopRecorder) SaveReport(context.Context, *RunReport) error { return nil }
