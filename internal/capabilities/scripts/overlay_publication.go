package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// OverlayPublicationSpec carries the stable logical identity used by Drive
// routing. It deliberately contains no provider-specific fields: the
// platform adapter owns the concrete publication contract.
type OverlayPublicationSpec struct {
	ScriptName string
	Language   string
	ProjectID  string
	// JobID is the stable generation/run identity used for Drive folder
	// routing. PlanID may be a per-item child plan (for example
	// job:item:003:phrase), so it must not be used as the Drive job folder
	// when separate item renders are enabled.
	JobID  string
	PlanID string
	// DriveFolderID is the job-selected Drive parent. The platform publisher
	// creates/reuses its deterministic overlay child and only falls back to
	// its configured root when this is empty.
	DriveFolderID string
	// RequireDriveBeforeReturn marks artifacts whose Drive identity is needed
	// immediately by a downstream handoff payload.
	RequireDriveBeforeReturn bool

	// Completion metrics are captured by PipelineGen while waiting for the
	// RenderingGen queue. They are copied into the Drive receipt so the
	// published overlay has an auditable timing record next to it.
	CompletionWait  time.Duration
	PollingSleep    time.Duration
	PollingInterval time.Duration
	PollCount       int

	// OverlayItem* identify the single semantic item rendered into this
	// artifact. They make every Drive video/receipt auditable without forcing
	// downstream consumers to reconstruct the source plan.
	OverlayItemID       string
	OverlayItemKind     string
	OverlayEntityID     string
	OverlayEntityIDs    []string
	OverlayEntityLabels []string
	OverlayText         string
	SourceStartUS       int64
	SourceEndUS         int64
	TargetDurationUS    int64
}

// OverlayArtifactPublisher publishes a certified RenderingGen artifact after
// the queue has completed. Keeping this as a capability port means the queue
// path cannot silently report success while the required Drive side effect is
// still missing.
type OverlayArtifactPublisher interface {
	PublishOverlay(context.Context, OverlayPublicationSpec, *RenderArtifact) error
}

// ── per-run publication batches ────────────────────────────────────────
//
// The async publication pool is PROCESS-WIDE (the composition root wires one
// QueueRenderEnqueuer for the whole process) while a publication belongs to
// exactly ONE run. The join must therefore be scoped to that run, which is what
// these helpers do; the owning fields live on QueueRenderEnqueuer
// (render_queue.go) next to the pool they guard.

// publicationBatch is the per-run join handle for the async publication pool.
// It owns its own WaitGroup (so a run joins only its own publications) and its
// own first-error slot (so a run is failed only by its own failure).
type publicationBatch struct {
	wg sync.WaitGroup
	mu sync.Mutex
	// err is the FIRST publication failure of this batch. Later failures are
	// dropped on purpose: the first one is the actionable cause and the run
	// fails on it, exactly like the synchronous path.
	err error
}

func (b *publicationBatch) record(err error) {
	if b == nil || err == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err == nil {
		b.err = err
	}
}

// join blocks until every publication submitted to this batch has finished and
// returns the batch's first failure. Taking the error clears it, so a retry of
// the same run neither re-reports an old failure nor is poisoned by it.
func (b *publicationBatch) join() error {
	if b == nil {
		return nil
	}
	b.wg.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	err := b.err
	b.err = nil
	return err
}

// publicationBatchFor resolves the publication batch the work submitted with
// ctx belongs to. The per-run identity is the kernel run bound to ctx (the job
// worker binds it before the script runner is reached), so two concurrent runs
// sharing this enqueuer never share a batch. A caller with no run bound (small
// unit-test compositions, one-shot CLI calls) falls back to the single unbound
// batch, which is the historical process-wide behaviour.
func (e *QueueRenderEnqueuer) publicationBatchFor(ctx context.Context) *publicationBatch {
	e.publicationMu.Lock()
	defer e.publicationMu.Unlock()
	key := kernobs.FromContext(ctx)
	if key == nil {
		if e.publicationUnbound == nil {
			e.publicationUnbound = &publicationBatch{}
		}
		return e.publicationUnbound
	}
	if e.publicationBatches == nil {
		e.publicationBatches = make(map[*kernobs.Run]*publicationBatch)
	}
	if batch, ok := e.publicationBatches[key]; ok {
		return batch
	}
	batch := &publicationBatch{}
	e.publicationBatches[key] = batch
	return batch
}

