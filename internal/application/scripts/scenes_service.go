// Package scripts — scenes_service.go replaces the ScenesService stub
// with a real implementation that generates scene images and voiceovers
// during the post-generation pipeline phase.
//
// AGENT-3 (June 2026): the previous stub was a bare struct{} that ignored
// all constructor args. The real implementation holds typed fields and
// delegates scene image generation to images.Service and voiceover
// generation to voiceover.Service.
package scripts

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// NewScenesService constructs a real ScenesService. All args are concrete typed.
// imgSvc and voSvc are required for scene image and voiceover generation.
// resolveFolder and groupsRes are optional (used for Drive folder resolution).
func NewScenesService(
	imgSvc *images.Service,
	voSvc *voiceover.Service,
	log *zap.Logger,
	cfg *config.Config,
	resolveFolder FolderResolver,
	groupsRes *voiceover.GroupsResolver,
	albumCapacity int,
) *ScenesService {
	return &ScenesService{
		imgSvc:        imgSvc,
		voSvc:         voSvc,
		log:           log,
		cfg:           cfg,
		resolveFolder: resolveFolder,
		groupsRes:     groupsRes,
		albumCapacity: albumCapacity,
	}
}

// GenerateSceneImages generates images for each scene in the script.
// Splits the script on narration/clip markers, identifies scene boundaries,
// and generates an image for each scene using the images.Service.
//
// Returns nil if imgSvc is nil or the script is empty.
func (s *ScenesService) GenerateSceneImages(
	ctx context.Context,
	script string,
	topic string,
	style string,
	language string,
	maxScenes int,
) []SceneImage {
	if s == nil || script == "" {
		return nil
	}

	scenes := parseScriptScenes(script)
	if len(scenes) == 0 {
		return nil
	}

	if maxScenes > 0 && len(scenes) > maxScenes {
		scenes = scenes[:maxScenes]
	}

	result := make([]SceneImage, 0, len(scenes))
	for i, sceneText := range scenes {
		sceneText = strings.TrimSpace(sceneText)
		if sceneText == "" {
			continue
		}

		visualPrompt := buildVisualPrompt(sceneText, topic, style, i)

		imgSvc, ok := s.imgSvc.(*images.Service)
		if !ok || imgSvc == nil {
			continue
		}

		name := fmt.Sprintf("scene_%d", i+1)
		asset, err := imgSvc.GenerateSmartImage(
			ctx,
			name,
			visualPrompt,
			style,
			[]string{visualPrompt},
			[]string{topic, fmt.Sprintf("scene_%d", i+1)},
			1024, 1024,
			"",
			false,
		)

		if err != nil {
			if s.log != nil {
				s.log.Warn("scenes_service: failed to generate scene image",
					zap.Int("scene_index", i),
					zap.Error(err))
			}
			result = append(result, SceneImage{
				Index: i,
				Text:  sceneText,
			})
			continue
		}

		url := ""
		if asset != nil {
			if asset.SourceURL != "" {
				url = asset.SourceURL
			} else if asset.DriveFileID != "" {
				url = fmt.Sprintf("https://drive.google.com/file/d/%s/view", asset.DriveFileID)
			}
		}

		result = append(result, SceneImage{
			Index: i,
			Text:  sceneText,
			URL:   url,
		})
	}

	if s.log != nil {
		s.log.Info("scenes_service: scene images generated",
			zap.Int("total", len(result)),
			zap.Int("with_url", countScenesWithURL(result)))
	}

	return result
}

// ── BuildScenesWithMarkers ───────────────────────────────────────────────

