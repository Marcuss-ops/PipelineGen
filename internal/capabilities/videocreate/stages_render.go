package videocreate

import (
	"context"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── 06_overlay_plan ──────────────────────────────────────────────────
//
// The §14-adjacent planning step. Overlays enter the RENDER plan (they
// are composited inside the render pass, never as a post-render
// transcode); cover/thumbnail is NOT a render-lane phase and stays
// owned by the caller side (§19 option B). This step is pure planning
// over the script facts — no children, no media binaries.

func runOverlayPlanStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	if !run.Request.Overlays {
		out.Skipped = true
		return out, nil
	}
	plan := &OverlayPlanFact{
		Style:         "default",
		TextOverlays:  len(run.Facts.TextSegments),
		ImageOverlays: 0,
	}
	for _, clip := range run.Facts.Acquired {
		if clip.Ref.AssetID != "" {
			plan.FrameAssetIDs = append(plan.FrameAssetIDs, clip.Ref.AssetID)
		}
	}
	run.Facts.OverlayPlan = plan
	return out, nil
}

// ── 07_render ────────────────────────────────────────────────────────
//
// The §14 stage. ONE clip.render child per scene — the RenderingGen →
// Chronon boundary stays behind clip.render; video.create never talks
// to Chronon. Each child returns a COPY-CERTIFIED segment (closed GOP,
// first-frame keyframe, stream signature) and the workflow refuses a
// segment whose copy certification is missing: the canonical assembler
// is copy-only and there is no re-encode fallback.

func runRenderStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	scenes := run.Facts.Scenes
	if len(scenes) == 0 {
		return out, fmt.Errorf("%w: render stage has no scene plan", ErrWorkflowFailed)
	}
	type pending struct {
		id      string
		sceneID string
		index   int
	}
	var issued []pending
	for i, sceneID := range scenes {
		sceneIndex := i + 1
		var sourceRefs []string
		for _, clip := range run.Facts.Acquired {
			if clip.SceneIndex == sceneIndex && clip.Ref.AssetID != "" {
				sourceRefs = append(sourceRefs, clip.Ref.AssetID)
			}
		}
		childID, err := run.enqueue(ctx, spec, SceneChildKey(run.RootKey, "render", sceneIndex), job.TypeClipRender, RenderChildRequest{
			Project:      run.Job.Project,
			SceneID:      sceneID,
			SceneIndex:   sceneIndex,
			SourceRefs:   sourceRefs,
			OverlayPlan:  run.Request.Overlays,
			AspectRatio:  run.Request.AspectRatio,
			Language:     run.Request.Language,
			TextSegments: run.Facts.TextSegments,
		})
		if err != nil {
			return out, fmt.Errorf("%w: render stage enqueue: %v", ErrWorkflowFailed, err)
		}
		issued = append(issued, pending{id: childID, sceneID: sceneID, index: sceneIndex})
	}
	for _, p := range issued {
		child, err := run.await(ctx, spec, p.id)
		if err != nil {
			return out, fmt.Errorf("%w: render stage wait: %v", ErrWorkflowFailed, err)
		}
		if child.Status != job.StatusSucceeded {
			return out, fmt.Errorf("%w: clip.render child %s failed: %s", ErrWorkflowFailed, p.id, child.Error)
		}
		var res RenderChildResult
		if err := childResult(child, &res); err != nil {
			return out, fmt.Errorf("%w: render child result: %v", ErrWorkflowFailed, err)
		}
		if res.AssetID == "" || res.LocalPath == "" {
			return out, fmt.Errorf("%w: clip.render child %s produced no asset/local materialization", ErrWorkflowFailed, p.id)
		}
		if !res.CopyCertified {
			return out, fmt.Errorf("%w: clip.render child %s segment is not copy-certified (the canonical assembler is copy-only)", ErrWorkflowFailed, p.id)
		}
		segment := AssembleSegment{
			AssetID:         res.AssetID,
			SHA256:          res.ContentSHA,
			DurationMS:      res.DurationMS,
			CopyCertified:   res.CopyCertified,
			ContractID:      res.ContractID,
			StreamSignature: res.StreamSignature,
		}
		out.ChildJobs = append(out.ChildJobs, p.id)
		out.Artifacts = append(out.Artifacts, StageArtifactRef{
			AssetID:    res.AssetID,
			Kind:       "rendered_clip",
			ContentSHA: res.ContentSHA,
			DurationMS: res.DurationMS,
			MediaType:  "video",
		})
		run.Facts.Rendered = append(run.Facts.Rendered, RenderedClip{
			Segment:   segment,
			LocalPath: res.LocalPath,
			SceneID:   p.sceneID,
		})
	}
	return out, nil
}

