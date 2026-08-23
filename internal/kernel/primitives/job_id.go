package primitives

// JobID is the canonical nominal type for the job identifier.
//
// godlike/06 SSOT (narrow port doctrine): JobID is the *only* typed
// identifier the domain layer accepts/exposes for jobs. Callers must
// not pass/return raw `string` for this value. The boundary layer
// (HTTP handler / CLI) is responsible for converting raw input into
// JobID via NewJobID.
//
// Wire identity: because `type JobID string` is a Go-defined named
// string type, JSON marshaling preserves the underlying string value
// with zero overhead (no MarshalJSON/UnmarshalJSON required).
type JobID string

// NewJobID wraps a raw string into a canonical JobID. The constructor
// never returns an error and never panics: an empty input is allowed
// and surfaces via IsEmpty at the handler boundary, where the richer
// HTTP context (400 mapping, idempotency token lookup) is available.
//
// Concurrency: the constructor is pure (no shared state) and safe
// for concurrent use.
func NewJobID(s string) JobID { return JobID(s) }

// IsEmpty reports whether the JobID is the zero value. This is the
// boundary-friendly hook for fail-closed validation: handlers reject
// empty JobID with 400 before invoking the application layer.
func (id JobID) IsEmpty() bool { return id == "" }

// String returns the underlying string form. Required by the fmt
// contract; deliberately NOT a `MarshalJSON`/`UnmarshalJSON` so the
// JSON wire format stays byte-identical with the raw-string version.
func (id JobID) String() string { return string(id) }
