package adapters

import (
	"context"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// NarrationSanitizer freezes the speakable scene surface before annotations
// and voiceover. The operation is idempotent so translated and synthesized
// scene surfaces can safely pass through it more than once.
type NarrationSanitizer struct{}

func NewNarrationSanitizer() *NarrationSanitizer { return &NarrationSanitizer{} }

func (p *NarrationSanitizer) Name() ProcessorName { return ProcessorNarrationSanitizer }

func (p *NarrationSanitizer) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorRequired
}

func (p *NarrationSanitizer) Process(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	clean := strings.TrimSpace(input.Text)
	if clean != "" {
		var err error
		clean, _, err = scriptpkg.SanitizeNarration(clean)
		if err != nil {
			return nil, err
		}
	}
	input.Text = clean
	if err := sanitizeSceneOutput(&input.SpecScene); err != nil {
		return nil, err
	}
	if len(input.TranslatedSpecScene.Scenes) > 0 {
		if err := sanitizeSceneOutput(&input.TranslatedSpecScene); err != nil {
			return nil, err
		}
	}
	if len(input.OriginalSpecScene.Scenes) > 0 {
		if err := sanitizeSceneOutput(&input.OriginalSpecScene); err != nil {
			return nil, err
		}
	}
	applyResearchSources(&input.SpecScene, input.ResearchSources)
	applyResearchSources(&input.TranslatedSpecScene, input.ResearchSources)
	applyResearchSources(&input.OriginalSpecScene, input.ResearchSources)
	result := &PostProcessResult{
		Changed:          true,
		UpdatedSpecScene: input.SpecScene,
		TranslatedText:   clean,
	}
	if len(input.TranslatedSpecScene.Scenes) > 0 {
		result.TranslatedSpecScene = input.TranslatedSpecScene
	}
	if len(input.OriginalSpecScene.Scenes) > 0 {
		result.OriginalText = clean
		result.OriginalSpecScene = input.OriginalSpecScene
	}
	return result, nil
}

func applyResearchSources(output *scriptpkg.SpecSceneOutput, sources []scriptpkg.SourceReference) {
	if len(sources) == 0 {
		return
	}
	for i := range output.Scenes {
		if output.Scenes[i].Metadata == nil {
			output.Scenes[i].Metadata = &scriptpkg.SceneMetadata{}
		}
		for _, source := range sources {
			found := false
			for _, existing := range output.Scenes[i].Metadata.Sources {
				if existing.URL == source.URL {
					found = true
					break
				}
			}
			if !found {
				output.Scenes[i].Metadata.Sources = append(output.Scenes[i].Metadata.Sources, source)
			}
		}
	}
}

func sanitizeSceneOutput(output *scriptpkg.SpecSceneOutput) error {
	for i := range output.Scenes {
		scene := &output.Scenes[i]
		if strings.TrimSpace(scene.Text) == "" {
			continue
		}
		clean, extracted, err := scriptpkg.SanitizeNarration(scene.Text)
		if err != nil {
			return err
		}
		scene.Text = clean
		if len(extracted) > 0 {
			if scene.Metadata == nil {
				scene.Metadata = &scriptpkg.SceneMetadata{}
			}
			for _, ref := range extracted {
				found := false
				for _, existing := range scene.Metadata.Sources {
					if strings.EqualFold(existing.URL, ref.URL) && existing.Title == ref.Title {
						found = true
						break
					}
				}
				if !found {
					scene.Metadata.Sources = append(scene.Metadata.Sources, ref)
				}
			}
		}
	}
	return nil
}
