package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// DocumentsProcessor publishes the canonical SpecScene representation to
// Google Docs when the request explicitly enables document output.
type DocumentsProcessor struct {
	service DocumentsService
}

func NewDocumentsProcessor(service DocumentsService) *DocumentsProcessor {
	return &DocumentsProcessor{service: service}
}

func (p *DocumentsProcessor) Name() ProcessorName { return ProcessorDocument }

func (p *DocumentsProcessor) Policy(*scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return DefaultPolicyFor(ProcessorDocument)
}

func (p *DocumentsProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil || !plan.DocsEnabled {
		return &PostProcessResult{Changed: true}, nil
	}
	if p == nil || p.service == nil {
		return nil, fmt.Errorf("document publisher is not configured")
	}

	languages := append([]string(nil), plan.DocsLanguages...)
	if len(languages) == 0 && strings.TrimSpace(plan.Language) != "" {
		languages = []string{plan.Language}
	}
	if len(languages) == 0 {
		return nil, fmt.Errorf("document publishing requires at least one language")
	}

	model := &scriptpkg.ModelScriptOutputV1{SchemaVersion: 1, Text: input.Text, SpecScene: input.SpecScene, WordCount: input.WordCount, ModelUsed: input.ModelUsed}
	if plan.SingleScene {
		var bindings scriptpkg.SceneBindings
		if len(model.SpecScene.Scenes) > 0 {
			bindings = model.SpecScene.Scenes[0].Bindings
		}
		model.SpecScene.Scenes = []scriptpkg.SpecScene{{ID: "scene-0", Index: 0, Kind: scriptpkg.SceneNarration, Text: input.Text, Bindings: bindings}}
	}
	if len(model.SpecScene.Scenes) == 1 && strings.TrimSpace(input.Text) != "" {
		model.SpecScene.Scenes[0].Text = input.Text
	}
	documentTitle := strings.TrimSpace(plan.Title)
	if plan.VideoMetadata != nil && strings.TrimSpace(plan.VideoMetadata.Title) != "" {
		documentTitle = strings.TrimSpace(plan.VideoMetadata.Title)
	}
	content := BuildSpecSceneDocumentHTML(model, documentTitle, plan.VideoMetadata, input.Provenance)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("document content is empty")
	}

	var firstID, firstLink string
	refreshDocument := plan.ForceRefresh || specSceneHasSubtitleLinks(input.SpecScene)
	for _, language := range languages {
		language = strings.TrimSpace(language)
		if language == "" {
			continue
		}
		link, id, createErr := p.service.CreateDoc(ctx, documentTitle+"_"+language, content, nil, plan.DocsFolderID, plan.ID+"-"+language, refreshDocument)
		if createErr != nil {
			return nil, fmt.Errorf("publish document for language %s: %w", language, createErr)
		}
		if strings.TrimSpace(link) == "" || strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("document publisher returned an empty reference for language %s", language)
		}
		if firstID == "" {
			firstID, firstLink = id, link
		}
	}
	if firstID == "" {
		return nil, fmt.Errorf("document publishing languages are empty")
	}
	return &PostProcessResult{DocID: firstID, DocLink: firstLink}, nil
}

func specSceneHasSubtitleLinks(spec scriptpkg.SpecSceneOutput) bool {
	for _, scene := range spec.Scenes {
		if scene.Bindings.Clip != nil && strings.TrimSpace(scene.Bindings.Clip.SubtitleLink) != "" {
			return true
		}
	}
	return false
}
