package generation

import (
	"context"

	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ProgressReporter is the canonical progress and event callback interface
// for generation executors. Implemented by the worker's JobTools.
type ProgressReporter interface {
	Progress(percent int, message string)
	Event(eventType string, message string, data map[string]any)
	IsCancelled() bool
}

// Generator executes a generation job for a specific internal job type.
// Each Generator handles exactly one job.Type value.
type Generator interface {
	// Type returns the internal job type this generator handles.
	Type() string

	// Execute runs the generation and returns the canonical result envelope.
	Execute(ctx context.Context, j jobdomain.Job, progress ProgressReporter) (Result, error)
}
