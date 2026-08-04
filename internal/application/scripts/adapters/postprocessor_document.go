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

func (p *DocumentsProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	if p == nil || p.service == nil {
		return nil, fmt.Errorf("document publisher is not configured")
	}
	if plan == nil || !plan.DocsEnabled {
		return &PostProcessResult{Changed: true}, nil
	}

	languages := append([]string(nil), plan.DocsLanguages...)
	if len(languages) == 0 && strings.TrimSpace(plan.Language) != "" {
		languages = []string{plan.Language}
	}
	if len(languages) == 0 {
		return nil, fmt.Errorf("document publishing requires at least one language")
	}

	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          input.Text,
		SpecScene:     input.SpecScene,
		WordCount:     input.WordCount,
		ModelUsed:     input.ModelUsed,
	}
	if plan.SingleScene {
		var bindings scriptpkg.SceneBindings
		if len(model.SpecScene.Scenes) > 0 {
			bindings = model.SpecScene.Scenes[0].Bindings
		}
		model.SpecScene.Scenes = []scriptpkg.SpecScene{{
			ID: "scene-0", Index: 0, Kind: scriptpkg.SceneNarration,
			Text: input.Text, Bindings: bindings,
		}}
	}
	// For a single explicit segment, the persisted model text is the
	// canonical generated narrative. The scene planner may retain only a
	// short preview in SpecScene; publishing that preview would silently
	// truncate the document produced by the endpoint.
	if len(model.SpecScene.Scenes) == 1 && strings.TrimSpace(input.Text) != "" {
		model.SpecScene.Scenes[0].Text = input.Text
	}
	documentTitle := strings.TrimSpace(plan.Title)

	if plan.VideoMetadata != nil &&
		strings.TrimSpace(plan.VideoMetadata.Title) != "" {
		documentTitle = strings.TrimSpace(plan.VideoMetadata.Title)
	}

	content := BuildSpecSceneDocumentHTML(
		model,
		documentTitle,
		plan.VideoMetadata,
		input.Provenance,
	)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("document content is empty")
	}

	var firstID, firstLink string
	// Existing documents are normally reused by the idempotent publisher.
	// Subtitle links are a versioned document surface, however: when ASS
	// artifacts become available, refresh the existing doc so operators do
	// not keep seeing the pre-subtitle HTML.
	refreshDocument := plan.ForceRefresh || specSceneHasSubtitleLinks(input.SpecScene)
	for _, language := range languages {
		language = strings.TrimSpace(language)
		if language == "" {
			continue
		}
		link, id := p.service.CreateDoc(
			ctx,
			documentTitle+"_"+language,
			content,
			nil,
			plan.DocsFolderID,
			plan.ID+"-"+language,
			refreshDocument,
		)
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
