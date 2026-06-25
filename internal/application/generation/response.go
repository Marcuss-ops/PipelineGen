package generation

// Mode identifies whether a generation request completed synchronously
// or was queued for background processing.
type Mode string

const (
	ModeSync  Mode = "sync"
	ModeAsync Mode = "async"
)

// Response is the shared wire envelope for all text-generation flows.
// The result payload is attached only on the synchronous branch.
//
// The envelope stays intentionally small:
//   - ok/kind/mode are always present
//   - job_id/status/job_type are async-only
//   - result is sync-only
//
// This gives the script, books, and lessons endpoints the same top-level
// shape while still allowing each endpoint to expose its own result DTO.
type Response[T any] struct {
	OK      bool   `json:"ok"`
	Kind    string `json:"kind"`
	Mode    Mode   `json:"mode"`
	JobID   string `json:"job_id,omitempty"`
	Status  string `json:"status,omitempty"`
	JobType string `json:"job_type,omitempty"`
	Result  *T     `json:"result,omitempty"`
}

// Sync builds a successful synchronous response envelope.
func Sync[T any](kind string, result T) Response[T] {
	return Response[T]{
		OK:     true,
		Kind:   kind,
		Mode:   ModeSync,
		Result: &result,
	}
}

// Async builds a successful async acknowledgment envelope.
func Async[T any](kind, jobID, status, jobType string) Response[T] {
	return Response[T]{
		OK:      true,
		Kind:    kind,
		Mode:    ModeAsync,
		JobID:   jobID,
		Status:  status,
		JobType: jobType,
	}
}
