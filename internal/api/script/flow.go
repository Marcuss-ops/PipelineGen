// Package script (api/script) — flow.go carries the script-flow helper
// functions used by handler.go (re-exports types and functions from
// internal/application/scripts/, PR2 June 2026).
//
// resolveDriveFolderID and findFolderByNameDeep moved out; the handler
// now stores a FolderResolver closure for Drive folder resolution. Package-level aliases declared in this file remain
// for back-compat with handler callers. Cross-package script<->transport
// aliases were removed in helpers.go (use usecase.<Type> directly).

package script

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
)

// ── Type aliases (back-compat, zero churn) ─────────────────────────────────

type (
	assetSearchTarget = usecase.AssetSearchTarget

	ScriptAssetSuggestion       = usecase.ScriptAssetSuggestion
	ScriptPhraseClipSuggestion  = usecase.ScriptPhraseClipSuggestion
	ScriptDriveFolderSuggestion = usecase.ScriptDriveFolderSuggestion
	ScriptArtlistClipSuggestion = usecase.ScriptArtlistClipSuggestion
	ScriptEntityImage           = usecase.ScriptEntityImage
	ScriptInsights              = usecase.ScriptInsights
)

// EntityScriptExtractor is the canonical entity extractor interface.
type EntityScriptExtractor = usecase.EntityScriptExtractor

// ── Forwarding functions ────────────────────────────────────────────────────

func SearchScriptAssets(ctx context.Context, svc usecase.ClipServices, queries []string, targets []assetSearchTarget, limit int) []ScriptAssetSuggestion {
	return usecase.SearchScriptAssets(ctx, svc, queries, targets, limit)
}

func SearchArtlistClips(ctx context.Context, svc usecase.ClipServices, title string, phrases []string) []ScriptArtlistClipSuggestion {
	return usecase.SearchArtlistClips(ctx, svc, title, phrases)
}

func BuildPhraseClipSuggestions(ctx context.Context, svc usecase.ClipServices, title string, insights ScriptInsights, targets []assetSearchTarget) []ScriptPhraseClipSuggestion {
	return usecase.BuildPhraseClipSuggestions(ctx, svc, title, insights, targets)
}

func SearchIntroClips(ctx context.Context, svc usecase.ClipServices, title, script string, insights ScriptInsights, targets []assetSearchTarget) []ScriptAssetSuggestion {
	return usecase.SearchIntroClips(ctx, svc, title, script, insights, targets)
}

func EnrichSpecialNamesWithImages(ctx context.Context, svc usecase.ClipServices, specialNames []string) []ScriptEntityImage {
	return usecase.EnrichSpecialNamesWithImages(ctx, svc, specialNames)
}

func ExtractScriptEntities(ctx context.Context, extractor EntityScriptExtractor, script string, model string) (string, error) {
	return usecase.ExtractScriptEntities(ctx, extractor, script, model)
}

// ── ScriptInsightBuilder ────────────────────────────────────────────────────

type ScriptInsightBuilder struct {
	inner *usecase.ScriptInsightBuilder
}

func NewScriptInsightBuilder(logger *zap.Logger, maxEntities int, svc usecase.ClipServices) *ScriptInsightBuilder {
	return &ScriptInsightBuilder{
		inner: &usecase.ScriptInsightBuilder{
			Logger:      logger,
			MaxEntities: maxEntities,
			Services:    svc,
		},
	}
}

func (b *ScriptInsightBuilder) Build(ctx context.Context, title, script, entitiesJSON string) ScriptInsights {
	return b.inner.Build(ctx, title, script, entitiesJSON)
}

func ResolveRecommendedDriveFolder(ctx context.Context, svc usecase.ClipServices, title, script string, insights ScriptInsights) *ScriptDriveFolderSuggestion {
	return usecase.ResolveRecommendedDriveFolder(ctx, svc, title, script, insights)
}

// resolveDriveFolderID retired (July 2026) as dead code per
// PR-SCRIPT-FACADE-EXTRACT — canonical impl moved to
// FacadeHandler.ResolveDriveFolderID (handler_facade.go). The
// ScriptFlowHandler.ResolveDriveFolderID public method is now a
// thin delegator. Pre-extraction the helper lived here as a method
// on ScriptFlowHandler (lowercase = package-private) with a single
// caller (the public ScriptFlowHandler.ResolveDriveFolderID); post-
// extraction that single caller hops to h.facade.ResolveDriveFolderID
// and the lowercase helper becomes unused — removed per godlike/07
// minimum-blast-radius dead-code discipline.