// ── 08_assemble ──────────────────────────────────────────────────────
//
// The §15 stage: timeline-ordered assembly of the copy-certified
// segments through the CANONICAL production assembly path (the
// Assembler port → assembly.prepare/finalize contract). Deliberately
// NOT a private concat and NOT the unwired video.assemble.copy.v1
// backend "because it exists".

func runAssembleStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	if len(run.Facts.Rendered) == 0 {
		return out, fmt.Errorf("%w: assemble stage has no rendered segments", ErrWorkflowFailed)
	}
	segments := make([]AssembleSegment, 0, len(run.Facts.Rendered))
	timeline := make([]AssembleScene, 0, len(run.Facts.Rendered))
	for _, clip := range run.Facts.Rendered {
		segments = append(segments, clip.Segment)
		timeline = append(timeline, AssembleScene{SceneID: clip.SceneID, AssetID: clip.Segment.AssetID})
	}
	assembled, err := run.Deps.Assembler.Assemble(ctx, AssembleRequest{
		AssemblyID: run.RootKey,
		ParentJob:  run.Job,
		Segments:   segments,
		Timeline:   timeline,
	})
	if err != nil {
		return out, fmt.Errorf("%w: assemble: %v", ErrWorkflowFailed, err)
	}
	if assembled.ArtifactID == "" || assembled.Path == "" {
		return out, fmt.Errorf("%w: assemble produced no artifact_id/path", ErrWorkflowFailed)
	}
	// ChildJobs contract: [prepare, finalize] (state.ChildLedger).
	out.ChildJobs = assembled.ChildJobIDs
	out.Artifacts = []StageArtifactRef{{
		AssetID:    assembled.ArtifactID,
		Kind:       "assembled_video",
		ContentSHA: assembled.SHA256,
		DurationMS: assembled.DurationMS,
		MediaType:  "video",
	}}
	out.Assembled = &assembled
	run.Facts.Assembled = &assembled
	return out, nil
}

// ── 09_audio_mux ─────────────────────────────────────────────────────
//
// The §16 stage: mux_audio_copy(assembled_video, canonical_final_audio)
// → final_video.mp4 — a PRODUCTION step of the workflow, never the
// manual ops/jobs/remote/mux-final-audio.sh script (that stays as the
// operational verifier/fallback). Skipped only when the run has no
// canonical master at all (voiceover=false and no §9 reused master): in
// that case the final video keeps the clips' own audio and the §17 gate
// still requires an audio stream.

func runAudioMuxStage(ctx context.Context, run *Run, spec StepSpec) (StageOutput, error) {
	out := StageOutput{}
	if run.Facts.FinalAudio == nil || run.Facts.Assembled == nil {
		out.Skipped = true
		return out, nil
	}
	muxed, err := run.Deps.Audio.Mux(ctx, MuxRequest{
		VideoPath:  run.Facts.Assembled.Path,
		FinalAudio: *run.Facts.FinalAudio,
		OutputPath: run.workPath("final_video.mp4"),
	})
	if err != nil {
		return out, fmt.Errorf("%w: audio mux: %v", ErrWorkflowFailed, err)
	}
	run.Facts.Muxed = &muxed
	out.Artifacts = []StageArtifactRef{{
		Kind:      "final_video",
		MediaType: "video",
	}}
	return out, nil
}
