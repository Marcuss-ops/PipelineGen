// Package script (api/script) — flow.go carries cross-cutting helpers
// for the script-flow transport that depends on the canonical types
// in internal/application/scripts/.
//
// PR3 (June 2026): removed 8 back-compat type aliases that re-exported
// `scripts.AssetSearchTarget`, `scripts.ScriptAssetSuggestion`,
// `scripts.ScriptPhraseClipSuggestion`, `scripts.ScriptDriveFolderSuggestion`,
// `scripts.ScriptArtlistClipSuggestion`, `scripts.ScriptEntityImage`,
// `scripts.ScriptInsights`, and `scripts.EntityScriptExtractor`. New
// code MUST consume the canonical types directly via the `scripts.X`
// qualifier; the bare names that existed before this PR are no longer
// resolvable in `script.*`.
//
// The thin pass-through helpers below (SearchScriptAssets,
// SearchArtlistClips, BuildPhraseClipSuggestions, SearchIntroClips,
// EnrichSpecialNamesWithImages, ExtractScriptEntities,
// ScriptInsightBuilder.NewScriptInsightBuilder/Build,
// ResolveRecommendedDriveFolder) are preserved because their
// receivers live outside this package; their signatures now use the
// canonical `scripts.X` types.
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

// ── SearchScriptAssets ──────────────────────────────────────────────────────

// SearchScriptAssets delegates to the application layer.
func SearchScriptAssets(ctx context.Context, svc scripts.ClipServices, queries []string, targets []scripts.AssetSearchTarget, limit int) []scripts.ScriptAssetSuggestion {
	return scripts.SearchScriptAssets(ctx, svc, queries, targets, limit)
}

// ── SearchArtlistClips ──────────────────────────────────────────────────────

// SearchArtlistClips delegates to the application layer.
func SearchArtlistClips(ctx context.Context, svc scripts.ClipServices, title string, phrases []string) []scripts.ScriptArtlistClipSuggestion {
	return scripts.SearchArtlistClips(ctx, svc, title, phrases)
}

// ── BuildPhraseClipSuggestions + SearchIntroClips ───────────────────────────

// BuildPhraseClipSuggestions delegates to the application layer.
func BuildPhraseClipSuggestions(ctx context.Context, svc scripts.ClipServices, title string, insights scripts.ScriptInsights, targets []scripts.AssetSearchTarget) []scripts.ScriptPhraseClipSuggestion {
	return scripts.BuildPhraseClipSuggestions(ctx, svc, title, insights, targets)
}

// SearchIntroClips delegates to the application layer.
func SearchIntroClips(ctx context.Context, svc scripts.ClipServices, title, script string, insights scripts.ScriptInsights, targets []scripts.AssetSearchTarget) []scripts.ScriptAssetSuggestion {
	return scripts.SearchIntroClips(ctx, svc, title, script, insights, targets)
}

// ── Entity image enrichment ─────────────────────────────────────────────────

// EnrichSpecialNamesWithImages delegates to the application layer.
func EnrichSpecialNamesWithImages(ctx context.Context, svc scripts.ClipServices, specialNames []string) []scripts.ScriptEntityImage {
	return scripts.EnrichSpecialNamesWithImages(ctx, svc, specialNames)
}

// ── Entity extraction ───────────────────────────────────────────────────────

// ExtractScriptEntities delegates to the application layer.
func ExtractScriptEntities(ctx context.Context, extractor scripts.EntityScriptExtractor, script string, model string) (string, error) {
	return scripts.ExtractScriptEntities(ctx, extractor, script, model)
}

// ── ScriptInsightBuilder ────────────────────────────────────────────────────

// ScriptInsightBuilder wraps the canonical scripts.ScriptInsightBuilder.
//
// All references in this file have been migrated to the bare scripts.*
// types (PR3 type-alias cleanup, June 2026). The wrapper struct remains
// because the handler receiver keeps it as a field; the receiver methods
// (Build) just shim through to the canonical implementation.
type ScriptInsightBuilder struct {
	inner *scripts.ScriptInsightBuilder
}

// NewScriptInsightBuilder creates a new insight builder from ClipServices.
func NewScriptInsightBuilder(logger *zap.Logger, maxEntities int, svc scripts.ClipServices) *ScriptInsightBuilder {
	return &ScriptInsightBuilder{
		inner: &scripts.ScriptInsightBuilder{
			Logger:      logger,
			MaxEntities: maxEntities,
			Services:    svc,
		},
	}
}

// Build constructs ScriptInsights from the entity analysis JSON.
func (b *ScriptInsightBuilder) Build(ctx context.Context, title, script, entitiesJSON string) scripts.ScriptInsights {
	return b.inner.Build(ctx, title, script, entitiesJSON)
}

// ── ResolveRecommendedDriveFolder ───────────────────────────────────────────

// ResolveRecommendedDriveFolder delegates to the application layer.
func ResolveRecommendedDriveFolder(ctx context.Context, svc scripts.ClipServices, title, script string, insights scripts.ScriptInsights) *scripts.ScriptDriveFolderSuggestion {
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