// publicationBatchForRun returns the EXISTING batch of the run bound to ctx.
// It never creates one: a run with no queued publication has nothing to join.
func (e *QueueRenderEnqueuer) publicationBatchForRun(ctx context.Context) (*kernobs.Run, *publicationBatch) {
	key := kernobs.FromContext(ctx)
	if key == nil {
		e.publicationMu.Lock()
		defer e.publicationMu.Unlock()
		return nil, e.publicationUnbound
	}
	e.publicationMu.Lock()
	defer e.publicationMu.Unlock()
	return key, e.publicationBatches[key]
}

// Wait joins ITS OWN run's queued publication/analytics work. It is the
// completion boundary for the render publication pool: the caller (the runner,
// right before completion) must invoke it before reporting a run as COMPLETE.
//
// The publication pool is process-wide but a run only ever joins its own
// batch, keyed by the kernel run bound to ctx:
//
//   - run A never blocks on run B's in-flight uploads, so a slow concurrent
//     run cannot stretch A's tail latency;
//   - run A is never FAILED by run B's publication error. That misattribution
//     was observed live: a run died on "context canceled" from another run's
//     overlay upload, so the operator's retry could not fix its own run.
//
// The batch is retired after the join (the run is finishing, so nothing else
// can be submitted to it), which also means a later attempt of the same run
// starts clean instead of inheriting a previous failure.
func (e *QueueRenderEnqueuer) Wait(ctx context.Context) error {
	if e == nil || !e.asyncPublication {
		return nil
	}
	run, batch := e.publicationBatchForRun(ctx)
	if batch == nil {
		return nil
	}
	err := batch.join()
	if run != nil {
		e.publicationMu.Lock()
		// Only the batch that was just joined is retired: a concurrent Wait
		// for the same run (or a publication submitted between the join and
		// this lock) must never lose its own join handle.
		if e.publicationBatches[run] == batch {
			delete(e.publicationBatches, run)
		}
		e.publicationMu.Unlock()
	}
	return err
}

