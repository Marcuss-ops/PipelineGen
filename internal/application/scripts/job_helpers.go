// Package scripts — job helpers extracted from api/script/handler_jobs.go (PR2, June 2026).
package scripts

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Pipeline stage logger
func StageLog(log *zap.Logger, jobID, stage string) func(extra ...zap.Field) {
	t := time.Now()
	log.Info("pipeline_stage_started",
		zap.String("job_id", jobID),
		zap.String("stage", stage))
	return func(extra ...zap.Field) {
		fields := append([]zap.Field{
			zap.String("job_id", jobID),
			zap.String("stage", stage),
			zap.Int64("duration_ms", time.Since(t).Milliseconds()),
		}, extra...)
		log.Info("pipeline_stage_completed", fields...)
	}
}

// ── buildVoiceoverDestination ────────────────────────────────────────────────

// BuildVoiceoverDestination builds a *voiceover.DestinationRequest from the
// provided parameters.
func BuildVoiceoverDestination(
	ctx context.Context,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	log *zap.Logger,
	title, voiceoverFolderID, voiceoverGroup, voRootID string,
	groupsResolver *voiceover.GroupsResolver,
) *voiceover.DestinationRequest {
	subfolderName := textutil.SlugifyWithMax(title, 40)

	if folderID := strings.TrimSpace(voiceoverFolderID); folderID != "" {
		return &voiceover.DestinationRequest{
			FolderID:        folderID,
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}

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

	targetFolderOrGroup := voiceoverGroup
	if targetFolderOrGroup != "" && resolveFolder != nil {
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

	if voRootID != "" {
		return &voiceover.DestinationRequest{
			FolderID:        voRootID,
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}

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

// ── Voiceover scene item ────────────────────────────────────────────────────

type VoiceoverSceneItem struct {
	Text       string
	SceneIndex int
}

// ── generateSceneVoiceovers ──────────────────────────────────────────────────

// GenerateSceneVoiceovers generates voiceovers for each scene item.
func GenerateSceneVoiceovers(
	ctx context.Context,
	voService *voiceover.Service,
	scenes []VoiceoverSceneItem,
	language string,
	destReq *voiceover.DestinationRequest,
	log *zap.Logger,
	onProgress func(pct int, msg string),
	basePct, pctRange int,
) int {
	if voService == nil || destReq == nil || len(scenes) == 0 {
		return 0
	}
	// AGENTS.md §7 post-write save ctx — voiceover job helper writes
	// must survive the request ctx cancel; the save-context is bounded
	// by the script-job lifetime, not the request that triggered it.
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

// ── Text helpers ─────────────────────────────────────────────────────────────

func countWords(text string) int {
	return len(strings.Fields(text))
}

func approxReadingSeconds(words int) int {
	if words <= 0 {
		return 0
	}
	return max(1, (words*60)/150)
}
