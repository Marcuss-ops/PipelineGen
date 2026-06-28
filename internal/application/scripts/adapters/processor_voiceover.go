// Package scripts — processor_voiceover.go generates voiceovers for
// each scene. Enabled as "voiceover" in the plan's Postprocessors list.
//
// PR 5 (June 2026): VoiceoverService (interface{} return) REPLACED by
// VoiceoverGenerator — a typed port that takes a
// domain.GenerateVoiceoverCommand and returns *domain.VoiceoverResult.
// extractVoiceoverPaths and map[string]any fallbacks are REMOVED.
// The processor now passes script_id + scene_id via domain.Reference.
// Locale-based filename generation is gone — the server owns naming
// via domain.BuildFilename.
//
// Partial failures are collected — the processor does NOT abort on
// first error. No-op when plan has no scenes.
package adapters

import (
	"context"
	"fmt"

	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// VoiceoverGenerator is the typed port for voiceover generation.
// Production implementations call GenerateVoiceoverUseCase.Execute.
// The port receives the fully-built domain command and returns a
// typed result — no interface{}, no map[string]any, no type assertions.
//
// PR 5 (June 2026): replaces the legacy VoiceoverService (Generate +
// GenerateWithDestination returning interface{}).
type VoiceoverGenerator interface {
	Generate(ctx context.Context, cmd domain.GenerateVoiceoverCommand) (*domain.VoiceoverResult, error)
}

// VoiceoverProcessor generates scene voiceovers via VoiceoverGenerator.
// Uses engineResult.Output.SpecScene.Scenes to drive per-scene
// voiceover generation (PR 9 contract).
type VoiceoverProcessor struct {
	gen VoiceoverGenerator
	log *zap.Logger
}

// NewVoiceoverProcessor creates a VoiceoverProcessor.
// gen must be non-nil (enforced at registration time by wire_script.go).
func NewVoiceoverProcessor(gen VoiceoverGenerator, log *zap.Logger) *VoiceoverProcessor {
	return &VoiceoverProcessor{gen: gen, log: log}
}

func (p *VoiceoverProcessor) Name() string { return "voiceover" }

// Policy classifies voiceover as ProcessorBestEffort: a missing
// voiceover service or a runtime TTS failure degrades into a Warning,
// not a hard failure.
func (p *VoiceoverProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process generates per-scene voiceovers. PR 9 contract: scenes come
// directly from engineResult.Output.SpecScene.Scenes.
//
// PR 5 (June 2026): each call builds a domain.GenerateVoiceoverCommand
// with Reference{ScriptID, SceneID} and delegates to the typed
// VoiceoverGenerator port. No more interface{}, no map[string]any,
// no extractVoiceoverPaths. The server owns the filename via
// domain.BuildFilename.
func (p *VoiceoverProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: voiceover processor: VoiceoverGenerator not configured", scriptpkg.ErrPostprocessFailed)
	}

	scenes := specScenesFromInput(input)
	if len(scenes) == 0 {
		if p.log != nil {
			p.log.Debug("voiceover processor: no scenes to render",
				zap.String("item_id", plan.ID))
		}
		return &PostProcessResult{}, nil
	}

	if input.Text == "" {
		return &PostProcessResult{}, nil
	}

	language := plan.Language
	if language == "" {
		language = "en"
	}

	voiceovers := make([]SceneVoiceover, 0, len(scenes))
	var warnings []string

	for i, scene := range scenes {
		sceneText := scene.Text
		if sceneText == "" {
			sceneText = fmt.Sprintf("Scene %d", i+1)
		}

		// PR 5: build the typed domain command with Reference.
		// The server owns the filename via domain.BuildFilename.
		// Destination is passed through from the plan.
		cmd := domain.GenerateVoiceoverCommand{
			Text:            sceneText,
			Locale:          domain.Locale(language),
			ForceRegenerate: true,
			Reference: domain.Reference{
				ScriptID: plan.ID,
				SceneID:  scene.ID,
			},
		}
		if plan.VoiceoverFolderID != "" {
			cmd.Destination = domain.DestinationRef{
				FolderID: plan.VoiceoverFolderID,
			}
		} else if plan.VoiceoverGroup != "" && p.log != nil {
			p.log.Warn("voiceover processor: voiceover_group set but not resolved to folder_id",
				zap.String("voiceover_group", plan.VoiceoverGroup))
		}

		result, voErr := p.gen.Generate(ctx, cmd)
		if voErr != nil {
			warnings = append(warnings, fmt.Sprintf("voiceover failed for scene %d: %v", i, voErr))
			voiceovers = append(voiceovers, SceneVoiceover{SceneIndex: i, Status: "failed"})
			continue
		}

		// PR 5: typed result — use fields directly, no type assertion.
		status := "completed"
		if result.DriveLink == "" && result.LocalPath == "" {
			status = "empty_result"
		}

		voiceovers = append(voiceovers, SceneVoiceover{
			SceneIndex: i,
			Status:     status,
			Link:       result.DriveLink,
			LocalPath:  result.LocalPath,
		})
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("voiceover processor: partial failures",
			zap.Int("total", len(scenes)),
			zap.Int("failed", len(warnings)),
			zap.Int("succeeded", len(voiceovers)-len(warnings)),
			zap.Strings("warnings", warnings))
	}

	// fix/voiceover-propagate-warnings (June 2026): propagate per-scene
	// failures to PostProcessResult.Warnings.
	return &PostProcessResult{
		Voiceovers: voiceovers,
		Warnings:   warnings,
	}, nil
}
