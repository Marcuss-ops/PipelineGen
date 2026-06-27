// Package script (api/script) — flow.go carries the script-flow helper
// functions used by handler.go (re-exports types and functions from
// internal/application/scripts/, PR2 June 2026).
//
// resolveDriveFolderID and findFolderByNameDeep moved out; the handler
// now stores a FolderResolver closure for Drive folder resolution. Package-level aliases declared in this file remain
// for back-compat with handler callers. Cross-package script<->transport
// aliases were removed in helpers.go (use scripts.<Type> directly).

package script

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
)

// ── Type aliases (back-compat, zero churn) ─────────────────────────────────

type (
	assetSearchTarget = scripts.AssetSearchTarget

	ScriptAssetSuggestion       = scripts.ScriptAssetSuggestion
	ScriptPhraseClipSuggestion  = scripts.ScriptPhraseClipSuggestion
	ScriptDriveFolderSuggestion = scripts.ScriptDriveFolderSuggestion
	ScriptArtlistClipSuggestion = scripts.ScriptArtlistClipSuggestion
	ScriptEntityImage           = scripts.ScriptEntityImage
	ScriptInsights              = scripts.ScriptInsights
)

// EntityScriptExtractor is the canonical entity extractor interface.
type EntityScriptExtractor = scripts.EntityScriptExtractor

// ── Forwarding functions ────────────────────────────────────────────────────

func SearchScriptAssets(ctx context.Context, svc scripts.ClipServices, queries []string, targets []assetSearchTarget, limit int) []ScriptAssetSuggestion {
	return scripts.SearchScriptAssets(ctx, svc, queries, targets, limit)
}

func SearchArtlistClips(ctx context.Context, svc scripts.ClipServices, title string, phrases []string) []ScriptArtlistClipSuggestion {
	return scripts.SearchArtlistClips(ctx, svc, title, phrases)
}

func BuildPhraseClipSuggestions(ctx context.Context, svc scripts.ClipServices, title string, insights ScriptInsights, targets []assetSearchTarget) []ScriptPhraseClipSuggestion {
	return scripts.BuildPhraseClipSuggestions(ctx, svc, title, insights, targets)
}

func SearchIntroClips(ctx context.Context, svc scripts.ClipServices, title, script string, insights ScriptInsights, targets []assetSearchTarget) []ScriptAssetSuggestion {
	return scripts.SearchIntroClips(ctx, svc, title, script, insights, targets)
}

func EnrichSpecialNamesWithImages(ctx context.Context, svc scripts.ClipServices, specialNames []string) []ScriptEntityImage {
	return scripts.EnrichSpecialNamesWithImages(ctx, svc, specialNames)
}

func ExtractScriptEntities(ctx context.Context, extractor EntityScriptExtractor, script string, model string) (string, error) {
	return scripts.ExtractScriptEntities(ctx, extractor, script, model)
}

// ── ScriptInsightBuilder ────────────────────────────────────────────────────

type ScriptInsightBuilder struct {
	inner *scripts.ScriptInsightBuilder
}

func NewScriptInsightBuilder(logger *zap.Logger, maxEntities int, svc scripts.ClipServices) *ScriptInsightBuilder {
	return &ScriptInsightBuilder{
		inner: &scripts.ScriptInsightBuilder{
			Logger:      logger,
			MaxEntities: maxEntities,
			Services:    svc,
		},
	}
}

func (b *ScriptInsightBuilder) Build(ctx context.Context, title, script, entitiesJSON string) ScriptInsights {
	return b.inner.Build(ctx, title, script, entitiesJSON)
}

func ResolveRecommendedDriveFolder(ctx context.Context, svc scripts.ClipServices, title, script string, insights ScriptInsights) *ScriptDriveFolderSuggestion {
	return scripts.ResolveRecommendedDriveFolder(ctx, svc, title, script, insights)
}

// ── resolveDriveFolderID (ScriptFlowHandler method) ─────────────────────────
// Uses the handler's DriveFolderClient instead of concrete
// *drive.Uploader. Folder resolution logic unchanged.

func (h *ScriptFlowHandler) resolveDriveFolderID(ctx context.Context, input, defaultRootID string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultRootID, nil
	}

	isRawID := true
	if len(input) < 19 || len(input) > 45 {
		isRawID = false
	} else {
		for _, r := range input {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
				isRawID = false
				break
			}
		}
	}

	if isRawID {
		return input, nil
	}

	if h.driveFolderClient == nil {
		h.log.Warn("driveFolderClient not initialized, cannot resolve folder name/path; returning defaultRootID",
			zap.String("input", input))
		return defaultRootID, nil
	}

	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == '/' || r == '\\'
	})

	currentID := defaultRootID
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := h.driveFolderClient.GetOrCreateFolder(ctx, part, currentID)
		if err != nil {
			return "", fmt.Errorf("failed to get or create folder %q under %q: %w", part, currentID, err)
		}
		currentID = id
	}

	return currentID, nil
}
