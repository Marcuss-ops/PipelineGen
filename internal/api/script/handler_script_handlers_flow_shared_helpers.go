package script

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// voiceoverSceneItem describes a scene for voiceover generation.
type voiceoverSceneItem struct {
	Text       string
	SceneIndex int
}

// buildVoiceoverDestination builds a *voiceover.DestinationRequest from the
// provided parameters. It resolves voiceoverFolderID (explicit) or falls back
// to voiceoverGroup-based resolution.
//
// Resolution order (highest priority first):
//  1. voiceoverFolderID — raw Drive folder ID passed by the caller.
//  2. groupsResolver.ResolveByName(voRootID, voiceoverGroup) — DB-backed
//     category lookup (asset_tree_nodes). Avoids the legacy fall-through
//     that would create a brand-new folder instead of routing to the
//     canonical one. groupsResolver may be nil (callers without DB
//     plumbing skip this step); a non-existent name falls through to (3).
//  3. resolveFolder(...) — Drive deep-search by name (legacy).
//     Falls back to voRootID on failure (creates a new path under root).
//  4. Group label only — when nothing else matched.
func buildVoiceoverDestination(
	ctx context.Context,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	log *zap.Logger,
	title, voiceoverFolderID, voiceoverGroup, voRootID string,
	groupsResolver *voiceover.GroupsResolver,
) *voiceover.DestinationRequest {
	subfolderName := textutil.SlugifyWithMax(title, 40)

	// No magic fallback for voRootID: callers MUST pass the configured
	// voiceover root (cfg.Drive.VoiceoverRootFolder) explicitly. Empty here
	// disables DB-backed groups_resolver lookups cleanly (ResolveByName fails
	// fast) and the Drive deep-search below gracefully returns an error.
	// This keeps DB seed migration and handler in sync via config, not via
	// duplicated literals.

	// Step 1: explicit folder ID takes precedence.
	if folderID := strings.TrimSpace(voiceoverFolderID); folderID != "" {
		return &voiceover.DestinationRequest{
			FolderID:        folderID,
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}

	// Step 2: DB-backed category lookup. Nil resolver is fine (boot path
	// without asset_tree wiring); ErrGroupNotFound falls through to Drive
	// deep-search so legacy callers keep working for ad-hoc names.
	if groupsResolver != nil && strings.TrimSpace(voiceoverGroup) != "" {
		entry, err := groupsResolver.ResolveByName(ctx, voRootID, voiceoverGroup)
		switch {
		case err == nil && entry.FolderID != "":
			if log != nil {
				log.Info("routed voiceover via DB groups_resolver",
					zap.String("voiceover_group", voiceoverGroup),
					zap.String("folder_id", entry.FolderID),
					zap.String("parent_id", voRootID))
			}
			return &voiceover.DestinationRequest{
				FolderID:        entry.FolderID,
				SubfolderName:   subfolderName,
				CreateSubfolder: true,
			}
		case err != nil && !errors.Is(err, voiceover.ErrGroupNotFound):
			if log != nil {
				log.Warn("groups_resolver lookup failed unexpectedly, falling back to Drive deep-search",
					zap.String("voiceover_group", voiceoverGroup),
					zap.Error(err))
			}
		}
	}

	// Step 3: legacy Drive deep-search by name OR raw folder ID pass-through.
	targetFolderOrGroup := voiceoverGroup
	if targetFolderOrGroup != "" {
		resolvedVOFolder, err := resolveFolder(ctx, targetFolderOrGroup, voRootID)
		if err != nil {
			if log != nil {
				log.Warn("failed to resolve custom voiceover folder name/path, using default root", zap.Error(err))
			}
			resolvedVOFolder = voRootID
		}

		if resolvedVOFolder != "" {
			return &voiceover.DestinationRequest{
				FolderID:        resolvedVOFolder,
				SubfolderName:   subfolderName,
				CreateSubfolder: true,
			}
		}
	}

	// Step 4 fallback: use the caller-provided voRootID (typically the
	// script folder, set in flow_entity_images.go:262-265 as
	// `voRootID := payload.DriveFolderID`). This avoids the
	// voiceover/process.go:88 fallback to `cfg.Drive.VoiceoverFolder()`
	// (top-level voiceover root) which placed generated voiceovers in the
	// Drive root instead of alongside the script.
	if voRootID != "" {
		return &voiceover.DestinationRequest{
			FolderID:        voRootID,
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}

	// Step 5: nothing matched — use the group label as-is for bookkeeping
	// only. Will fall through to process.go:88 → cfg.Drive.VoiceoverFolder().
	grp := voiceoverGroup
	if grp == "" {
		grp = "curation"
	}
	return &voiceover.DestinationRequest{
		Group:           grp,
		SubfolderName:   subfolderName,
		CreateSubfolder: true,
	}
}

// generateSceneVoiceovers generates voiceovers for each scene item.
// Returns the number of successful generations.
func generateSceneVoiceovers(
	ctx context.Context,
	voService *voiceover.Service,
	scenes []voiceoverSceneItem,
	language string,
	destReq *voiceover.DestinationRequest,
	log *zap.Logger,
	onProgress func(pct int, msg string),
	basePct, pctRange int,
) int {
	if voService == nil || destReq == nil || len(scenes) == 0 {
		return 0
	}
	voCtx := context.WithoutCancel(ctx)
	successCount := 0
	for i, sc := range scenes {
		sceneText := strings.TrimSpace(sc.Text)
		if sceneText == "" {
			continue
		}
		sceneSlug := textutil.SlugifyWithMax(sceneText, 30)
		filename := sceneSlug

		if onProgress != nil && len(scenes) > 0 {
			onProgress(basePct+(i*pctRange/len(scenes)), "")
		}

		voRes, voErr := voService.GenerateWithDestination(voCtx, sceneText, language, filename, destReq)
		if voErr != nil {
			if log != nil {
				log.Warn("voiceover generation failed for scene",
					zap.Int("scene_index", sc.SceneIndex),
					zap.Error(voErr))
			}
			continue
		}
		if voRes != nil {
			successCount++
		}
	}
	return successCount
}

// resolveDriveFolderID takes an input which could be a raw folder ID or a folder name/path.
// If it is a name or a path, it searches for it or walks/creates folders under the given defaultRootID on Google Drive
// using the uploader, returning the resolved folder ID.
func (h *ScriptFlowHandler) resolveDriveFolderID(ctx context.Context, input, defaultRootID string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultRootID, nil
	}

	// Helper to check if it's already a raw ID:
	// Google Drive IDs are typically 19 to 45 characters of [a-zA-Z0-9_-]
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

	// It's a path or name.
	if h.driveUploader == nil || h.driveUploader.Service == nil {
		h.log.Warn("driveUploader not initialized, cannot resolve folder name/path; returning defaultRootID", zap.String("input", input))
		return defaultRootID, nil
	}

	// Dynamic deep search: try to find an existing folder matching this name (1-2 levels deep) under defaultRootID
	if foundID, err := h.findFolderByNameDeep(ctx, input, defaultRootID); err == nil && foundID != "" {
		h.log.Info("found existing folder dynamically on Google Drive", zap.String("name", input), zap.String("folder_id", foundID))
		return foundID, nil
	}

	// Fallback: build the path segments under defaultRootID
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

// findFolderByNameDeep searches for a folder by name directly under rootID or 1 level deeper (subfolders).
func (h *ScriptFlowHandler) findFolderByNameDeep(ctx context.Context, name, rootID string) (string, error) {
	if h.driveUploader == nil || h.driveUploader.Service == nil {
		return "", fmt.Errorf("drive uploader not initialized")
	}
	targetClean := fileutil.CleanFolderName(name)

	// 1. Search directly under the root folder
	query := fmt.Sprintf("'%s' in parents and trashed = false and mimeType = 'application/vnd.google-apps.folder'", rootID)
	list, err := h.driveUploader.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err == nil && len(list.Files) > 0 {
		for _, file := range list.Files {
			if fileutil.CleanFolderName(file.Name) == targetClean {
				return file.Id, nil
			}
		}

		// 2. Search one level deep (look inside each subfolder of the root)
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

// buildTextOnlyScriptPlan builds a plan for text-only script generation.
// This pattern is used by HandleClipScriptGenerateJob (Path 3 text-only fallback).
func buildTextOnlyScriptPlan(
	topic, sourceText, guidelines, title, language, tone, model string,
	forceRefresh, saveToDB bool, targetWords int,
	promptVersion, editorPromptVersion, qaPromptVersion string,
) *scripts.ScriptGenerationPlan {
	if topic == "" {
		topic = sourceText
	}
	if title == "" {
		title = topic
	}

	plan := scripts.NewPlan()
	plan.Title = title
	plan.Topic = topic
	plan.Language = language
	plan.Tone = tone
	plan.Model = model
	plan.Mode = gemmamemory.ModeGenerate
	plan.UseMemory = !forceRefresh
	plan.SaveToDB = saveToDB
	plan.TargetWords = targetWords
	plan.Prompt = topic
	plan.SourceText = sourceText
	plan.Guidelines = guidelines
	plan.PromptVersion = promptVersion
	plan.EditorPromptVersion = editorPromptVersion
	plan.QAPromptVersion = qaPromptVersion
	return plan
}
