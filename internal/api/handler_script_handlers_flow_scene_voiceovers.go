package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	concurrent "github.com/Marcuss-ops/PipelineGen/internal/platform"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// generateSceneVoiceovers generates voiceovers for each scene in parallel.
func (h *ScriptFlowHandler) generateSceneVoiceovers(ctx context.Context, payload *jobPayloadUnified, scenes []ScriptSceneImage) []SceneVoiceover {
	if h.clipServices.VoSvc == nil || !payload.GenerateVoiceover || len(scenes) == 0 {
		return nil
	}

	subfolderName := textutil.SlugifyWithMax(payload.Title, 40)
	if subfolderName == "" {
		subfolderName = "scenes"
	}

	voRootID := payload.VoiceoverFolderID
	if voRootID == "" {
		voRootID = payload.DriveFolderID
	}
	if voRootID == "" && h.cfg != nil {
		voRootID = h.cfg.Drive.VoiceoverFolder()
	}

	var mu sync.Mutex
	results := make([]SceneVoiceover, len(scenes))
	group, groupCtx := concurrent.WithContext(ctx)

	for idx, scene := range scenes {
		idx := idx
		scene := scene

		group.Go(fmt.Sprintf("scene-voiceover-%d", idx), func() error {
			cleanedText := textutil.CleanForVoiceover(scene.Text)
			if strings.TrimSpace(cleanedText) == "" {
				mu.Lock()
				results[idx] = SceneVoiceover{SceneIndex: idx, Status: "skipped"}
				mu.Unlock()
				return nil
			}

			voFilename := fmt.Sprintf("scene-%d_%s.mp3", idx, payload.Language)
			voDest := h.resolveSceneVoiceoverDestination(ctx, payload.VoiceoverFolderID, payload.VoiceoverGroup, voRootID, subfolderName)

			voCtx, voCancel := context.WithTimeout(groupCtx, 90*time.Second)
			defer voCancel()

			voResult, err := h.clipServices.VoSvc.GenerateWithDestination(voCtx, cleanedText, payload.Language, voFilename, voDest)
			if err != nil {
				h.log.Warn("failed to generate voiceover for scene",
					zap.Int("scene_idx", idx), zap.Error(err))
				mu.Lock()
				results[idx] = SceneVoiceover{SceneIndex: idx, Status: "failed"}
				mu.Unlock()
				return nil
			}

			mu.Lock()
			results[idx] = SceneVoiceover{SceneIndex: idx, Link: voResult.DriveLink, LocalPath: voResult.Path, Status: "completed"}
			mu.Unlock()

			return nil
		})
	}

	_ = group.Wait()
	return results
}

// resolveSceneVoiceoverDestination extracts the routing decision for a per-scene voiceover.
func (h *ScriptFlowHandler) resolveSceneVoiceoverDestination(
	ctx context.Context,
	voiceoverFolderID, voiceoverGroup, voRootID, subfolderName string,
) *voiceover.DestinationRequest {
	dest := buildVoiceoverDestination(
		ctx,
		h.resolveDriveFolderID,
		h.log,
		subfolderName,
		voiceoverFolderID,
		voiceoverGroup,
		voRootID,
		h.groupsResolver,
	)
	if dest == nil {
		dest = &voiceover.DestinationRequest{
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}
	if dest.SubfolderName == "" {
		dest.SubfolderName = subfolderName
	}
	return dest
}
