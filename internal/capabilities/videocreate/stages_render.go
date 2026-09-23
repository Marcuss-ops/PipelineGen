package videocreate

import (
	"context"
	"errors"
	"fmt"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
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
		payload cliprender.RenderRequest
	}
	var issued []pending
	for i, sceneID := range scenes {
		sceneIndex := i + 1
		sourceAssetID := ""
		for _, clip := range run.Facts.Acquired {
			if clip.SceneIndex == sceneIndex && clip.Ref.AssetID != "" {
				sourceAssetID = clip.Ref.AssetID
				break
			}
		}
		if sourceAssetID == "" {
			return out, fmt.Errorf("%w: scene %s has no acquired source asset", ErrWorkflowFailed, sceneID)
		}
		// The requested-language transcript must be READY before the render
		// fan-out: 07_render reuses it fail-closed, and the translations are
		// materialized asynchronously right after media acquisition.
		if run.Deps.Texts != nil {
			if err := run.Deps.Texts.WaitTranscriptReady(ctx, sourceAssetID, run.Request.Language); err != nil {
				return out, fmt.Errorf("%w: render stage transcript: %v", ErrWorkflowFailed, err)
			}
		}
		payload, err := RenderChildPayload(sourceAssetID, run.Request.Language, run.Request.AspectRatio, run.Request.Overlays)
		if err != nil {
			return out, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
		}
		childID, err := run.enqueue(ctx, spec, SceneChildKey(run.RootKey, "render", sceneIndex), job.TypeClipRender, payload)
		if err != nil {
			return out, fmt.Errorf("%w: render stage enqueue: %v", ErrWorkflowFailed, err)
		}
		issued = append(issued, pending{id: childID, sceneID: sceneID, index: sceneIndex, payload: payload})
	}
	for _, p := range issued {
		// A render execution failure (the settle boundary) is the one
		// transient class a bounded re-attempt can recover from: the
		// GPU/encoder window that starved the render may have passed.
		// Contract and payload failures never carry renderSettleError and
		// fail closed on the first attempt.
		var res RenderChildResult
		childID := p.id
		for attempt := 1; ; attempt++ {
			var err error
			res, err = awaitRenderedSegment(ctx, run, spec, childID)
			if err == nil {
				break
			}
			var settleErr renderSettleError
			if !errors.As(err, &settleErr) || attempt >= renderSceneAttempts {
				return out, fmt.Errorf("%w: render stage: %v", ErrWorkflowFailed, err)
			}
			run.event("render_retry", spec, map[string]any{
				"scene_id":   p.sceneID,
				"attempt":    attempt + 1,
				"last_child": childID,
				"reason":     err.Error(),
			})
			childID, err = run.enqueue(ctx, spec, renderAttemptChildKey(run.RootKey, p.index, attempt+1), job.TypeClipRender, p.payload)
			if err != nil {
				return out, fmt.Errorf("%w: render stage retry enqueue: %v", ErrWorkflowFailed, err)
			}
		}
		segment, err := res.assembleSegment()
		if err != nil {
			return out, fmt.Errorf("%w: clip.render child %s: %v", ErrWorkflowFailed, childID, err)
		}
		out.ChildJobs = append(out.ChildJobs, childID)
		out.Artifacts = append(out.Artifacts, StageArtifactRef{
			AssetID:    res.AssetID,
			Kind:       "rendered_clip",
			DurationMS: res.DurationMS,
			MediaType:  "video",
			Source:     res.SourceAssetID,
		})
		run.Facts.Rendered = append(run.Facts.Rendered, RenderedClip{
			Segment:   segment,
			LocalPath: res.LocalPath,
			SceneID:   p.sceneID,
		})
	}
	return out, nil
}

// renderSceneAttempts bounds the render-stage re-attempts: one initial
// child plus at most this many total submissions per scene.
const renderSceneAttempts = 3

// renderAttemptChildKey derives the idempotency key of a re-attempted
// render. A new attempt must never collapse onto the previous settle
// chain (cliprender.ActiveKeyFor's rule), so the attempt is part of the
// key.
func renderAttemptChildKey(root string, sceneIndex, attempt int) string {
	return fmt.Sprintf("%s:attempt:%d", SceneChildKey(root, "render", sceneIndex), attempt)
}

// awaitRenderedSegment waits for one clip.render child and projects its
// materialized segment.
//
// The clip.render async boundary (cliprender/worker.go submit result)
// returns a submit-phase envelope — phase="submitted" plus the settle
// continuation child_job_id — BEFORE the remote render materializes:
// the segment lands in the settle child's result (the settle owns
// materialize/probe/publish), never in the submit-phase one. Following
// that continuation is part of consuming the contract; decoding the
// submit-phase result directly would mistake an in-flight remote render
// for a finished segment. A settle failure is returned as
// renderSettleError so the stage can decide on a re-attempt.
func awaitRenderedSegment(ctx context.Context, run *Run, spec StepSpec, childID string) (RenderChildResult, error) {
	child, err := run.await(ctx, spec, childID)
	if err != nil {
		return RenderChildResult{}, fmt.Errorf("render stage wait: %w", err)
	}
	if child.Status != job.StatusSucceeded {
		return RenderChildResult{}, fmt.Errorf("clip.render child %s failed: %s", childID, child.Error)
	}
	for hops := 0; ; hops++ {
		settleID := renderSettleChildID(child.Result)
		if settleID == "" {
			break
		}
		if hops >= 3 {
			return RenderChildResult{}, fmt.Errorf("clip.render child %s: settle chain deeper than 3 continuations", childID)
		}
		child, err = run.await(ctx, spec, settleID)
		if err != nil {
			return RenderChildResult{}, fmt.Errorf("render settle wait: %w", err)
		}
		if child.Status != job.StatusSucceeded {
			return RenderChildResult{}, renderSettleError{id: settleID, reason: child.Error}
		}
	}
	res, err := decodeRenderChildResult(child.Result)
	if err != nil {
		return RenderChildResult{}, err
	}
	if _, err := res.assembleSegment(); err != nil {
		return RenderChildResult{}, err
	}
	return res, nil
}

// ── 08_assemble ──────────────────────────────────────────────────────
//
// The §15 stage: timeline-ordered assembly of the copy-certified
// segments through the CANONICAL production assembly boundary — the
// VeloxEditing media plane (assemble_copy / video.assemble.copy.v1:
// packet-copy, zero decode/encode). Deliberately NOT a private concat
// and NOT RenderingGen's chunk finalizer (that lane assembles the
// chunks of ONE render job; this one assembles independent scenes).

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
		OutputPath: run.workPath("assembled_video.mp4"),
	})
	if err != nil {
		return out, fmt.Errorf("%w: assemble: %v", ErrWorkflowFailed, err)
	}
	if assembled.ArtifactID == "" || assembled.Path == "" {
		return out, fmt.Errorf("%w: assemble produced no artifact_id/path", ErrWorkflowFailed)
	}
	// ChildJobs contract: EMPTY — the boundary is the VeloxEditing media
	// plane itself (assemble_copy), never a fan-out of child jobs.
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
