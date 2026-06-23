// Package script (api/script) — flow.go re-exports types and functions
// from internal/application/scripts/ (PR2, June 2026).
//
// The business logic that was previously in this file has been extracted
// to the application layer. This file keeps only:
//   - Type aliases for back-compat (existing callers use the same names)
//   - ScriptFlowHandler receiver methods that depend on the handler's
//     infrastructure fields (resolveDriveFolderID, findFolderByNameDeep)
package script

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// ── Type aliases (back-compat, zero churn) ─────────────────────────────────

type (
	assetSearchTarget = scripts.AssetSearchTarget

	ScriptAssetSuggestion        = scripts.ScriptAssetSuggestion
	ScriptPhraseClipSuggestion   = scripts.ScriptPhraseClipSuggestion
	ScriptDriveFolderSuggestion  = scripts.ScriptDriveFolderSuggestion
	ScriptArtlistClipSuggestion  = scripts.ScriptArtlistClipSuggestion
	ScriptEntityImage            = scripts.ScriptEntityImage
	ScriptInsights               = scripts.ScriptInsights
)

// EntityScriptExtractor is the canonical entity extractor interface.
type EntityScriptExtractor = scripts.EntityScriptExtractor

// ── SearchScriptAssets ──────────────────────────────────────────────────────

// SearchScriptAssets delegates to the application layer.
func SearchScriptAssets(ctx context.Context, svc ClipServices, queries []string, targets []assetSearchTarget, limit int) []ScriptAssetSuggestion {
	return scripts.SearchScriptAssets(ctx, svc, queries, targets, limit)
}

// ── SearchArtlistClips ──────────────────────────────────────────────────────

// SearchArtlistClips delegates to the application layer.
func SearchArtlistClips(ctx context.Context, svc ClipServices, title string, phrases []string) []ScriptArtlistClipSuggestion {
	return scripts.SearchArtlistClips(ctx, svc, title, phrases)
}

// ── BuildPhraseClipSuggestions + SearchIntroClips ───────────────────────────

// BuildPhraseClipSuggestions delegates to the application layer.
func BuildPhraseClipSuggestions(ctx context.Context, svc ClipServices, title string, insights ScriptInsights, targets []assetSearchTarget) []ScriptPhraseClipSuggestion {
	return scripts.BuildPhraseClipSuggestions(ctx, svc, title, insights, targets)
}

// SearchIntroClips delegates to the application layer.
func SearchIntroClips(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights, targets []assetSearchTarget) []ScriptAssetSuggestion {
	return scripts.SearchIntroClips(ctx, svc, title, script, insights, targets)
}

// ── Entity image enrichment ─────────────────────────────────────────────────

// EnrichSpecialNamesWithImages delegates to the application layer.
func EnrichSpecialNamesWithImages(ctx context.Context, svc ClipServices, specialNames []string) []ScriptEntityImage {
	return scripts.EnrichSpecialNamesWithImages(ctx, svc, specialNames)
}

// ── Entity extraction ───────────────────────────────────────────────────────

// ExtractScriptEntities delegates to the application layer.
func ExtractScriptEntities(ctx context.Context, extractor EntityScriptExtractor, script string, model string) (string, error) {
	return scripts.ExtractScriptEntities(ctx, extractor, script, model)
}

// ── ScriptInsightBuilder ────────────────────────────────────────────────────

// ScriptInsightBuilder delegates to the application layer.
type ScriptInsightBuilder struct {
	inner *scripts.ScriptInsightBuilder
}

// NewScriptInsightBuilder creates a new insight builder from ClipServices.
func NewScriptInsightBuilder(logger *zap.Logger, maxEntities int, svc ClipServices) *ScriptInsightBuilder {
	return &ScriptInsightBuilder{
		inner: &scripts.ScriptInsightBuilder{
			Logger:      logger,
			MaxEntities: maxEntities,
			Services:    svc,
		},
	}
}

// Build constructs ScriptInsights from the entity analysis JSON.
func (b *ScriptInsightBuilder) Build(ctx context.Context, title, script, entitiesJSON string) ScriptInsights {
	return b.inner.Build(ctx, title, script, entitiesJSON)
}

// ── ResolveRecommendedDriveFolder ───────────────────────────────────────────

// ResolveRecommendedDriveFolder delegates to the application layer.
func ResolveRecommendedDriveFolder(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights) *ScriptDriveFolderSuggestion {
	return scripts.ResolveRecommendedDriveFolder(ctx, svc, title, script, insights)
}

// ── buildTextOnlyScriptPlan ─────────────────────────────────────────────────

func buildTextOnlyScriptPlan(
	topic, sourceText, guidelines, title, language, tone, model string,
	forceRefresh, saveToDB bool, targetWords int,
	promptVersion, editorPromptVersion, qaPromptVersion string,
) *scriptpkg.ScriptGenerationPlan {
	return scripts.BuildTextOnlyScriptPlan(
		topic, sourceText, guidelines, title, language, tone, model,
		forceRefresh, saveToDB, targetWords,
		promptVersion, editorPromptVersion, qaPromptVersion,
	)
}

// ── resolveDriveFolderID + findFolderByNameDeep (ScriptFlowHandler methods) ─

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

	if h.driveUploader == nil || h.driveUploader.Service == nil {
		h.log.Warn("driveUploader not initialized, cannot resolve folder name/path; returning defaultRootID", zap.String("input", input))
		return defaultRootID, nil
	}

	if foundID, err := h.findFolderByNameDeep(ctx, input, defaultRootID); err == nil && foundID != "" {
		h.log.Info("found existing folder dynamically on Google Drive", zap.String("name", input), zap.String("folder_id", foundID))
		return foundID, nil
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
		id, err := h.driveUploader.GetOrCreateFolder(ctx, part, currentID)
		if err != nil {
			return "", fmt.Errorf("failed to get or create folder %q under %q: %w", part, currentID, err)
		}
		currentID = id
	}

	return currentID, nil
}

func (h *ScriptFlowHandler) findFolderByNameDeep(ctx context.Context, name, rootID string) (string, error) {
	if h.driveUploader == nil || h.driveUploader.Service == nil {
		return "", fmt.Errorf("drive uploader not initialized")
	}
	targetClean := fileutil.CleanFolderName(name)

	query := fmt.Sprintf("'%s' in parents and trashed = false and mimeType = 'application/vnd.google-apps.folder'", rootID)
	list, err := h.driveUploader.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err == nil && len(list.Files) > 0 {
		for _, file := range list.Files {
			if fileutil.CleanFolderName(file.Name) == targetClean {
				return file.Id, nil
			}
		}

		for _, subDir := range list.Files {
			subQuery := fmt.Sprintf("'%s' in parents and trashed = false and mimeType = 'application/vnd.google-apps.folder'", subDir.Id)
			subList, subErr := h.driveUploader.Service.Files.List().Q(subQuery).Fields("files(id, name)").Context(ctx).Do()
			if subErr == nil && len(subList.Files) > 0 {
				for _, file := range subList.Files {
					if fileutil.CleanFolderName(file.Name) == targetClean {
						return file.Id, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("folder %q not found", name)
}


