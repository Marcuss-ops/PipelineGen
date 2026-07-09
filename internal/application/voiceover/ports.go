// Package voiceover — narrow port interfaces for out-of-package
// dependencies (AGENTS.md Pattern 0, June 2026).
//
// Fase 4 Spina Dorsale (July 2026): ports are organised into three
// territories matching the domain separation:
//
//	ports_synthesis.go   — TTSProvider, AudioPostProcessor, DTOs
//	ports_publication.go — VoiceoverPublisher, PublishCommand, Validate,
//	                       sentinels, DestinationResolver, URL helpers
//	ports_finalization.go — TxOutboxEnqueuer, VoiceoverFinalizer,
//	                        PostCommitVerifier, ItemExecutor
//
// The package-level `database/sql` import exists ONLY in
// ports_finalization.go. Synthesis-territory ports carry ZERO
// sql/drive/qdrant imports.
//
// Layout note: voiceover never imports the SDK. Production concretes
// satisfy these ports by structural conformance (Go's implicit
// interface rules). Tests substitute stubs that record invocations.
package voiceover

import "context"

// ────────────────────────────────────────────────────────────────────────
// VoiceoverGenerator — the main synthesis entry-point port.
// ────────────────────────────────────────────────────────────────────────

// VoiceoverGenerator is the BACKFILL typed-port (Wave 21
// PR-VOICEOVER-TYPED-PORT-RECOVERY-PHASE2, B-2 step closure, per
// AGENTS.md Pattern 0 — port abstraction layer, June 2026).
//
// Shape: matches main's *Service.Generate signature exactly (positional
// ctx + text + language + filename). The original blueprint called for
// a `voiceover.GenerateVoiceoverCommand` struct, but the live main shape
// is positional — per the user's re-execution directive: "usando il type
// del dominio attuale di main, NON blueprint".
//
// Back-compat note: the legacy *Service satisfies this port
// structurally via Go's implicit-interface rule. Test doubles inject
// stubs via ServiceDeps in usecase.go (no production behavior change).
//
// The VoiceoverResult return type is the package-local struct declared
// in types.go (NOT the domain.voiceover.VoiceoverResult alias for the
// canonical Result — those are intentionally separate types).
type VoiceoverGenerator interface {
	Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error)
	// GenerateWithDestination is the canonical narrow-port surface for
	// destination-aware voiceover generation.
	GenerateWithDestination(ctx context.Context, text, language, filename string, dest *DestinationRequest) (*VoiceoverResult, error)
}

// Compile-time assertion (AGENTS.md Pattern 0): *Service must
// structurally satisfy VoiceoverGenerator. Drift between Service.Generate
// signature and the port contract triggers a compile error at this
// line — preventing silent drift on the wire contract.
var _ VoiceoverGenerator = (*Service)(nil)

// Logger is intentionally NOT defined as an interface here — the
// canonical codebase-wide logging surface is *zap.Logger (used across
// every application-layer package). The use case constructor accepts
// *zap.Logger directly and nil-safes it via zap.NewNop(). Re-aliasing
// at this layer would only add drift surface.
