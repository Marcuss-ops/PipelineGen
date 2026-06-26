// Package generation — jobs.go: worker-side handler publication
// for the Generation capability.
//
// Implements the api.DescriptorJobs optional slot. The composition
// root (internal/app/registry.go::WireRegistry) type-asserts on
// the descriptor and calls RegisterJobHandlers after the
// descriptor is wired. This single-source replaces the late-binding
// calls that used to live in internal/app/composition.go.
//
// Generation owns two worker-side job types:
//   - books.process   (handled by books.Service.HandleJob)
//   - lessons.process (handled by lessons.Service.HandleJob)
//
// IMPORTANT: this file does NOT import internal/application/books
// or internal/application/lessons — both packages already transitively
// import internal/application/generation (for generation.Response[T]
// envelopes used by their use cases). To avoid the resulting import
// cycle, the capability receives the handler functions as deps
// (Dependencies.Books / Dependencies.Lessons of type generation.HandlerFunc
// = appjobs.HandlerFunc) and loses the type-level coupling.
package generation

import (
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"go.uber.org/zap"
)

// JobHandlers aggregates the worker-side handler dependencies
// the Generation capability owns. The fields are typed as
// HandlerFunc (function values), not *books.Service / *lessons.Service,
// so this package does NOT need to import the books/lessons packages —
// they would form an import cycle because those packages transitively
// import internal/application/generation.
//
// Composition-time nil is tolerated: RegisterJobHandlers silently
// skips a handler whose field is nil, exactly matching the previous
// "feature flag off → don't register" behaviour.
type JobHandlers struct {
	// Books is the worker-side handler for books.process jobs.
	// nil = skip (e.g. when BooksEnabled is false or the books
	// sub-service isn't wired).
	Books appjobs.HandlerFunc
	// Lessons is the worker-side handler for lessons.process jobs.
	// nil = skip.
	Lessons appjobs.HandlerFunc
	// Log records the per-handler registration outcome.
	Log *zap.Logger
}

// RegisterJobHandlers publishes the worker-side bindings into the
// canonical jobs registrar. Implements api.DescriptorJobs.
//
// Takes a typed JobRegistrar port, not the concrete *appjobs.Service,
// so:
//   - the capability is decoupled from the canonical service concrete
//     (future broker swap is one new impl),
//   - tests can stub with their own JobRegistrar (the module_test.go
//     recordingJobs satisfies the interface at compile time).
//
// Returns an error only if the registrar itself rejects the
// registration (duplicate type, frozen dispatcher). nil Books and
// nil Lessons are tolerated (silently skipped).
func (jh JobHandlers) RegisterJobHandlers(svc api.JobRegistrar) error {
	if svc == nil {
		return fmt.Errorf("generation.JobHandlers: jobs registrar is required")
	}
	if jh.Books != nil {
		if err := svc.RegisterHandler(JobTypeBooksProcess, jh.Books); err != nil {
			return fmt.Errorf("generation.JobHandlers: register books handler: %w", err)
		}
		if jh.Log != nil {
			jh.Log.Info("registered book.process job handler via generation capability")
		}
	}
	if jh.Lessons != nil {
		if err := svc.RegisterHandler(JobTypeLessonsProcess, jh.Lessons); err != nil {
			return fmt.Errorf("generation.JobHandlers: register lessons handler: %w", err)
		}
		if jh.Log != nil {
			jh.Log.Info("registered lessons.process job handler via generation capability")
		}
	}
	return nil
}
