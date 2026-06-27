// Package scripts — processor_voiceover.go generates voiceovers for
// each scene. Enabled as "voiceover" in the plan's Postprocessors list.
//
// PR 3 (June 2026): the processor now walks model.SpecScene.Scenes by
// reference and writes back into scene.Bindings.Voiceover directly.
// The pre-PR-3 splitScriptIntoSegments + sceneCountFromPlan helpers
// are gone: the model is the single source of truth for scene count
// and scene narration text.
//
// Partial failures are collected — the processor does NOT abort on
// first error.
package scripts

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// VoiceoverProcessor generates scene voiceovers via VoiceoverService.
// Walks model.SpecScene.Scenes by reference and mutates
// scene.Bindings.Voiceover.
type VoiceoverProcessor struct {
	gen VoiceoverService
	log *zap.Logger
}

// NewVoiceoverProcessor creates a VoiceoverProcessor.
func NewVoiceoverProcessor(gen VoiceoverService, log *zap.Logger) *VoiceoverProcessor {
	return &VoiceoverProcessor{gen: gen, log: log}
}

func (p *VoiceoverProcessor) Name() string { return "voiceover" }

// Process walks model.SpecScene.Scenes by index. For each scene it
// generates a voiceover via VoiceoverService.Generate using
// scene.Text as narration, and stamps scene.Bindings.Voiceover
// with the result.
//
// Returns an empty *PostProcessArtifact — voiceover generation is a
// side effect on model.SpecScene.Scenes; the aggregate's other fields
// are not touched by this processor.
func (p *VoiceoverProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	model *scriptpkg.ModelScriptOutputV1,
	_ *PostProcessArtifact,
) (*PostProcessArtifact, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: voiceover processor: VoiceoverService not configured", scriptpkg.ErrPostprocessFailed)
	}
	if model == nil || plan == nil {
		return &PostProcessArtifact{}, nil
	}
	scenes := model.SpecScene.Scenes
	if len(scenes) == 0 {
		if p.log != nil {
			p.log.Debug("voiceover processor: no scenes (empty specscene)",
				zap.String("item_id", plan.ID))
		}
		return &PostProcessArtifact{}, nil
	}

	language := plan.Language
	if language == "" {
		language = "en"
	}

	var warnings []string
	succeeded := 0

	for i := range scenes {
		scene := &scenes[i]
		sceneText := strings.TrimSpace(scene.Text)
		if sceneText == "" {
			sceneText = fmt.Sprintf("Scene %d", i+1)
		}

		filename := sanitizeVoiceoverFilename(plan.Title, i+1)

		status := "failed"
		var link, localPath string
		result, err := p.gen.Generate(ctx, sceneText, language, filename)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("voiceover failed for scene %d: %v", i, err))
		} else {
			link, localPath = extractVoiceoverPaths(result)
			if link != "" || localPath != "" {
				status = "completed"
				succeeded++
			} else {
				status = "empty_result"
			}
		}

		// Stamp the binding onto the scene by reference.
		if scene.Bindings.Voiceover == nil {
			scene.Bindings.Voiceover = &scriptpkg.VoiceoverBinding{}
		}
		scene.Bindings.Voiceover.Status = status
		scene.Bindings.Voiceover.Link = link
		scene.Bindings.Voiceover.LocalPath = localPath
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("voiceover processor: partial failures",
			zap.Int("total", len(scenes)),
			zap.Int("failed", len(warnings)),
			zap.Int("succeeded", succeeded),
			zap.Strings("warnings", warnings))
	}

	return &PostProcessArtifact{}, nil
}

// extractVoiceoverPaths extracts DriveLink and Path from a voiceover
// result. Handles both *voiceover.VoiceoverResult (production concrete)
// and map[string]any (test fakes). The VoiceoverService interface
// returns interface{}, so we type-assert to discover the concrete
// shape.
func extractVoiceoverPaths(result interface{}) (link, path string) {
	if result == nil {
		return "", ""
	}
	if vo, ok := result.(*voiceover.VoiceoverResult); ok {
		return vo.DriveLink, vo.Path
	}
	if m, ok := result.(map[string]any); ok {
		l, _ := m["drive_link"].(string)
		p, _ := m["path"].(string)
		return l, p
	}
	return "", ""
}

// sanitizeVoiceoverFilename replaces characters unsafe in filenames
// and lowercases. Mirrors the pre-PR-3 helper so filenames match the
// Drive upload format operators expect.
func sanitizeVoiceoverFilename(name string, index int) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("scene_%d", index)
	}
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", " ", "_",
	)
	base := strings.ToLower(replacer.Replace(name))
	return fmt.Sprintf("%s_scene_%d", base, index)
}
