package cliprender

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

const payloadKeyContinuation = "continuation"

// continuationPayload is deliberately tiny: the sealed plan and preparation
// values live in CAS, while the job row carries only this address and the
// durable submission facts.
type continuationPayload struct {
	RenderPhase  RenderPhase   `json:"render_phase"`
	Continuation *Continuation `json:"continuation,omitempty"`
}

func decodeContinuationPayload(raw json.RawMessage) (RenderPhase, *Continuation, error) {
	var envelope continuationPayload
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", nil, fmt.Errorf("%w: decode render payload: %v", ErrInvalidJobPayload, err)
	}
	phase := envelope.RenderPhase
	if phase == "" {
		phase = RenderPhaseSubmit
	}
	if !phase.IsValid() {
		return "", nil, fmt.Errorf("%w: unknown render_phase=%q", ErrInvalidJobPayload, phase)
	}
	if phase == RenderPhaseSettle {
		if envelope.Continuation == nil {
			return "", nil, fmt.Errorf("%w: settle phase requires %s", ErrInvalidJobPayload, payloadKeyContinuation)
		}
		if err := envelope.Continuation.Validate(); err != nil {
			return "", nil, err
		}
		return phase, envelope.Continuation, nil
	}
	return phase, nil, nil
}

func encodeContinuationPayload(c Continuation) (json.RawMessage, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(continuationPayload{RenderPhase: RenderPhaseSettle, Continuation: &c})
}

func preparedFromResume(doc ResumeDocument) *Prepared {
	prepared := &Prepared{
		RunID:      doc.Plan.RunID,
		Contract:   doc.Contract,
		Transcript: doc.Transcript,
		Timings:    PreparationTimings{},
		Source: &MaterializedAsset{
			AssetID:    doc.Plan.Source.AssetID,
			Title:      doc.SourceTitle,
			LocalPath:  doc.Plan.Source.Path,
			SHA256:     doc.Plan.Source.SHA256,
			SizeBytes:  doc.SourceSizeBytes,
			DurationMS: doc.Plan.DurationMS,
		},
	}
	if doc.Plan.Watermark != nil {
		prepared.Watermark = &MaterializedAsset{
			AssetID:   doc.Plan.Watermark.AssetID,
			LocalPath: doc.Plan.Watermark.Path,
			SHA256:    doc.Plan.Watermark.SHA256,
		}
	}
	if doc.Plan.Background != nil && doc.Plan.Background.Mode == BackgroundModeAsset {
		prepared.Background = &MaterializedAsset{
			AssetID:   doc.Plan.Background.AssetID,
			LocalPath: doc.Plan.Background.Path,
			SHA256:    doc.Plan.Background.SHA256,
		}
	}
	return prepared
}

func continuationResumeFolder(resolved, fallback string) string {
	if resolved != "" {
		return resolved
	}
	return fallback
}

