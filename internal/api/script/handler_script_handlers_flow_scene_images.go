package script

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/database/drive"
	concurrent "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// sceneImageParallelism bounds concurrent scene image-generation goroutines.
// Tunable via VELOX_SCENE_PARALLELISM (1..4, default 2).
const (
	sceneParallelismDefault = 2
	sceneParallelismMin     = 1
	sceneParallelismMax     = 4
)

var (
	sceneImageParallelism     = loadSceneImageParallelism(os.Getenv("VELOX_SCENE_PARALLELISM"))
	sceneParallelismInitNote  = sceneParallelismClampNote(os.Getenv("VELOX_SCENE_PARALLELISM"))
	sceneParallelismClampOnce sync.Once
)

func loadSceneImageParallelism(raw string) int {
	if raw == "" {
		return sceneParallelismDefault
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < sceneParallelismMin {
		return sceneParallelismMin
	}
	if v > sceneParallelismMax {
		return sceneParallelismMax
	}
	return v
}

func sceneParallelismClampNote(raw string) string {
	if raw == "" {
		return ""
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Sprintf("VELOX_SCENE_PARALLELISM=%q is not an integer; clamped to %d (min)", raw, sceneParallelismMin)
	}
	if v < sceneParallelismMin {
		return fmt.Sprintf("VELOX_SCENE_PARALLELISM=%d below min %d; clamped to %d", v, sceneParallelismMin, sceneParallelismMin)
	}
	if v > sceneParallelismMax {
		return fmt.Sprintf("VELOX_SCENE_PARALLELISM=%d above max %d; clamped to %d", v, sceneParallelismMax, sceneParallelismMax)
	}
	return ""
}

// generateSceneImages splits the script into scenes and generates AI images.
func (h *ScriptFlowHandler) generateSceneImages(ctx context.Context, payload *jobPayloadUnified, script string, tools *jobservice.JobTools) []ScriptSceneImage {
	if h.clipServices.ImgSvc == nil || strings.TrimSpace(script) == "" {
		return nil
	}

	sceneParallelismClampOnce.Do(func() {
		if sceneParallelismInitNote == "" {
			return
		}
		h.log.Warn("scene_parallelism_clamped",
			zap.String("note", sceneParallelismInitNote),
			zap.Int("effective_parallelism", sceneImageParallelism),
			zap.String("env_var", "VELOX_SCENE_PARALLELISM"))
	})

	sentencesPerImage := payload.SentencesPerImage
	if sentencesPerImage <= 0 {
		sentencesPerImage = 10
	}

	scenesText := splitScriptIntoSceneImages(script, sentencesPerImage)
	if len(scenesText) == 0 {
		return nil
	}

	style := payload.Style
	if style == "" {
		style = "realistic"
	}

	imagesPerScene := payload.ImagesPerScene
	if imagesPerScene <= 0 {
		imagesPerScene = 1
	}

	h.log.Info("scene_images_spawn",
		zap.Int("total_scenes", len(scenesText)),
		zap.Int("images_per_scene", imagesPerScene),
		zap.Int("parallelism", sceneImageParallelism),
		zap.String("style", style),
		zap.Int("sentences_per_image", sentencesPerImage),
		zap.Int("script_chars", len(script)))

	sem := make(chan struct{}, sceneImageParallelism)
	var mu sync.Mutex
	results := make([]ScriptSceneImage, len(scenesText))

	group, groupCtx := concurrent.WithContext(ctx)
	startedAll := time.Now()

	var sceneCompleted int32
	totalScenes := len(scenesText)

	for idx, para := range scenesText {
		idx := idx
		para := para

		group.Go(fmt.Sprintf("scene-image-%d", idx), func() error {
			sceneStart := time.Now()

			select {
			case sem <- struct{}{}:
			case <-groupCtx.Done():
				return groupCtx.Err()
			}
			defer func() { <-sem }()

			h.log.Info("scene_image_started",
				zap.Int("scene_idx", idx),
				zap.Int("total_scenes", len(scenesText)),
				zap.Int("para_chars", len(para)))

			shortPrompt := truncatePrompt(para, 200)
			var imageLinks []string
			var failedVariations int

			for i := 0; i < imagesPerScene; i++ {
				if groupCtx.Err() != nil {
					break
				}

				promptWithVar := shortPrompt
				if i > 0 {
					promptWithVar = shortPrompt + fmt.Sprintf(", variation %d", i+1)
				}

				aiCtx, aiCancel := context.WithTimeout(groupCtx, 90*time.Second)
				aiAsset, err := h.clipServices.ImgSvc.GenerateSmartImage(aiCtx, shortPrompt, para, style, []string{promptWithVar}, []string{"scene", fmt.Sprintf("scene-%d", idx)}, 1024, 1024, "", false)
				aiCancel()

				if err == nil && aiAsset != nil {
					fileID := strings.TrimSpace(aiAsset.DriveFileID)
					if fileID != "" {
						link := drive.FileURLFromID(fileID)
						imageLinks = append(imageLinks, link)
						h.log.Info("scene_image_variation_completed",
							zap.Int("scene_idx", idx), zap.Int("var_idx", i),
							zap.String("drive_link", link))
					}
				} else {
					failedVariations++
					h.log.Warn("scene_image_variation_failed",
						zap.Int("scene_idx", idx), zap.Int("var_idx", i), zap.Error(err))
				}
			}

			var mainImage string
			if len(imageLinks) > 0 {
				mainImage = imageLinks[0]
			}

			mu.Lock()
			results[idx] = ScriptSceneImage{
				Text:   para,
				Image:  mainImage,
				Images: imageLinks,
			}
			mu.Unlock()

			h.log.Info("scene_image_completed",
				zap.Int("scene_idx", idx),
				zap.Int("variations_ok", len(imageLinks)),
				zap.Int("variations_failed", failedVariations),
				zap.Int64("duration_ms", time.Since(sceneStart).Milliseconds()))

			done := int(atomic.AddInt32(&sceneCompleted, 1))
			if tools != nil && tools.Progress != nil && totalScenes > 0 {
				pct := 70 + (10 * done / totalScenes)
				tools.Progress(pct, fmt.Sprintf("Scene %d/%d finished (%d/%d ok)",
					done, totalScenes, len(imageLinks), imagesPerScene))
			}

			return nil
		})
	}

	if err := group.Wait(); err != nil {
		h.log.Warn("scene_images_partial_errors",
			zap.Error(err),
			zap.Int64("elapsed_ms", time.Since(startedAll).Milliseconds()))
	}

	h.log.Info("scene_images_all_done",
		zap.Int("total_scenes", len(scenesText)),
		zap.Int64("total_ms", time.Since(startedAll).Milliseconds()))

	// June 2026 endpoint-compat: always emit intro/outro narration labels on
	// the first/last scene. Shared with /generate-with-images so both
	// endpoints produce the same JSON shape. See flow_scene_intro_outro.go
	// for the policy.
	return markScenesIntroOutro(results)
}
