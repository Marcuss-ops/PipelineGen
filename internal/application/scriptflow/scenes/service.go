// Package scenes provides scene-level image and voiceover generation for
// the script pipeline. It lives in the application layer so the HTTP
// transport (internal/api/) remains thin.
package scenes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Local interfaces (application-layer, no import from internal/api/) ──────

// ImageService captures the image generation method the scenes package needs.
type ImageService interface {
	GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*media.ImageAsset, error)
}

// VoiceoverService captures the voiceover generation method the scenes package needs.
type VoiceoverService interface {
	GenerateWithDestination(ctx context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error)
}

// FolderResolver resolves a folder name or path to a Drive folder ID.
type FolderResolver func(ctx context.Context, input, defaultRootID string) (string, error)

// ── Service ─────────────────────────────────────────────────────────────────

// Service generates scene images and voiceovers for script pipelines.
type Service struct {
	imgSvc        ImageService
	voSvc         VoiceoverService
	log           *zap.Logger
	cfg           *config.Config
	resolveFolder FolderResolver
	groupsRes     *voiceover.GroupsResolver
	parallelism   int
}

// NewService creates a new scenes Service.
// parallelism controls concurrent scene image generation (1..4, default 2).
// Pass 0 or negative to default to the VELOX_SCENE_PARALLELISM env var
// (fallback 2).
func NewService(
	imgSvc ImageService,
	voSvc VoiceoverService,
	log *zap.Logger,
	cfg *config.Config,
	resolveFolder FolderResolver,
	groupsRes *voiceover.GroupsResolver,
	parallelism int,
) *Service {
	if parallelism <= 0 {
		parallelism = loadSceneImageParallelism(os.Getenv("VELOX_SCENE_PARALLELISM"))
	}
	return &Service{
		imgSvc:        imgSvc,
		voSvc:         voSvc,
		log:           log,
		cfg:           cfg,
		resolveFolder: resolveFolder,
		groupsRes:     groupsRes,
		parallelism:   parallelism,
	}
}

// ── Scene image generation ──────────────────────────────────────────────────

