// Package app — build_voiceover_destinations.go
// Destination resolution for the canonical per-item voiceover use case.
// A nil asset.Resolver (typical of internal/app/*_test.go stub-bootstrap
// helpers) is replaced by a nil-tolerant nopDestinationResolver so the
// ProcessVoiceoverItemUseCase constructor's nil guard passes while the
// use case still fails closed with "missing_folder_id".
//
// Extracted from buildVoiceoverService (build_bundles_voiceover.go) as
// part of the July 2026 domain split: tts / destinations / jobs /
// validators.
package app

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildVoiceoverDestResolvers constructs the destination resolver
// adapters consumed by ProcessVoiceoverItemUseCase (Recovery +
// Pipeline deps).
//
// Nil-tolerant construction (mirrors the outboxDispatcher block in
// buildVoiceoverService). When destResolver is NOT supplied (typical of
// `internal/app/*_test.go` stub-bootstrap helpers, which exercise the
// composition root without standing up the full asset.Resolver chain),
// a nil-tolerant nopDestinationResolver is wired so the
// ProcessVoiceoverItemUseCase constructor's
// `if deps.DestinationResolver == nil { panic }` check passes. The use
// case's Execute tolerates a resolver that returns (nil, nil) via the
// canonical "missing_folder_id" short-circuit in
// ResolveDestinationWithFallback — the production semantics are
// preserved (a request without an explicit Destination always fails
// closed, regardless of whether the resolver is real or nop).
//
// P0-#3 (July 2026): processItemUseCase is now ALWAYS constructed (not
// gated on destResolver != nil) because the NewService
// composition-time fail-fast panics when ProcessItem is nil. The use
// case itself tolerates nil DestinationResolver + nil
// DefaultFolderResolver via the canonical short-circuit path.
func buildVoiceoverDestResolvers(
	destResolver asset.Resolver,
	cfg *config.Config,
	voDir string,
	log *zap.Logger,
) (voiceover.DestinationResolver, voiceover.VoiceoverDefaultFolderResolver) {
	// Always wire the real adapter (now nil-tolerant): an explicit
	// destination (KindExplicit / KindAuto + FolderID) resolves through
	// ResolveVoiceoverDestination's direct() path WITHOUT consulting the
	// asset.Resolver, so a caller-supplied output.voiceover_folder_id works
	// even when the deployment lacks a configured voiceover_root_folder.
	// Only group-based routing needs the asset tree, and it fails with a
	// typed error when destResolver is nil.
	destResolverAdapter := newUseCaseDestResolverAdapter(destResolver)
	defaultFolderResolver := newUseCaseDefaultFolderResolverAdapter(
		cfg.Drive.VoiceoverFolder(),
		voDir,
	)
	if destResolver == nil {
		log.Warn("voiceover: no asset.Resolver wired; explicit voiceover destinations resolve directly, but group-based routing will fail closed (typical of internal/app/*_test.go stub-bootstrap helpers)")
	}
	return destResolverAdapter, defaultFolderResolver
}

// nopDestinationResolver is a nil-tolerant DestinationResolver used
// by the composition root when no asset.Resolver is wired (typical
// of `internal/app/*_test.go` stub-bootstrap helpers that exercise
// the composition root without the full asset resolution chain).
//
// The ProcessVoiceoverItemUseCase constructor panics on nil
// DestinationResolver (a composition-time fail-closed guard), so the
// composition root cannot pass a literal nil interface. The nop
// resolver returns (nil, nil) — a value that the downstream
// ResolveDestinationWithFallback function correctly maps to the
// canonical "missing_folder_id" short-circuit, so the use case
// surfaces a typed failure on every Execute call rather than
// silently falling back to /tmp or some other unspecified
// destination.
//
// godlike/07 NO-FAKE-AVAILABILITY: this is a TEST-BOOTSTRAP-ONLY
// degradation. Production composition root paths always wire a
// real asset.Resolver (the `else` branch in
// buildVoiceoverDestResolvers logs a Warn so operators see the
// dev-mode shortcut). The Warn + the "missing_folder_id" failure
// mode together preserve the no-fake-availability invariant: a
// misconfigured composition root fails loud, not silent.
type nopDestinationResolver struct{}

// Compile-time assertion (AGENTS.md Pattern 0): the nop resolver
// must structurally satisfy the narrow voiceover.DestinationResolver
// port so a future port drift triggers a compile error here.
var _ voiceover.DestinationResolver = nopDestinationResolver{}

// Resolve is the canonical nop implementation: returns (nil, nil) so
// the use case's ResolveDestinationWithFallback short-circuits to
// "missing_folder_id" via the canonical Rule 2 + Rule 3 path
// (destReq == nil AND defaultResolver is nil → return nil).
func (nopDestinationResolver) Resolve(_ context.Context, _ *voiceover.DestinationRequest) (*voiceover.ResolvedDestination, error) {
	return nil, nil
}
