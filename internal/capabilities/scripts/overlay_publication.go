package scriptgeneration

import (
	"context"
	"fmt"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

type finalJobFrameWindow struct {
	start, end int64
	sceneID    string
}

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
	JobID string
	// ResultJobID identifies the API job whose result receives the Drive link.
	ResultJobID string
	PlanID      string
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
	OverlayMotionID     string
	OverlayEntityID     string
	OverlayEntityName   string
	OverlayEntityIDs    []string
	OverlayEntityLabels []string
	OverlayText         string
	SourceStartUS       int64
	SourceEndUS         int64
	TargetDurationUS    int64
	OverlayBatchID      string
	OverlayBatchItemIDs []string
	OverlayBatchEndUS   int64
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

// scheduleFinalJobOverlayFrames moves an otherwise-valid replace overlay to
// the first free frame interval after its planned start, constrained to the
// source scene. This preserves every overlay while making the final payload
// conform to Master 51's non-overlapping mode=replace contract.
func scheduleFinalJobOverlayFrames(result *GenerateResult, item capabilityoverlay.OverlayItem, startFrame, endFrame int64, fpsNum, fpsDen int, occupied map[string]finalJobFrameWindow) (int64, int64, error) {
	if fpsNum <= 0 || fpsDen <= 0 || endFrame <= startFrame {
		return 0, 0, fmt.Errorf("final_job overlay %q has an invalid frame window [%d,%d)", item.ID, startFrame, endFrame)
	}
	sceneStartUS, sceneEndUS := int64(-1), int64(-1)
	if result != nil {
		if result.CanonicalTimeline != nil {
			for _, scene := range result.CanonicalTimeline.Segments {
				if scene.ID == item.SceneID {
					sceneStartUS, sceneEndUS = scene.TimelineStartUS, scene.TimelineStartUS+scene.DurationUS
					break
				}
			}
		}
		for _, scene := range result.ResolvedScenes {
			if scene.ID == item.SceneID {
				sceneStartUS, sceneEndUS = scene.TimelineStartUS, scene.TimelineStartUS+scene.DurationUS
				break
			}
		}
	}
	if sceneStartUS < 0 || sceneEndUS <= sceneStartUS {
		return 0, 0, fmt.Errorf("final_job overlay %q overlaps another overlay but scene %q has no valid timeline window", item.ID, item.SceneID)
	}
	fpsNumerator := int64(fpsNum)
	frameDenominator := int64(1_000_000 * fpsDen)
	sceneStartFrame := (sceneStartUS*fpsNumerator + frameDenominator - 1) / frameDenominator
	sceneEndFrame := sceneEndUS * fpsNumerator / frameDenominator
	duration := endFrame - startFrame
	candidate := startFrame
	if candidate < sceneStartFrame {
		candidate = sceneStartFrame
	}
	for candidate+duration <= sceneEndFrame {
		conflictEnd := candidate
		for _, prior := range occupied {
			if prior.sceneID == item.SceneID && candidate < prior.end && prior.start < candidate+duration && prior.end > conflictEnd {
				conflictEnd = prior.end
			}
		}
		if conflictEnd == candidate {
			return candidate, candidate + duration, nil
		}
		candidate = conflictEnd
	}
	return 0, 0, fmt.Errorf("final_job overlay %q cannot fit a non-overlapping %d-frame window in scene %q", item.ID, duration, item.SceneID)
}

// finalJobOverlayIsImageStyle identifies replace-mode visual cards that may
// move within their scene to avoid the Master rejecting intersecting overlay
// frame windows. Entity cards render a full-frame image just like generic image
// overlays, even though their semantic kind is "entity_card".
func finalJobOverlayIsImageStyle(item capabilityoverlay.OverlayItem) bool {
	return item.Kind == "image" || item.Kind == "entity_card"
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
			// A stock-marked clip is audio-only: replace-mode overlays on its
			// window hide the scene's stock video, which is the intended visual,
			// so the scene is never reclassified as an intermediate clip window.
			if intent.Mode == capabilityaudio.AudioClip && !intent.ProtectedOriginalAudio && !intent.StockOriginalAudio {
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
