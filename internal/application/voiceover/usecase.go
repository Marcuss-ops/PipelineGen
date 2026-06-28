// Package voiceover — BACKFILL typed-port use case (Wave 21
// PR-VOICEOVER-TYPED-PORT-RECOVERY-PHASE2, B-2 step closure).
//
// AGENTS.md Pattern 0 (port abstraction layer, June 2026):
// use cases own their dependency wiring via ServiceDeps{...}; concrete
// adapters are injected in the composition root (internal/app/
// build_bundles_voiceover.go) and never directly referenced from the
// service layer.
//
// B-2 BACKFILL scope:
//   - GenerateVoiceoverUseCase is a 1-a-1 delegate to VoiceoverGenerator.
//   - No business logic augmentation at B-2 — pure typed-port
//     introduction.
//   - Wired in build_bundles_voiceover.go but NOT YET CONSUMED by
//     call sites in scripts/ or handlers. CUTOVER (B-3) flips the call
//     sites; CONTRACT (B-4) removes back-compat aliases.
//
// Future B-3+ BACKFILL stages can layer:
//   - idempotency cache lookup (per Wave 21 PR-G.2 BACKFILL readiness)
//   - post-write save context detachment (per AGENTS.md post-write
//     save exemption table)
//   - audit-log emit (per Wave 22 context.WithoutCancel gate review)
package voiceover

import (
	"context"

	"go.uber.org/zap"
)

// ServiceDeps wires dependencies for GenerateVoiceoverUseCase per
// AGENTS.md Pattern 0. Generator is the BACKFILL port (production
// supplies *Service via compile-time assertion in ports.go; tests
// inject stubs).
type ServiceDeps struct {
	// Generator is the canonical voiceover generation port (B-2 BACKFILL).
	Generator VoiceoverGenerator

	// Logger is optional (nil-safe via zap.NewNop() in the constructor).
	Logger *zap.Logger
}

// GenerateVoiceoverUseCase is the BACKFILL typed-port-adapter for
// voiceover generation. It is a 1-a-1 delegate to VoiceoverGenerator —
// no business orchestration augmentation at B-2 scope; future BACKFILL
// stages layer decorators via a UseCase decorator chain without touching
// the Service core or the wire-up.
type GenerateVoiceoverUseCase struct {
	deps ServiceDeps
}

// NewGenerateVoiceoverUseCase constructs the use case with mandatory
// deps. Panics on nil Generator — fail-fast per AGENTS.md WireUp pattern.
// Logger is optional (nil-safe via zap.NewNop()).
func NewGenerateVoiceoverUseCase(deps ServiceDeps) *GenerateVoiceoverUseCase {
	if deps.Generator == nil {
		panic("voiceover.NewGenerateVoiceoverUseCase: Generator is required (ServiceDeps.Generator)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &GenerateVoiceoverUseCase{deps: deps}
}

// Execute delegates 1-a-1 to the underlying VoiceoverGenerator.
// B-2 BACKFILL scope: NO orchestration additions — pure typed-port.
//
// Signature mirrors main's *Service.Generate shape (positional
// ctx + text + language + filename).
func (u *GenerateVoiceoverUseCase) Execute(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	return u.deps.Generator.Generate(ctx, text, language, filename)
}
