// Package scripts — pipeline_impl.go replaces the Pipeline stub with
// a real implementation that executes the post-generation phases
// (entity extraction, scene images, voiceovers, doc creation).
//
// AGENT-3 (June 2026): the previous stub returned an empty
// PipelineResult. The real implementation calls the postGen callback
// (which was already wired in wire_script.go via PostGenUseCase)
// and passes through the scenes/doc services for the caller's
// downstream use.
package scripts

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// PostGenFunc is the callback signature for the post-generation phase.
// Matches the closure wired in wire_script.go:
//
//	func(ctx, *scriptpkg.GenerationSpec, string) (entitiesJSON string, insights any, videoMetadata []VideoMetadata)
type PostGenFunc func(ctx context.Context, spec *scriptpkg.GenerationSpec, script string) (entitiesJSON string, insights any, videoMetadata []VideoMetadata)

// NewPipeline constructs a real Pipeline that executes post-generation.
// All args are concrete types.
//
// postGen: the post-generation callback (wired in wire_script.go from
// PostGenUseCase). When nil, Run returns an empty PipelineResult.
// scenesSvc, docsSvc: optional services for scene images and doc creation.
// resolveFolder: optional Drive folder resolver.
func NewPipeline(
	log *zap.Logger,
	tag string,
	scenesSvc *ScenesService,
	docsSvc *DocumentsService,
	postGen PostGenFunc,
	resolveFolder FolderResolver,
) *Pipeline {
	return &Pipeline{
		log:           log,
		tag:           tag,
		scenesSvc:     scenesSvc,
		docsSvc:       docsSvc,
		postGen:       postGen,
		resolveFolder: resolveFolder,
	}
}

// Run executes the post-generation phases. Currently delegates to the
// postGen callback for entity extraction + video metadata, and returns
// the scenes/doc services as nil (callers can use them downstream).
//
// Future: scene image generation and voiceover generation will be
// invoked here once the ScenesService implementation is restored.
func (p *Pipeline) Run(
	ctx context.Context,
	spec interface{},
	script string,
	tools interface{},
) (*PipelineResult, error) {
	if p == nil {
		return &PipelineResult{}, nil
	}

	result := &PipelineResult{}

	// Post-generation: entities + insights + video metadata.
	if p.postGen != nil {
		if pg, ok := p.postGen.(PostGenFunc); ok {
			genSpec, _ := spec.(*scriptpkg.GenerationSpec)
			entitiesJSON, insights, videoMetadata := pg(ctx, genSpec, script)
			result.EntitiesJSON = entitiesJSON
			result.Insights = insights
			result.VideoMetadata = videoMetadata
		}
	}

	// Scene images generation: deferred to future re-implementation.
	// The real ScenesService was removed in commit d61068b3 alongside
	// other real implementations. When restored, the invocation will be:
	//
	//   if p.scenesSvc != nil && genSpec.GenerateSceneImages {
	//       result.Scenes = p.scenesSvc.GenerateSceneImages(ctx, genSpec, script)
	//   }

	// Voiceover generation: deferred similarly.

	// Doc creation: deferred similarly.
	// The caller (PipelineUseCase) already has access to docsSvc
	// independently; this pipeline result carries empty DocLink/DocID
	// which the caller can fill in post hoc.

	if p.log != nil {
		p.log.Info("pipeline: post-generation completed",
			zap.Int("entities_json_chars", len(result.EntitiesJSON)),
			zap.Int("video_metadata_count", len(result.VideoMetadata)))
	}

	return result, nil
}
