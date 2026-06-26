package scripts

import (
	"context"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

type Pipeline struct {
	log           *zap.Logger
	tag           string
	scenesSvc     *ScenesService
	docsSvc       *DocumentsService
	postGen       interface{}
	resolveFolder FolderResolver
}

type FolderResolver func(ctx context.Context, input, defaultRootID string) (string, error)

type PipelineResult struct {
	EntitiesJSON  string
	Insights      interface{}
	VideoMetadata []VideoMetadata
	DocLink       string
	DocID         string
	DocError      string
	Scenes        []SceneImage
	Voiceovers    []SceneVoiceover
}

type SceneImage struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
	URL   string `json:"url"`
}

type SceneVoiceover struct {
	SceneIndex int    `json:"scene_index"`
	Status     string `json:"status"`
	Link       string `json:"link"`
	LocalPath  string `json:"local_path"`
}

type VideoMetadata struct {
	Language    string   `json:"language"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

type PostGenFunc func(ctx context.Context, spec *scriptpkg.GenerationSpec, script string) (entitiesJSON string, insights any, videoMetadata []VideoMetadata)

func NewPipeline(log *zap.Logger, tag string, scenesSvc *ScenesService, docsSvc *DocumentsService, postGen PostGenFunc, resolveFolder FolderResolver) *Pipeline {
	return &Pipeline{log: log, tag: tag, scenesSvc: scenesSvc, docsSvc: docsSvc, postGen: postGen, resolveFolder: resolveFolder}
}

func (p *Pipeline) Run(ctx context.Context, spec *scriptpkg.GenerationSpec, script string, tools interface{}) (*PipelineResult, error) {
	return p.RunWithClipScenes(ctx, spec, script, nil, tools)
}

func (p *Pipeline) RunWithClipScenes(ctx context.Context, spec *scriptpkg.GenerationSpec, script string, clipScenes []ClipScene, _ interface{}) (*PipelineResult, error) {
	if p == nil {
		return &PipelineResult{}, nil
	}
	result := &PipelineResult{}
	if pg, ok := p.postGen.(PostGenFunc); ok {
		result.EntitiesJSON, result.Insights, result.VideoMetadata = pg(ctx, spec, script)
	}
	if spec != nil && spec.CreateDoc {
		title := strings.TrimSpace(spec.Title)
		if title == "" {
			title = strings.TrimSpace(spec.Topic)
		}
		if title == "" {
			title = "Generated Script"
		}
		if p.docsSvc == nil {
			result.DocError = "documents service not configured"
		} else {
			content := BuildScriptDocumentContent(title, script, clipScenes, result.EntitiesJSON)
			result.DocLink, result.DocID = p.docsSvc.CreateDoc(ctx, title, content, p.resolveFolder, spec.DriveFolderID)
			if result.DocLink == "" || result.DocID == "" {
				result.DocError = "google doc creation failed"
			}
		}
	}
	if p.log != nil {
		p.log.Info("post-generation completed", zap.Int("clip_scenes", len(clipScenes)), zap.Bool("doc_created", result.DocLink != ""), zap.String("doc_error", result.DocError))
	}
	return result, nil
}
