// Package app — build_voiceover_destinations.go
// Destination resolution for the canonical per-item voiceover use case.
// The real adapter is the only resolver produced here: the use case
// tolerates a nil asset.Resolver on its own (nil DestinationResolver +
// nil DefaultFolderResolver short-circuits to "missing_folder_id"), which
// is why the NewNopDestinationResolver companion that used to live in this
// file was deleted on 2026-09-20 — nothing constructed it, not even the
// stub-bootstrap helpers it was written for.
//
// Extracted from buildVoiceoverService (build_bundles_voiceover.go) as
// part of the July 2026 domain split: tts / destinations / jobs /
// validators.
package voiceover

import (
	"go.uber.org/zap"

	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// BuildVoiceoverDestResolvers constructs the destination resolver
// adapters consumed by ProcessVoiceoverItemUseCase (Recovery +
// Pipeline deps).
//
// Nil-tolerant construction (mirrors the outboxDispatcher block in
// buildVoiceoverService). When destResolver is NOT supplied (typical of
// `internal/app/*_test.go` stub-bootstrap helpers, which exercise the
// composition root without standing up the full asset.Resolver chain),
// a nil-tolerant NewNopDestinationResolver is wired so the
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
func BuildVoiceoverDestResolvers(
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
	destResolverAdapter := NewUseCaseDestResolverAdapter(destResolver)
	defaultFolderResolver := NewUseCaseDefaultFolderResolverAdapter(
		cfg.Drive.VoiceoverFolder(),
		voDir,
	)
	if destResolver == nil {
		log.Warn("voiceover: no asset.Resolver wired; explicit voiceover destinations resolve directly, but group-based routing will fail closed (typical of internal/app/*_test.go stub-bootstrap helpers)")
	}
	return destResolverAdapter, defaultFolderResolver
}

// NewNopDestinationResolver was DELETED here on 2026-09-20. It was a
// nil-tolerant DestinationResolver documented as a TEST-BOOTSTRAP-ONLY
// degradation for `internal/app/*_test.go` stub-bootstrap helpers, but no such
// helper (and no production path) ever constructed it — its Resolve was
// unreachable. Production still wires the real adapter built above, and the
// use case's own ResolveDestinationWithFallback short-circuits to
// "missing_folder_id" when the resolver returns (nil, nil), so the failure mode
// the nop existed to produce is unchanged without it.