// finalJobOverlayAssets projects the already-rendered semantic overlay items
// to the Master finalizer. Their bytes live on Drive; only the remote worker
// fetches them and applies them over the stock timeline.
func finalJobOverlayAssets(result *GenerateResult) ([]any, error) {
	if result == nil || result.OverlayPlan == nil || len(result.OverlayPlan.Items) == 0 {
		return []any{}, nil
	}
	if result.OverlayRender == nil || len(result.OverlayRender.Items) == 0 {
		return nil, fmt.Errorf("final_job has semantic overlays but no published overlay render artifacts")
	}
	byID := make(map[string]RenderArtifact, len(result.OverlayRender.Items))
	for _, rendered := range result.OverlayRender.Items {
		if rendered.Artifact != nil {
			byID[strings.TrimSpace(rendered.ItemID)] = *rendered.Artifact
		}
	}
	fpsNum, fpsDen := result.OverlayPlan.FPSNum, result.OverlayPlan.FPSDen
	if fpsNum <= 0 || fpsDen <= 0 {
		return nil, fmt.Errorf("final_job overlay plan has invalid frame rate %d/%d", fpsNum, fpsDen)
	}
	out := make([]any, 0, len(result.OverlayPlan.Items))
	frameGuardUS := (1_000_000*int64(fpsDen) + int64(fpsNum) - 1) / int64(fpsNum)
	clipWindows := finalJobIntermediateClipWindows(result)
	// One SSOT admission gate for the finalize handoff. Every duplicate
	// surface upstream (entity/context image arms, planner+resolver copies,
	// compose composites) has its own key and its own blind spot; THIS is the
	// last gate before the Master and it sees the exact wire facts: content
	// identity (one overlay per rendered asset) and the FRAME window the
	// Master's mode=replace decoder will enforce (one overlay per frame
	// interval). Both are fail-closed: a payload that violates either never
	// reaches the remote and can never burn a worker attempt on a guaranteed
	// rejection.
	admittedContent := make(map[string]string, len(result.OverlayPlan.Items))
	type frameWindow struct{ start, end int64 }
	admittedFrames := make(map[string]frameWindow, len(result.OverlayPlan.Items))
	for index, item := range result.OverlayPlan.Items {
		if clipWindow, isIntermediateClip := clipWindows[item.SceneID]; isIntermediateClip {
			// The Master only supports mode=replace. Full-frame phrase/image
			// renders therefore cover the source clip instead of compositing on
			// top of it. Keep the phrase callout, move it just before the clip,
			// and omit the contextual still for these short clip scenes so the
			// entire source video remains visible and its original audio stays
			// aligned with the mixed narration.
			if item.Kind == "image" {
				continue
			}
			if item.Kind == "text_phrase" {
				durationUS := item.EndUSValue() - item.StartUSValue()
				if durationUS <= 0 {
					return nil, fmt.Errorf("final_job clip-scene phrase %q has an empty timing window", item.ID)
				}
				endUS := clipWindow.startUS - frameGuardUS
				startUS := endUS - durationUS
				if startUS < clipWindow.previousStartUS {
					return nil, fmt.Errorf("final_job clip-scene phrase %q cannot fit before clip scene %q", item.ID, item.SceneID)
				}
				item.StartUS = startUS
				item.DurationUS = durationUS
				item.StartMs = startUS / 1000
				item.EndMs = (endUS + 999) / 1000
			}
		}
		artifact, ok := byID[strings.TrimSpace(item.ID)]
		if !ok {
			return nil, fmt.Errorf("final_job overlay %q has no published render artifact", item.ID)
		}
		driveID := strings.TrimSpace(artifact.DriveFileID)
		sha := strings.TrimSpace(artifact.SHA256)
		if driveID == "" || len(sha) != 64 || artifact.SizeBytes <= 0 {
			return nil, fmt.Errorf("final_job overlay %q is not a certified published Drive artifact", item.ID)
		}
		startUS, endUS := item.StartUSValue(), item.EndUSValue()
		if item.Kind == "image" {
			scheduledStartUS, scheduledEndUS, scheduleErr := scheduleFinalJobSceneImage(result, item, frameGuardUS)
			if scheduleErr != nil {
				return nil, scheduleErr
			}
			startUS, endUS = scheduledStartUS, scheduledEndUS
		}
		// Map both endpoints onto the same frame grid. Flooring the start and
		// ceiling the end can turn an exact 120-frame (5 s at 24 fps) asset
		// into a 121-frame window whenever the start falls between frames.
		// The Master then has to extend a packet-copied overlay past its
		// certified tail, which can corrupt the following GOP.
		frameDenominator := int64(1_000_000 * fpsDen)
		startFrame := (startUS*int64(fpsNum) + frameDenominator - 1) / frameDenominator
		endFrame := (endUS*int64(fpsNum) + 1_000_000*int64(fpsDen) - 1) / (1_000_000 * int64(fpsDen))
		if artifact.FrameCount > 0 && endFrame-startFrame > int64(artifact.FrameCount) {
			endFrame = startFrame + int64(artifact.FrameCount)
		}
		if endFrame <= startFrame {
			return nil, fmt.Errorf("final_job overlay %q has an empty frame interval", item.ID)
		}
		// Gate 1 — content identity: the same rendered overlay artifact (same
		// Drive bytes) admitted twice would render the same visual again. The
		// upstream arms dedup on their own keys, which is exactly how the
		// 2026-09-30 duplicate-image incident slipped through.
		if prior, exists := admittedContent[sha]; exists {
			return nil, fmt.Errorf("final_job overlays %q and %q render the same Drive artifact %s; one image one overlay", prior, item.ID, driveID)
		}
		admittedContent[sha] = item.ID // Gate 2 — frame window: the Master replaces the frames of an overlay
		// window, so two admitted overlays sharing even one frame is a
		// guaranteed remote rejection (or a corrupted composite). Project both
		// endpoints onto the exact frame grid the payload carries, matching
		// what the Master will compare.
		for priorID, prior := range admittedFrames {
			if startFrame < prior.end && prior.start < endFrame {
				return nil, fmt.Errorf("final_job overlay %q frame window [%d,%d) intersects overlay %q [%d,%d); the Master rejects intersecting replace overlays", item.ID, startFrame, endFrame, priorID, prior.start, prior.end)
			}
		}
		admittedFrames[item.ID] = frameWindow{start: startFrame, end: endFrame}
		out = append(out, map[string]any{
			"id": item.ID, "asset_id": firstFinalJobValue(artifact.ID, driveID),
			"drive_file_id": driveID, "url": driveFileWebLink(driveID),
			"sha256": sha, "size_bytes": artifact.SizeBytes,
			"start_frame": startFrame, "end_frame": endFrame, "frame_count": endFrame - startFrame,
			"mode": "replace", "z_index": index + 1, "audio_mode": "preserve_final_audio",
		})
	}
	return out, nil
}

