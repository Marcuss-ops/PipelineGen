package script

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// voiceoverSceneItem describes a scene for voiceover generation.
type voiceoverSceneItem struct {
	Text       string
	SceneIndex int
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
