// Package scripts provides the script generation pipeline orchestration.
// Merged from internal/application/scriptflow/jobs/.
package scripts

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// ── Pipeline ────────────────────────────────────────────────────────────────

// Pipeline runs the post-script-generation phases of a clip script job:
//   Phase 2 (parallel): entity_metadata ‖ scene_images
//   Phase 3 (sequential):   scene_voiceovers
//   Phase 4 (sequential):   google_doc
//
// It does NOT handle path dispatch (Phase 1) or semaphore acquisition —
// those remain in the HTTP handler. Pipeline is constructed with function
// callbacks to avoid importing API-layer types.
type Pipeline struct {
	log    *zap.Logger
	jobID  string
	scenes *ScenesService
	docs   *DocumentsService

	// PostGeneration runs entity extraction and metadata generation.
	postGeneration func(ctx context.Context, spec *script.GenerationSpec, scr string) (entitiesJSON string, insights any, videoMetadata []VideoMetadata)

	// ResolveFolder resolves a folder name/ID to a Drive folder ID.
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error)
}

// NewPipeline creates a new Pipeline.
func NewPipeline(
	log *zap.Logger,
	jobID string,
	scenesSvc *ScenesService,
	docsSvc *DocumentsService,
	postGeneration func(ctx context.Context, spec *script.GenerationSpec, scr string) (string, any, []VideoMetadata),
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
) *Pipeline {
	return &Pipeline{
		log:            log,
		jobID:          jobID,
		scenes:         scenesSvc,
		docs:           docsSvc,
		postGeneration: postGeneration,
		resolveFolder:  resolveFolder,
	}
}

// PipelineRunResult holds the outputs of the post-generation pipeline.
type PipelineRunResult struct {
	EntitiesJSON  string
	Insights      any
	VideoMetadata []VideoMetadata
	Scenes        []SceneImage
	Voiceovers    []SceneVoiceover
	DocLink       string
	DocID         string
	TotalMs       int64
}

