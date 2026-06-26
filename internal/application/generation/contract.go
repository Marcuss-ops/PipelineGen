// Package generation — contract.go: Capability Standard contracts.
//
// Holds the typed commands (POST/DELETE ask), queries (GET ask),
// results (response payloads), JobType constants, and the canonical
// worker-side handler signature for the Generation capability.
//
// Capabilities expose these through:
//   - service.go (Create/Status/Cancel — the dispatcher side)
//   - jobs.go    (RegisterJobHandlers — the worker side via the
//                  api.DescriptorJobs slot)
//
// The HTTP handler in handler.go translates JSON requests into the
// commands below and never touches the dispatcher or jobs.Service
// directly.
//
// Errors ErrUnsupportedType / ErrTypeDisabled / ErrJobNotFound
// are declared in service.go (the canonical location where the
// dispatcher returns them). The contract layer does NOT redeclare
// them — package-internal identifier visible from handler.go's
// writeErr via errors.Is.
package generation

import (
	"encoding/json"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domaingeneration "github.com/Marcuss-ops/PipelineGen/internal/domain/generation"
)

// Typed commands ----------------------------------------------------

// CreateCommand is the canonical ask for POST /api/generations.
// Field-for-field translation from JSON createRequest via the
// handler; service validates + resolves + enqueues.
type CreateCommand struct {
	Type    domaingeneration.Type
	Input   json.RawMessage
	Options map[string]any
}

// StatusQuery is the canonical ask for GET /api/generations/:id.
type StatusQuery struct {
	JobID string
}

// CancelCommand is the canonical ask for POST /api/generations/:id/cancel.
type CancelCommand struct {
	JobID string
}

// Typed results -----------------------------------------------------

// CreateResult is what POST /api/generations hands back: the
// canonical public job envelope projected from the enqueued job.
type CreateResult struct {
	OK  bool   `json:"ok"`
	Job JobRef `json:"job"`
}

// StatusResult is what GET /api/generations/:id hands back.
type StatusResult struct {
	OK  bool     `json:"ok"`
	Job JobState `json:"job"`
}

// CancelResult is what POST /api/generations/:id/cancel hands back.
type CancelResult struct {
	OK bool `json:"ok"`
}

// JobRef is the minimal post-create projection: enough for clients
// to poll the status endpoint.
type JobRef struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	StatusURL string `json:"status_url"`
}

// JobState is the canonical status projection of a generation job.
type JobState struct {
	ID          string                  `json:"id"`
	Type        string                  `json:"type"`
	Status      string                  `json:"status"`
	Progress    int                     `json:"progress"`
	Phase       string                  `json:"phase,omitempty"`
	Message     string                  `json:"message,omitempty"`
	CreatedAt   string                  `json:"created_at"`
	StartedAt   string                  `json:"started_at,omitempty"`
	CompletedAt string                  `json:"completed_at,omitempty"`
	Result      json.RawMessage         `json:"result,omitempty"`
	Error       *domaingeneration.Error `json:"error,omitempty"`
}

// Worker-side job type constants ------------------------------------

// JobTypeBooksProcess is the canonical job type the worker side
// runs for book generation. Mirrors domain/job.TypeBooksProcess so
// the capability can publish the constant without importing
// internal/domain/job.
const JobTypeBooksProcess = "books.process"

// JobTypeLessonsProcess is the canonical job type the worker side
// runs for lesson generation.
const JobTypeLessonsProcess = "lessons.process"

// Worker-side handler signature -------------------------------------

// HandlerFunc is the canonical worker-side handler signature. It
// is the function-value shape the Generation capability's
// Dependencies.Books / Dependencies.Lessons accept from the
// composition root. The composition root polls the BooksService /
// LessonsService method values (books.Service.HandleJob,
// lessons.Service.HandleJob) and forwards them in via this type.
//
// Re-exports appjobs.HandlerFunc so the rest of the capability
// package does not need to scatter imports of appjobs.
type HandlerFunc = appjobs.HandlerFunc
