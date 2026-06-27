// Package scripts — pipeline_impl.go replaces the Pipeline stub with
// a real implementation that executes the post-generation phases
// (entity extraction, scene images, voiceovers, doc creation).
//
// AGENT-3 (June 2026): the previous stub returned an empty
// PipelineResult. The real implementation calls the postGen callback
// (which was already wired in wire_script.go via PostGenUseCase)
// and passes through the scenes/doc services for the caller's
// downstream use.
//
// PG-029 (June 2026): Pipeline types + VideoMetadata + FolderResolver
// consolidated here from the now-deleted types.go.
package scripts

import (
	"context"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Pipeline types ───────────────────────────────────────────────────────

// Pipeline executes the post-generation phases (entity extraction,
// scene images, voiceovers, doc creation). All fields are concrete typed.
type Pipeline struct {
	log           *zap.Logger
	tag           string
	scenesSvc     *ScenesService
	docsSvc       *DocumentsService
	postGen       interface{} // PostGenFunc callback
	resolveFolder FolderResolver
}

// FolderResolver resolves a folder ID from an input name and default root.
type FolderResolver func(ctx context.Context, input, defaultRootID string) (string, error)

// PipelineResult holds the output of Pipeline.Run.
type PipelineResult struct {
	EntitiesJSON  string
	Insights      interface{}
	VideoMetadata []VideoMetadata
	DocLink       string
	DocID         string
	Scenes        []SceneImage
	Voiceovers    []SceneVoiceover
	ScriptID      int64

	// AlreadyPersisted is true when PersistenceProcessor found an
	// existing script row by idempotency key and skipped the
	// insert. Consumers downstream can use this flag to log a
	// "replay" outcome without re-marking the script as newly
	// produced. PR 5 default value is false.
	AlreadyPersisted bool

	// PR 2 (June 2026): Warnings carries non-fatal per-processor
	// observations aggregated across the run. Includes:
	//   - per-processor PostProcessResult.Warnings with explicit
	//     messages ("alt text missing for scene 0");
	//   - best-effort processor failures (TTS down, image service
	//     partial errors, entity extraction returned empty);
	//   - missing-registered (best-effort) processor names that
	//     the plan requested but composition never wired
	//     (e.g. voiceover service not configured).
	// Required-class failures do NOT appear here — they bubble up
	// via Run's *scriptpkg.PostprocessError path instead.
	// Propagated to GenerationResult.Warnings by
	// buildGenerationResult so HTTP / JobSystem responses
	// surface the warnings slice.
	Warnings []string `json:"warnings,omitempty"`
}

// SceneImage represents a scene with an image.
type SceneImage struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
	URL   string `json:"url"`
}

// SceneVoiceover represents a scene with a voiceover.
type SceneVoiceover struct {
	SceneIndex int    `json:"scene_index"`
	Status     string `json:"status"`
	Link       string `json:"link"`
	LocalPath  string `json:"local_path"`
}

// VideoMetadata holds YouTube metadata for a single language.
type VideoMetadata struct {
	Language    string   `json:"language"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

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

	// Doc creation: always create a Google Doc for the script.
	if p.docsSvc != nil {
		genSpec, _ := spec.(*scriptpkg.GenerationSpec)
		if genSpec != nil {
			docTitle := strings.TrimSpace(genSpec.Title)
			if docTitle == "" {
				docTitle = "Script"
			}
			htmlContent := BuildSectionDocHTML(docTitle, []string{""}, []string{script}, true, genSpec.Language)
			link, id := p.docsSvc.CreateDoc(ctx, docTitle, htmlContent, p.resolveFolder, genSpec.DriveFolderID)
			result.DocLink = link
			result.DocID = id
		}
	}

	if p.log != nil {
		p.log.Info("pipeline: post-generation completed",
			zap.Int("entities_json_chars", len(result.EntitiesJSON)),
			zap.Int("video_metadata_count", len(result.VideoMetadata)))
	}

	return result, nil
}