// Run executes Phases 2–4 after the script has been generated (Phase 1).
func (p *Pipeline) Run(
	ctx context.Context,
	spec *script.GenerationSpec,
	scr string,
	tools *appjobs.JobTools,
) (*PipelineRunResult, error) {
	startAll := time.Now()

	// ── Phase 2 (parallel): entity_metadata ‖ scene_images ─────────────
	var (
		phase2Mu        sync.Mutex
		phase2Entities  string
		phase2Insights  any
		phase2VideoMeta []VideoMetadata
		phase2Scenes    []SceneImage
	)
	phase2Run := spec.ExtractEntities || spec.GenerateMetadata || spec.GenerateSceneImages
	if phase2Run {
		if tools.Progress != nil {
			tools.Progress(70, "Phase 2: entities + scene images (parallel)...")
		}
		phase2Start := time.Now()
		p.log.Info("phase2_fanout_started",
			zap.String("job_id", p.jobID),
			zap.Bool("entity_metadata_enabled", spec.ExtractEntities || spec.GenerateMetadata),
			zap.Bool("scene_images_enabled", spec.GenerateSceneImages))
		group, groupCtx := concurrent.WithContext(ctx)
		if spec.ExtractEntities || spec.GenerateMetadata {
			group.Go("entity_metadata", func() error {
				stagePost := pipelineStageLog(p.log, p.jobID, "entity_metadata")
				postStart := time.Now()
				ents, ins, meta := p.postGeneration(groupCtx, spec, scr)
				phase2Mu.Lock()
				phase2Entities = ents
				phase2Insights = ins
				phase2VideoMeta = meta
				phase2Mu.Unlock()
				stagePost(
					zap.Int("entities_chars", len(ents)),
					zap.Int("metadata_count", len(meta)),
					zap.Int64("post_ms", time.Since(postStart).Milliseconds()))
				return nil
			})
		}
		if spec.GenerateSceneImages {
			group.Go("scene_images", func() error {
				stageScenes := pipelineStageLog(p.log, p.jobID, "scene_images")
				scenesStart := time.Now()
				var progressFn ProgressReporter
				if tools != nil {
					progressFn = tools.Progress
				}
				scns := p.scenes.GenerateImages(groupCtx, spec, scr, progressFn)
				okScenes := 0
				for _, s := range scns {
					if len(s.Images) > 0 {
						okScenes++
					}
				}
				phase2Mu.Lock()
				phase2Scenes = scns
				phase2Mu.Unlock()
				stageScenes(
					zap.Int("total_scenes", len(scns)),
					zap.Int("ok_scenes", okScenes),
					zap.Int64("scenes_ms", time.Since(scenesStart).Milliseconds()))
				return nil
			})
		}
		if waitErr := group.Wait(); waitErr != nil {
			p.log.Warn("phase2_fanout_partial_errors",
				zap.String("job_id", p.jobID), zap.Error(waitErr))
		}
		p.log.Info("phase2_fanout_completed",
			zap.String("job_id", p.jobID),
			zap.Int64("phase2_ms", time.Since(phase2Start).Milliseconds()))
	}

	// ── Phase 3 (sequential after Phase 2): scene voiceovers ────────
	var voiceovers []SceneVoiceover
	if spec.GenerateVoiceover && p.scenes != nil {
		if tools.Progress != nil {
			tools.Progress(80, "Generating scene-by-scene voiceovers...")
		}
		stageVoiceover := pipelineStageLog(p.log, p.jobID, "scene_voiceovers")
		voStart := time.Now()
		voiceovers = p.scenes.GenerateVoiceovers(ctx, spec, phase2Scenes)
		okVoices := 0
		for _, v := range voiceovers {
			if v.Status == "completed" {
				okVoices++
			}
		}
		stageVoiceover(
			zap.Int("voiceover_total", len(voiceovers)),
			zap.Int("voiceover_ok", okVoices),
			zap.Int64("voiceover_ms", time.Since(voStart).Milliseconds()))
	}

	// ── Phase 4: Google Doc ─────────────────────────────────────────
	if tools.Progress != nil {
		tools.Progress(85, "Creating Google Doc...")
	}
	stageDoc := pipelineStageLog(p.log, p.jobID, "google_doc")
	docStart := time.Now()
	docLink, docID := p.createDoc(ctx, spec, scr, phase2Entities, phase2Insights, phase2VideoMeta, phase2Scenes)
	if docLink != "" {
		stageDoc(zap.String("status", "ok"), zap.String("doc_id", docID), zap.Int64("doc_ms", time.Since(docStart).Milliseconds()))
	} else {
		stageDoc(zap.String("status", "skipped_or_failed"), zap.Int64("doc_ms", time.Since(docStart).Milliseconds()))
	}

	totalDurMs := time.Since(startAll).Milliseconds()

	p.log.Info("pipeline_completed",
		zap.String("job_id", p.jobID),
		zap.Int64("total_ms", totalDurMs),
		zap.Int("scenes", len(phase2Scenes)),
		zap.Int("voiceovers", len(voiceovers)),
		zap.Bool("has_doc", docLink != ""))

	if tools.Progress != nil {
		tools.Progress(100, "Generation completed")
	}

	return &PipelineRunResult{
		EntitiesJSON:  phase2Entities,
		Insights:      phase2Insights,
		VideoMetadata: phase2VideoMeta,
		Scenes:        phase2Scenes,
		Voiceovers:    voiceovers,
		DocLink:       docLink,
		DocID:         docID,
		TotalMs:       totalDurMs,
	}, nil
}

// createDoc builds the Google Doc content and creates it.
func (p *Pipeline) createDoc(
	ctx context.Context,
	spec *script.GenerationSpec,
	scr string,
	entitiesJSON string,
	insights any,
	videoMetadata []VideoMetadata,
	scenes []SceneImage,
) (docLink, docID string) {
	if p.docs == nil {
		return "", ""
	}

	// Convert SceneImage → SceneRef for content building.
	sceneRefs := make([]SceneRef, len(scenes))
	for i, s := range scenes {
		sceneRefs[i] = SceneRef{
			Text:          s.Text,
			Image:         s.Image,
			Images:        s.Images,
			Kind:          s.Kind,
			NarrationRole: s.NarrationRole,
		}
	}

	docInsights := ScriptInsights{}
	if ins, ok := insights.(ScriptInsights); ok {
		docInsights = ins
	}

	content := BuildContent(
		spec.Title,
		scr,
		spec.TargetWords,
		videoMetadata,
		entitiesJSON,
		docInsights,
		sceneRefs,
	)

	return p.docs.CreateDoc(ctx, spec.Title, content, p.resolveFolder, spec.DriveFolderID)
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// pipelineStageLog wraps a pipeline phase with structured start/complete logs.
func pipelineStageLog(log *zap.Logger, jobID, stage string) func(extra ...zap.Field) {
	t := time.Now()
	log.Info("pipeline_stage_started",
		zap.String("job_id", jobID),
		zap.String("stage", stage))
	return func(extra ...zap.Field) {
		fields := append([]zap.Field{
			zap.String("job_id", jobID),
			zap.String("stage", stage),
			zap.Int64("duration_ms", time.Since(t).Milliseconds()),
		}, extra...)
		log.Info("pipeline_stage_completed", fields...)
	}
}