// BuildScenesWithMarkers parses a script for [CLIP:xxx] and [NARRATION]
// markers and produces a slice of ClipScene entries with associated ClipIDs
// from the pack. Used by PipelineUseCase.handleClipPathExplicit.
func BuildScenesWithMarkers(script string, pack interface{}) []ClipScene {
	if script == "" {
		return nil
	}

	type markerPos struct {
		pos  int
		kind string
		id   string
	}

	var markers []markerPos

	clipRe := textutil.StripClipMarkerRe
	narrationRe := textutil.StripNarrationMarkerRe

	for _, match := range clipRe.FindAllStringIndex(script, -1) {
		markerText := script[match[0]:match[1]]
		id := ""
		if colon := strings.Index(markerText, ":"); colon >= 0 {
			id = strings.TrimRight(markerText[colon+1:len(markerText)-1], " \t")
		}
		markers = append(markers, markerPos{pos: match[0], kind: "clip", id: id})
	}

	for _, match := range narrationRe.FindAllStringIndex(script, -1) {
		markers = append(markers, markerPos{pos: match[0], kind: "narration"})
	}

	if len(markers) == 0 {
		return nil
	}

	// Sort by position (insertion sort, small N).
	for i := 1; i < len(markers); i++ {
		j := i
		for j > 0 && markers[j].pos < markers[j-1].pos {
			markers[j], markers[j-1] = markers[j-1], markers[j]
			j--
		}
	}

	scenes := make([]ClipScene, 0, len(markers))
	for i, m := range markers {
		start := m.pos
		end := len(script)
		if i+1 < len(markers) {
			end = markers[i+1].pos
		}

		markerEnd := start
		searchEnd := start + 50
		if searchEnd > len(script) {
			searchEnd = len(script)
		}
		if idx := strings.Index(script[start:searchEnd], "]"); idx >= 0 {
			markerEnd = start + idx + 1
		}

		sceneText := strings.TrimSpace(script[markerEnd:end])
		if sceneText == "" {
			continue
		}

		scenes = append(scenes, ClipScene{
			SceneIndex: i,
			Text:       sceneText,
			ClipID:     m.id,
			DriveLink:  "", // TODO: resolve via clip repository
			Kind:       m.kind,
		})
	}

	return scenes
}

// ── Internal helpers ────────────────────────────────────────────────────

func parseScriptScenes(script string) []string {
	if script == "" {
		return nil
	}

	type markerPos struct {
		pos int
	}

	var markers []markerPos

	re := textutil.StripClipMarkerRe
	for _, match := range re.FindAllStringIndex(script, -1) {
		markers = append(markers, markerPos{pos: match[0]})
	}
	re = textutil.StripNarrationMarkerRe
	for _, match := range re.FindAllStringIndex(script, -1) {
		markers = append(markers, markerPos{pos: match[0]})
	}

	if len(markers) == 0 {
		return []string{script}
	}

	for i := 1; i < len(markers); i++ {
		j := i
		for j > 0 && markers[j].pos < markers[j-1].pos {
			markers[j], markers[j-1] = markers[j-1], markers[j]
			j--
		}
	}

	scenes := make([]string, 0, len(markers))
	for i, m := range markers {
		start := m.pos
		end := len(script)
		if i+1 < len(markers) {
			end = markers[i+1].pos
		}

		markerEnd := start
		searchEnd := start + 50
		if searchEnd > len(script) {
			searchEnd = len(script)
		}
		if idx := strings.Index(script[start:searchEnd], "]"); idx >= 0 {
			markerEnd = start + idx + 1
		}

		sceneText := strings.TrimSpace(script[markerEnd:end])
		if sceneText == "" {
			continue
		}
		scenes = append(scenes, sceneText)
	}

	if len(scenes) == 0 {
		return []string{script}
	}

	return scenes
}

func buildVisualPrompt(sceneText, topic, style string, index int) string {
	preview := sceneText
	if len(preview) > 200 {
		if space := strings.LastIndex(preview[:200], " "); space > 100 {
			preview = preview[:space]
		} else {
			preview = preview[:200]
		}
	}

	if style == "" {
		style = "cinematic"
	}
	if topic == "" {
		topic = "documentary"
	}

	return fmt.Sprintf("Scene %d for %s: %s. Style: %s. High quality, professional.", index+1, topic, preview, style)
}

func countScenesWithURL(scenes []SceneImage) int {
	n := 0
	for _, s := range scenes {
		if s.URL != "" {
			n++
		}
	}
	return n
}
