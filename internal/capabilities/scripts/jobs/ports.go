// Package scripts/jobs - ClipsFolderExtPort declares the canonical narrow
// port that scripts/jobs consumes to extract a Drive folder ID from a
// (possibly whitespace-padded) raw folder-id string.
//
// Refactor 1 of the cross-capability cleanup plan (June 2026, audit at
// architecture/audits/2026-06-28-cross-capability-imports.md):
// removes the direct clips-package import from job_helpers.go by
// introducing the consumer-declared port.
//
// Layering note: per AGENTS.md Pattern 0 the port is declared
// structurally here in scripts/jobs (the consumer); the adapter
// wraps the existing clips.ExtractDriveFolderID function. Production
// concrete wired in the composition root via NewClipsFolderExtAdapter.
package jobs

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
)

// ClipsFolderExtPort is the canonical typed port for the single-purpose
// operation of extracting a Drive folder ID from a (potentially
// whitespace-padded) raw string. The port signature is intentionally
// minimal: a single ExtractDriveFolderID method that takes a raw
// string and returns the canonicalised folder ID.
//
// Production wiring is NewClipsFolderExtAdapter() which wraps the
// existing clips.ExtractDriveFolderID function plus a defensive
// strings.TrimSpace; test wiring is a stub that returns canned
// folder IDs so existing folder-resolver logic remains untouched.
//
// Why consumer-side ownership (scripts/jobs) and not producer-side
// (clips): scripts/jobs is the consumer that needs the operation.
// clips is the producer that supplies the underlying function. Per
// AGENTS.md Pattern 0 ports belong to the consumer, not the producer.
type ClipsFolderExtPort interface {
	ExtractDriveFolderID(raw string) string
}

// clipsFolderExtAdapter bridges clips.ExtractDriveFolderID into the
// ClipsFolderExtPort port.
//
// The adapter is stateless; a single instance is safe to share across
// goroutines (the wrapped clips.ExtractDriveFolderID is a pure
// function). Production wiring is NewClipsFolderExtAdapter() called
// once at composition time.
type clipsFolderExtAdapter struct{}

// NewClipsFolderExtAdapter constructs the production adapter wrapping
// clips.ExtractDriveFolderID. Returns a fresh instance per call; for
// shared use, callers can cache the result themselves (the adapter is
// stateless and goroutine-safe).
func NewClipsFolderExtAdapter() ClipsFolderExtPort {
	return &clipsFolderExtAdapter{}
}

// Compile-time assertion (AGENTS.md Pattern 0): the adapter must
// structurally satisfy the port. Drift between adapter signature and
// port contract surfaces as a compile error here.
var _ ClipsFolderExtPort = (*clipsFolderExtAdapter)(nil)

// ExtractDriveFolderID trims whitespace and forwards to
// clips.ExtractDriveFolderID. The defensive trim preserves the
// pre-refactor behaviour of internal/capabilities/scripts/jobs/job_helpers.go
// which called `clips.ExtractDriveFolderID(strings.TrimSpace(...))`
// twice (lines ~46-47 for voiceoverFolderID + voRootID).
func (a *clipsFolderExtAdapter) ExtractDriveFolderID(raw string) string {
	return clips.ExtractDriveFolderID(strings.TrimSpace(raw))
}