func (w *Worker) preparePlan(ctx context.Context, req *RenderRequest, runID string, emit func(string, string, map[string]any)) (*Prepared, ClipRenderPlanV1, *SubtitleArtifact, int64, error) {
	prepareStart := time.Now()
	prepared, err := w.preparer.Prepare(ctx, req, runID)
	prepareEnd := time.Now()
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipPrepare}, prepareStart, prepareEnd, err)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhasePrepare, prepareStart, prepareEnd, kernobs.StageStatusCompleted, err)
	if err != nil {
		return nil, ClipRenderPlanV1{}, nil, -1, fmt.Errorf("clip.render: prepare: %w", err)
	}

	runDir := filepath.Join(w.workspaceDir, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, ClipRenderPlanV1{}, nil, -1, fmt.Errorf("clip.render: create run directory: %w", err)
	}
	var subtitleArtifact *SubtitleArtifact
	subtitleCompileMS := int64(-1)
	if req.Subtitles.Enabled {
		if w.subtitles == nil {
			return nil, ClipRenderPlanV1{}, nil, -1, fmt.Errorf("%w: subtitles.enabled=true but no SubtitleCompiler is wired", ErrSubtitleCompileUnavailable)
		}
		subtitleCompileStart := time.Now()
		subtitleArtifact, err = w.subtitles.Compile(ctx, SubtitleCompileInput{
			RunID: runID, AssetID: req.SourceAssetID, Language: prepared.Transcript.Language,
			Mode: req.Subtitles.Mode, StyleID: req.Subtitles.StyleID, Cues: prepared.Transcript.Cues,
			ClipDurationMS: prepared.Source.DurationMS, SourceSHA256: prepared.Source.SHA256, OutputDir: runDir,
		})
		kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipSubtitles}, subtitleCompileStart, time.Now(), err)
		if err != nil {
			return nil, ClipRenderPlanV1{}, nil, -1, fmt.Errorf("clip.render: compile subtitles: %w", err)
		}
		subtitleCompileMS = time.Since(subtitleCompileStart).Milliseconds()
		emit("clip.render.subtitles.compiled", "ASS artifact compiled", map[string]any{
			"path": subtitleArtifact.LocalPath, "sha256": subtitleArtifact.SHA256,
			"mode": subtitleArtifact.Mode, "cue_count": len(prepared.Transcript.Cues),
		})
	}

	var overlayInput *PlanOverlayInput
	if req.Overlay != nil {
		if w.overlayResolver == nil {
			return nil, ClipRenderPlanV1{}, nil, -1, fmt.Errorf("clip.render: overlay declared but no OverlaySegmentResolver is wired")
		}
		segment, resolveErr := w.overlayResolver.Resolve(ctx, OverlayResolveInput{RenderJobID: req.Overlay.RenderJobID, RenderKey: req.Overlay.RenderKey})
		if resolveErr != nil {
			return nil, ClipRenderPlanV1{}, nil, -1, fmt.Errorf("clip.render: resolve overlay segment: %w", resolveErr)
		}
		if segment == nil || segment.LocalPath == "" || segment.SHA256 == "" {
			return nil, ClipRenderPlanV1{}, nil, -1, fmt.Errorf("clip.render: overlay resolver returned an invalid segment")
		}
		overlayInput = &PlanOverlayInput{Segment: segment, StartMS: (req.Overlay.StartUS + 500) / 1000, EndMS: (req.Overlay.EndUS + 500) / 1000}
		emit("clip.render.overlay.single_pass", "overlay composited inside the Chronon render pass (single encode)", map[string]any{
			"render_job_id": req.Overlay.RenderJobID, "render_key": req.Overlay.RenderKey,
			"sha256": segment.SHA256, "start_ms": overlayInput.StartMS, "end_ms": overlayInput.EndMS,
		})
	}

	var watermarkSpec *WatermarkSpec
	if req.Watermark != nil && req.Watermark.Enabled {
		watermarkSpec = req.Watermark
	}
	plan, err := Compile(CompileInput{
		RunID: runID, Source: prepared.Source, DurationMS: prepared.Source.DurationMS,
		Watermark: prepared.Watermark, WatermarkSpec: watermarkSpec, Background: prepared.Background,
		BackgroundMode: req.Background.Mode, Subtitles: subtitleArtifact, SubtitlesStyle: req.Subtitles.Style,
		Cues: prepared.Transcript.Cues, Contract: prepared.Contract, AudioMode: req.Audio.Mode,
		Overlay: overlayInput, OutputPath: filepath.Join(runDir, "rendered-clip.mp4"),
		ForegroundScalePercent: req.Output.ForegroundScalePercent,
	})
	if err != nil {
		return nil, ClipRenderPlanV1{}, nil, -1, fmt.Errorf("clip.render: compile plan: %w", err)
	}
	return prepared, plan, subtitleArtifact, subtitleCompileMS, nil
}
