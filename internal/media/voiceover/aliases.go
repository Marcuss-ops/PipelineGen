// Package voiceover is the backward-compatibility shim for
// internal/media/voiceover. The canonical implementation now lives in
// internal/application/voiceover (PR-D.2 migration). Existing callers
// keep working unchanged; new code MUST import
// internal/application/voiceover directly.
//
// Coverage mirrors every public identifier exported from the original 8
// source files (service, types, process, promo, job_handler, filename,
// groups_resolver, registry_adapter). Private helpers stay with the
// implementation.
package voiceover

import (
	"database/sql"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/core/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/core/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/voiceovers"
	appassoc "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
)

// ── Sentinel error re-exports ───────────────────────────────────────
// Go does not alias `var`s, so each is re-declared here. Callers using
// `errors.Is(err, voiceover.ErrGroupNotFound)` keep working through
// the shim.

var ErrGroupNotFound = appassoc.ErrGroupNotFound

// ── Type aliases ────────────────────────────────────────────────────

type (
	Service              = appassoc.Service
	BatchRequest         = appassoc.BatchRequest
	BatchResponse        = appassoc.BatchResponse
	BatchItem            = appassoc.BatchItem
	DestinationRequest   = appassoc.DestinationRequest
	VoiceoverResult      = appassoc.VoiceoverResult
	ResolvedDestination  = appassoc.ResolvedDestination
	PromoRequest         = appassoc.PromoRequest
	PromoResponse        = appassoc.PromoResponse
	PromoResult          = appassoc.PromoResult
	LanguageTarget       = appassoc.LanguageTarget
	GroupsResolver       = appassoc.GroupsResolver
	GroupEntry           = appassoc.GroupEntry
	SemanticTaggerResult = appassoc.SemanticTaggerResult

	SemanticTaggerFunc = appassoc.SemanticTaggerFunc
	ClipIndexFunc      = appassoc.ClipIndexFunc
	TranslatorFunc     = appassoc.TranslatorFunc
)

// ── Function re-exports ─────────────────────────────────────────────

// NewService mirrors appassoc.NewService verbatim so that callers using
// concrete infrastructure types (`*config.Config`, `*sql.DB`,
// `*drive.Uploader`, `*lifecycle.Service`, `destination.Resolver`)
// compile unchanged through the shim.
func NewService(
	cfg *config.Config,
	db *sql.DB,
	pythonScriptsDir string,
	outputDir string,
	log *zap.Logger,
	driveUploader *drive.Uploader,
	lifecycleService *lifecycle.Service,
	assetDestResolver destination.Resolver,
) *Service {
	return appassoc.NewService(cfg, db, pythonScriptsDir, outputDir, log, driveUploader, lifecycleService, assetDestResolver)
}

// NewGroupsResolver mirrors appassoc.NewGroupsResolver.
func NewGroupsResolver(svc *assettree.Service, log *zap.Logger) (*GroupsResolver, error) {
	return appassoc.NewGroupsResolver(svc, log)
}

// NewVoiceoverRegistryAdapter mirrors appassoc.NewVoiceoverRegistryAdapter.
// Returns `artifacts.Registry` (the interface advertised by the canonical
// implementation) so that callers can use the returned adapter as a real
// `artifacts.Registry` and reach UpsertMedia/GetMedia/DeleteMedia/etc.
func NewVoiceoverRegistryAdapter(repo *voiceovers.Repository) artifacts.Registry {
	return appassoc.NewVoiceoverRegistryAdapter(repo)
}

// DefaultPromoLanguages mirrors appassoc.DefaultPromoLanguages.
func DefaultPromoLanguages() []LanguageTarget {
	return appassoc.DefaultPromoLanguages()
}
