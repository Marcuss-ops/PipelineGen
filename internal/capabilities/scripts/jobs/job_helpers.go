// Package scripts — job helpers extracted from api/script/handler_jobs.go (PR2, June 2026).
package jobs

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"

	"github.com/Marcuss-ops/PipelineGen/pkg/background"
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
	groupsResolver ports.VoiceoverGroupResolver,
) *voiceover.DestinationRequest {
	rawVoiceoverFolderID := strings.TrimSpace(voiceoverFolderID)
	voiceoverFolderID = clips.ExtractDriveFolderID(rawVoiceoverFolderID)
	voRootID = clips.ExtractDriveFolderID(strings.TrimSpace(voRootID))
	subfolderName := textutil.SlugifyWithMax(title, 40)

	if rawVoiceoverFolderID != "" &&
		(strings.HasPrefix(rawVoiceoverFolderID, "http://") || strings.HasPrefix(rawVoiceoverFolderID, "https://")) &&
		voiceoverFolderID == rawVoiceoverFolderID {
		// Preserve explicit caller intent as an explicit request, but
		// leave FolderID empty so the canonical resolver fails closed.
		// Never continue into group/config/default fallback for a
		// malformed explicit Drive URL.
		return &voiceover.DestinationRequest{
			Kind:            string(voiceover.KindExplicit),
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}

	if folderID := strings.TrimSpace(voiceoverFolderID); folderID != "" {
		return &voiceover.DestinationRequest{
			Kind:            string(voiceover.KindExplicit),
			FolderID:        folderID,
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}

	if groupsResolver != nil && strings.TrimSpace(voiceoverGroup) != "" {
		folderID, err := groupsResolver.ResolveGroup(ctx, voRootID, voiceoverGroup)
		switch {
		case err == nil && folderID != "":
			if log != nil {
				log.Info("routed voiceover via DB groups_resolver",
					zap.String("voiceover_group", voiceoverGroup),
					zap.String("folder_id", folderID),
					zap.String("parent_id", voRootID))
			}
			return &voiceover.DestinationRequest{
				Kind:            string(voiceover.KindExplicit),
				FolderID:        folderID,
				SubfolderName:   subfolderName,
				CreateSubfolder: true,
			}
		case err != nil && !errors.Is(err, ports.ErrVoiceoverGroupNotFound):
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
				Kind:            string(voiceover.KindExplicit),
				FolderID:        resolvedVOFolder,
				SubfolderName:   subfolderName,
				CreateSubfolder: true,
			}
		}
	}

	if voRootID != "" {
		return &voiceover.DestinationRequest{
			Kind:            string(voiceover.KindExplicit),
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
//
// P0-#3 final closure (July 2026): the `voExecutor` parameter is now
// `voiceover.VoiceoverItemExecutor` (the canonical narrow port per
// AGENTS.md Pattern 0). The legacy `*voiceover.Service` concrete
// parameter is RETIRED — the production concrete implementation
// (`*voiceover.ProcessVoiceoverItemUseCase`) is injected at
// composition time in `internal/app/build_bundles_voiceover.go`.
// Test stubs implement the single Execute method.
func GenerateSceneVoiceovers(
	ctx context.Context,
	voExecutor voiceover.VoiceoverItemExecutor,
	scenes []VoiceoverSceneItem,
	language string,
	destReq *voiceover.DestinationRequest,
	log *zap.Logger,
	onProgress func(pct int, msg string),
	basePct, pctRange int,
) int {
	if voExecutor == nil || destReq == nil || len(scenes) == 0 {
		return 0
	}
	// AGENTS.md §7 post-write save ctx — voiceover job helper writes
	// must survive the request ctx cancel; the save-context is bounded
	// by the script-job lifetime (30m timeout), not the request that
	// triggered it.
	voCtx, voCancel := background.DetachWithTimeout(ctx, "voiceover-generation", 30*time.Minute)
	defer voCancel()
	inputs := make([]adapterspkg.VoiceoverSceneInput, 0, len(scenes))
	for _, sc := range scenes {
		sceneText := strings.TrimSpace(sc.Text)
		if sceneText == "" {
			continue
		}
		sceneSlug := textutil.SlugifyWithMax(sceneText, 30)
		inputs = append(inputs, adapterspkg.VoiceoverSceneInput{
			SceneIndex:  sc.SceneIndex,
			Text:        sceneText,
			Filename:    sceneSlug,
			Destination: destReq,
		})
	}
	// P0-#3 final closure (July 2026): the fanout now takes the
	// canonical VoiceoverItemExecutor port; real failures surface as
	// typed Go errors per scene (no Result{OK:false} masking).
	outcomes := adapterspkg.RunVoiceoverSceneFanout(voCtx, voExecutor, language, inputs, 4)
	successCount := adapterspkg.CountCompletedSceneOutcomes(outcomes)
	if log != nil {
		for _, out := range outcomes {
			if out.Status != "failed" {
				continue
			}
			log.Warn("voiceover generation failed for scene",
				zap.Int("scene_index", out.SceneIndex),
				zap.String("error", out.Error))
		}
	}
	if onProgress != nil && len(outcomes) > 0 {
		for i := range outcomes {
			onProgress(basePct+((i+1)*pctRange/len(outcomes)), "")
		}
	}
	return successCount
}
