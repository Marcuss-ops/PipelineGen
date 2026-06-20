// Package jobs provides the script generation pipeline orchestration.
// It lives in the application layer and wires together scene generation,
// entity extraction, voiceover generation, and document creation without
// importing from the HTTP transport layer.
package jobs

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/documents"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/scenes"
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
	scenes *scenes.Service
	docs   *documents.Service

	// PostGeneration runs entity extraction and metadata generation.
	// Returns entities JSON, insights (any for JSON-agnostic transport), and video metadata.
	postGeneration func(ctx context.Context, spec *script.GenerationSpec, script string) (entitiesJSON string, insights any, videoMetadata []documents.VideoMetadata)

	// ResolveFolder resolves a folder name/ID to a Drive folder ID.
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error)
}

// NewPipeline creates a new Pipeline.
func NewPipeline(
	log *zap.Logger,
	jobID string,
	scenesSvc *scenes.Service,
	docsSvc *documents.Service,
	postGeneration func(ctx context.Context, spec *script.GenerationSpec, script string) (string, any, []documents.VideoMetadata),
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

// RunResult holds the outputs of the post-generation pipeline.
type RunResult struct {
	EntitiesJSON  string
	Insights      any
	VideoMetadata []documents.VideoMetadata
	Scenes        []scenes.SceneImage
	Voiceovers    []scenes.SceneVoiceover
	DocLink       string
	DocID         string
	TotalMs       int64
}

// Run executes Phases 2–4 after the script has been generated (Phase 1).
// spec is the parsed generation spec, script is the LLM output, and
// tools provides progress/event callbacks.
func (p *Pipeline) Run(
	ctx context.Context,
	spec *script.GenerationSpec,
	script string,
	tools *appjobs.JobTools,
) (*RunResult, error) {
	startAll := time.Now()

	// ── Phase 2 (parallel): entity_metadata ‖ scene_images ─────────────
	var (
		phase2Mu        sync.Mutex
		phase2Entities  string
		phase2Insights  any
		phase2VideoMeta []documents.VideoMetadata
		phase2Scenes    []scenes.SceneImage
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
				stagePost := stageLog(p.log, p.jobID, "entity_metadata")
				postStart := time.Now()
				ents, ins, meta := p.postGeneration(groupCtx, spec, script)
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
				stageScenes := stageLog(p.log, p.jobID, "scene_images")
				scenesStart := time.Now()
				var progressFn scenes.ProgressReporter
				if tools != nil {
					progressFn = tools.Progress
				}
				scns := p.scenes.GenerateImages(groupCtx, spec, script, progressFn)
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
	var voiceovers []scenes.SceneVoiceover
	if spec.GenerateVoiceover && p.scenes != nil {
		if tools.Progress != nil {
			tools.Progress(80, "Generating scene-by-scene voiceovers...")
		}
		stageVoiceover := stageLog(p.log, p.jobID, "scene_voiceovers")
		voStart := time.Now()
		voiceovers = p.scenes.GenerateVoiceovers(ctx, spec, phase2Scenes)
		okVoices := 0
		for _, v := range voiceovers {
			if v.job.Status == "completed" {
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
	stageDoc := stageLog(p.log, p.jobID, "google_doc")
	docStart := time.Now()
	docLink, docID := p.createDoc(ctx, spec, script, phase2Entities, phase2Insights, phase2VideoMeta, phase2Scenes)
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

	return &RunResult{
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
	script string,
	entitiesJSON string,
	insights any,
	videoMetadata []documents.VideoMetadata,
	scenes []scenes.SceneImage,
) (docLink, docID string) {
	if p.docs == nil {
		return "", ""
	}

	// Convert scenes.SceneImage → documents.SceneRef for content building.
	sceneRefs := make([]documents.SceneRef, len(scenes))
	for i, s := range scenes {
		sceneRefs[i] = documents.SceneRef{
			Text:          s.Text,
			Image:         s.Image,
			Images:        s.Images,
			Kind:          s.Kind,
			NarrationRole: s.NarrationRole,
		}
	}

	// Build insights for the doc — pass through as any (typed at call site).
	docInsights := documents.ScriptInsights{}
	// If insights carries the expected shape, populate it. The caller's
	// postGeneration callback returns any; we attempt a best-effort populate.
	if ins, ok := insights.(documents.ScriptInsights); ok {
		docInsights = ins
	}
	// Always include the string fields (these are always present).
	// The any-type fields (ArtlistClipSuggestions, etc.) are populated
	// by the type assertion above when available.

	content := documents.BuildContent(
		spec.Title,
		script,
		spec.TargetWords,
		videoMetadata,
		entitiesJSON,
		docInsights,
		sceneRefs,
	)

	return p.docs.CreateDoc(ctx, spec.Title, content, p.resolveFolder, spec.DriveFolderID)
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// stageLog wraps a pipeline phase with structured start/complete logs.
// (Moved from the API handler; identical to the original.)
func stageLog(log *zap.Logger, jobID, stage string) func(extra ...zap.Field) {
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