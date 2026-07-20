// Package submission — command.go defines the canonical command
// objects consumed by the script-generation submission factory.
//
// PR-SUBMISSION-FACTORY (July 2026): the HTTP transport layer
// (internal/api/script) builds only this command and delegates
// SubmitRequest assembly to the application layer. This keeps
// transport concerns (headers, JSON binding, status codes) out of
// policy/hash/scope decisions.
package submission

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

// GenerateCommand carries the facts the transport layer extracted
// from an HTTP request. It intentionally contains no HTTP-specific
// types so the factory stays portable and testable without a
// gin.Context.
type GenerateCommand struct {
	// Envelope is the validated generation envelope.
	Envelope *scriptpkg.GenerationEnvelopeV2
	// IdempotencyKey is the caller-supplied, trimmed idempotency key.
	IdempotencyKey string
}