// SceneImage represents a scene text and its corresponding AI generated image(s).
type SceneImage struct {
	Text          string   `json:"text"`
	Image         string   `json:"image,omitempty"`
	Images        []string `json:"images,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	NarrationRole string   `json:"narration_role,omitempty"`
}

// SceneVoiceover holds the voiceover result for a single scene.
type SceneVoiceover struct {
	SceneIndex int    `json:"scene_index"`
	Link       string `json:"link"`
	LocalPath  string `json:"local_path,omitempty"`
	Status     string `json:"status"`
}

// ProgressReporter abstracts job progress reporting.
type ProgressReporter func(pct int, msg string)

// GenerateImages splits the script into scenes and generates AI images.
func (s *Service) GenerateImages(
	ctx context.Context,
	spec *script.GenerationSpec,
	script string,
	reportProgress ProgressReporter,
) []SceneImage {
	if s.imgSvc == nil || strings.TrimSpace(script) == "" {
		return nil
	}

	sentencesPerImage := spec.SentencesPerImage
	if sentencesPerImage <= 0 {
		sentencesPerImage = 10
	}

	scenesText := splitScriptIntoSceneImages(script, sentencesPerImage)
	if len(scenesText) == 0 {
		return nil
	}

	style := spec.Style
	if style == "" {
		style = "realistic"
	}

	imagesPerScene := spec.ImagesPerScene
	if imagesPerScene <= 0 {
		imagesPerScene = 1
	}

	s.log.Info("scene_images_spawn",
		zap.Int("total_scenes", len(scenesText)),
		zap.Int("images_per_scene", imagesPerScene),
		zap.Int("parallelism", s.parallelism),
		zap.String("style", style),
		zap.Int("sentences_per_image", sentencesPerImage),
		zap.Int("script_chars", len(script)))

	sem := make(chan struct{}, s.parallelism)
	var mu sync.Mutex
	results := make([]SceneImage, len(scenesText))

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

			s.log.Info("scene_image_started",
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
				aiAsset, err := s.imgSvc.GenerateSmartImage(aiCtx, shortPrompt, para, style, []string{promptWithVar}, []string{"scene", fmt.Sprintf("scene-%d", idx)}, 1024, 1024, "", false)
				aiCancel()

				if err == nil && aiAsset != nil {
					fileID := strings.TrimSpace(aiAsset.DriveFileID)
					if fileID != "" {
						link := drive.FileURLFromID(fileID)
						imageLinks = append(imageLinks, link)
						s.log.Info("scene_image_variation_completed",
							zap.Int("scene_idx", idx), zap.Int("var_idx", i),
							zap.String("drive_link", link))
					}
				} else {
					failedVariations++
					s.log.Warn("scene_image_variation_failed",
						zap.Int("scene_idx", idx), zap.Int("var_idx", i), zap.Error(err))
				}
			}

			var mainImage string
			if len(imageLinks) > 0 {
				mainImage = imageLinks[0]
			}

			mu.Lock()
			results[idx] = SceneImage{
				Text:   para,
				Image:  mainImage,
				Images: imageLinks,
			}
			mu.Unlock()

			s.log.Info("scene_image_completed",
				zap.Int("scene_idx", idx),
				zap.Int("variations_ok", len(imageLinks)),
				zap.Int("variations_failed", failedVariations),
				zap.Int64("duration_ms", time.Since(sceneStart).Milliseconds()))

			done := int(atomic.AddInt32(&sceneCompleted, 1))
			if reportProgress != nil && totalScenes > 0 {
				pct := 70 + (10 * done / totalScenes)
				reportProgress(pct, fmt.Sprintf("Scene %d/%d finished (%d/%d ok)",
					done, totalScenes, len(imageLinks), imagesPerScene))
			}

			return nil
		})
	}

	if err := group.Wait(); err != nil {
		s.log.Warn("scene_images_partial_errors",
			zap.Error(err),
			zap.Int64("elapsed_ms", time.Since(startedAll).Milliseconds()))
	}

	s.log.Info("scene_images_all_done",
		zap.Int("total_scenes", len(scenesText)),
		zap.Int64("total_ms", time.Since(startedAll).Milliseconds()))

	return markScenesIntroOutro(results)
}

// ── Scene voiceover generation ──────────────────────────────────────────────

// GenerateVoiceovers generates voiceovers for each scene in parallel.
func (s *Service) GenerateVoiceovers(
	ctx context.Context,
	spec *script.GenerationSpec,
	scenes []SceneImage,
) []SceneVoiceover {
	if s.voSvc == nil || !spec.GenerateVoiceover || len(scenes) == 0 {
		return nil
	}

	subfolderName := textutil.SlugifyWithMax(spec.Title, 40)
	if subfolderName == "" {
		subfolderName = "scenes"
	}

	voRootID := spec.VoiceoverFolderID
	if voRootID == "" {
		voRootID = spec.DriveFolderID
	}
	if voRootID == "" && s.cfg != nil {
		voRootID = s.cfg.Drive.VoiceoverFolder()
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

			voFilename := fmt.Sprintf("scene-%d_%s.mp3", idx, spec.Language)
			voDest := s.resolveSceneVoiceoverDestination(ctx, spec.VoiceoverFolderID, spec.VoiceoverGroup, voRootID, subfolderName)

			voCtx, voCancel := context.WithTimeout(groupCtx, 90*time.Second)
			defer voCancel()

			voResult, err := s.voSvc.GenerateWithDestination(voCtx, cleanedText, spec.Language, voFilename, voDest)
			if err != nil {
				s.log.Warn("failed to generate voiceover for scene",
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
func (s *Service) resolveSceneVoiceoverDestination(
	ctx context.Context,
	voiceoverFolderID, voiceoverGroup, voRootID, subfolderName string,
) *voiceover.DestinationRequest {
	dest := buildVoiceoverDestination(
		ctx,
		s.resolveFolder,
		s.log,
		subfolderName,
		voiceoverFolderID,
		voiceoverGroup,
		voRootID,
		s.groupsRes,
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

// ── Helpers (moved from internal/api/script/) ────────────────────────────────

// loadSceneImageParallelism reads VELOX_SCENE_PARALLELISM (1..4, default 2).
func loadSceneImageParallelism(raw string) int {
	const (
		defaultVal = 2
		minVal     = 1
		maxVal     = 4
	)
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

// splitScriptIntoSceneImages splits a script into scenes of N sentences each.
func splitScriptIntoSceneImages(script string, sentencesPerImage int) []string {
	if sentencesPerImage <= 0 {
		sentencesPerImage = 10
	}
	sentences := splitIntoSentences(script)
	if len(sentences) == 0 {
		return nil
	}
	var scenes []string
	var current []string
	for _, s := range sentences {
		current = append(current, s)
		if len(current) == sentencesPerImage {
			scenes = append(scenes, strings.Join(current, " "))
			current = nil
		}
	}
	if len(current) > 0 {
		scenes = append(scenes, strings.Join(current, " "))
	}
	return scenes
}

// splitIntoSentences splits text into sentences based on punctuation.
func splitIntoSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		current.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			if i+1 == len(runes) || runes[i+1] == ' ' {
				s := strings.TrimSpace(current.String())
				if s != "" {
					sentences = append(sentences, s)
				}
				current.Reset()
			}
		}
	}
	s := strings.TrimSpace(current.String())
	if s != "" {
		sentences = append(sentences, s)
	}
	return sentences
}

// truncatePrompt extracts a short visual description from scene text.
// Takes the first 1-2 sentences and caps at maxLen characters.
func truncatePrompt(text string, maxLen int) string {
	sentences := splitIntoSentences(text)
	if len(sentences) == 0 {
		if len(text) > maxLen {
			return text[:maxLen]
		}
		return text
	}
	prompt := sentences[0]
	if len(sentences) > 1 && len(prompt)+len(sentences[1])+1 <= maxLen {
		prompt += " " + sentences[1]
	}
	if len(prompt) > maxLen {
		prompt = prompt[:maxLen]
	}
	return prompt
}

// markScenesIntroOutro labels the first scene as narration/introduction and the
// last as narration/outroduction. Middle scenes are untouched.
func markScenesIntroOutro(scenes []SceneImage) []SceneImage {
	if len(scenes) == 0 {
		return scenes
	}

	if scenes[0].Kind == "" {
		scenes[0].Kind = "narration"
	}
	if scenes[0].NarrationRole == "" {
		scenes[0].NarrationRole = "intro"
	}

	if len(scenes) > 1 {
		last := len(scenes) - 1
		if scenes[last].Kind == "" {
			scenes[last].Kind = "narration"
		}
		if scenes[last].NarrationRole == "" {
			scenes[last].NarrationRole = "outro"
		}
	}

	return scenes
}

// buildVoiceoverDestination builds a *voiceover.DestinationRequest.
// (Moved from internal/api/script/handler_script_handlers_flow_shared_helpers.go)
func buildVoiceoverDestination(
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

// ensure errors import is used by buildVoiceoverDestination.
var _ = errors.Is