type finalJobClipWindow struct {
	startUS         int64
	previousStartUS int64
}

// finalJobIntermediateClipWindows identifies short clip scenes that combine
// original clip audio with generated narration. Replace-mode overlays on
// those windows hide the clip, so their phrase callouts are scheduled just
// before the clip instead.
func finalJobIntermediateClipWindows(result *GenerateResult) map[string]finalJobClipWindow {
	out := make(map[string]finalJobClipWindow)
	if result == nil || result.CanonicalTimeline == nil {
		return out
	}
	segments := result.CanonicalTimeline.Segments
	for i, segment := range segments {
		if segment.FixedMedia {
			continue
		}
		clipAudio := false
		for _, intent := range segment.EffectiveAudioIntents() {
			if intent.Mode == capabilityaudio.AudioClip && !intent.ProtectedOriginalAudio {
				clipAudio = true
				break
			}
		}
		if !clipAudio || segment.TimelineStartUS <= 0 {
			continue
		}
		for previous := i - 1; previous >= 0; previous-- {
			prior := segments[previous]
			if prior.TimelineStartUS+prior.DurationUS != segment.TimelineStartUS || prior.FixedMedia {
				continue
			}
			priorHasClipAudio := false
			for _, intent := range prior.EffectiveAudioIntents() {
				if intent.Mode == capabilityaudio.AudioClip {
					priorHasClipAudio = true
					break
				}
			}
			if !priorHasClipAudio {
				out[segment.ID] = finalJobClipWindow{startUS: segment.TimelineStartUS, previousStartUS: prior.TimelineStartUS}
			}
			break
		}
	}
	return out
}

// scheduleFinalJobSceneImage moves a contextual scene image to the first
// available interval after its planned start. The Master currently accepts
// only mode=replace and rejects intersecting overlay windows, while local
// semantic cards can occupy the same opening beat. Preserve every certified
// overlay and move only the generic scene image within its own scene window.
func scheduleFinalJobSceneImage(result *GenerateResult, image capabilityoverlay.OverlayItem, frameGuardUS int64) (int64, int64, error) {
	start := image.StartUSValue()
	end := image.EndUSValue()
	duration := end - start
	if duration <= 0 {
		return 0, 0, fmt.Errorf("final_job scene image %q has an empty timing window", image.ID)
	}
	sceneStart, sceneEnd := int64(0), int64(0)
	if result != nil {
		if result.CanonicalTimeline != nil {
			for _, scene := range result.CanonicalTimeline.Segments {
				if scene.ID == image.SceneID {
					sceneStart, sceneEnd = scene.TimelineStartUS, scene.TimelineStartUS+scene.DurationUS
					break
				}
			}
		}
		for _, scene := range result.ResolvedScenes {
			if scene.ID == image.SceneID {
				sceneStart, sceneEnd = scene.TimelineStartUS, scene.TimelineStartUS+scene.DurationUS
				break
			}
		}
	}
	if sceneEnd > sceneStart {
		if start < sceneStart {
			start = sceneStart
		}
		if end > sceneEnd {
			end = sceneEnd
			duration = end - start
		}
	}
	if result == nil || result.OverlayPlan == nil {
		return start, start + duration, nil
	}
	occupied := make([][2]int64, 0)
	for _, other := range result.OverlayPlan.Items {
		if other.ID == image.ID || other.SceneID != image.SceneID {
			continue
		}
		otherStart, otherEnd := other.StartUSValue(), other.EndUSValue()
		if otherEnd > otherStart {
			occupied = append(occupied, [2]int64{otherStart, otherEnd})
		}
	}
	sort.Slice(occupied, func(i, j int) bool { return occupied[i][0] < occupied[j][0] })
	candidate := start
	for {
		moved := false
		for _, window := range occupied {
			if candidate < window[1] && window[0] < candidate+duration {
				candidate = window[1] + frameGuardUS
				moved = true
				break
			}
		}
		if !moved {
			break
		}
	}
	if sceneEnd > sceneStart && candidate+duration > sceneEnd {
		return 0, 0, fmt.Errorf("final_job scene image %q cannot fit a non-overlapping %dµs window in scene %q", image.ID, duration, image.SceneID)
	}
	return candidate, candidate + duration, nil
}
