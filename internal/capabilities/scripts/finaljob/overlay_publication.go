package finaljob

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

type FinalJobFrameWindow struct {
	Start, End int64
	SceneID    string
}

var ErrFinalJobOptionalOverlayNoFreeGap = errors.New("final_job optional overlay has no free scene window")

// OverlayPublicationSpec and OverlayArtifactPublisher are the kernel-owned
// publication contract (internal/kernel/script/renderqueue): the queue adapter
// in internal/platform/renderinggen implements the port without importing this
// capability package. Aliased, not re-declared, so the Drive routing fields and
// the "required side effect" semantics have exactly one owner.
type (
	OverlayPublicationSpec   = renderqueue.OverlayPublicationSpec
	OverlayArtifactPublisher = renderqueue.OverlayArtifactPublisher
)

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
func ScheduleFinalJobOverlayFrames(result *contract.GenerateResult, item capmodel.OverlayItem, startFrame, endFrame int64, fpsNum, fpsDen int, occupied map[string]FinalJobFrameWindow) (int64, int64, error) {
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
	// Both ends round the same way the item windows do (ceil). A floor on the
	// scene end made an item that ends exactly at the scene tail one frame
	// longer than its own scene, so the last phrase of the video could never
	// be placed even in an empty scene.
	sceneStartFrame := (sceneStartUS*fpsNumerator + frameDenominator - 1) / frameDenominator
	sceneEndFrame := (sceneEndUS*fpsNumerator + frameDenominator - 1) / frameDenominator
	duration := endFrame - startFrame
	candidate := startFrame
	if candidate < sceneStartFrame {
		candidate = sceneStartFrame
	}
	for candidate+duration <= sceneEndFrame {
		conflictEnd := candidate
		for _, prior := range occupied {
			if prior.SceneID == item.SceneID && candidate < prior.End && prior.Start < candidate+duration && prior.End > conflictEnd {
				conflictEnd = prior.End
			}
		}
		if conflictEnd == candidate {
			return candidate, candidate + duration, nil
		}
		candidate = conflictEnd
	}
	// A planned start near the scene tail can be blocked by a LATER overlay
	// even when an earlier gap is free. Search backward before failing: the
	// card stays inside its scene and as close as possible to the planned
	// mention instead of rejecting the whole final job (r8: a 5 s date card in
	// scene-05 had no slot after its start although the scene had free time
	// before it).
	candidate = startFrame - duration
	if candidate+duration > sceneEndFrame {
		candidate = sceneEndFrame - duration
	}
	for candidate >= sceneStartFrame {
		conflictStart := candidate + duration
		for _, prior := range occupied {
			if prior.SceneID == item.SceneID && candidate < prior.End && prior.Start < candidate+duration && prior.Start < conflictStart {
				conflictStart = prior.Start
			}
		}
		if conflictStart == candidate+duration {
			return candidate, candidate + duration, nil
		}
		candidate = conflictStart - duration
	}
	// Image, entity, location and number cards may use a shorter source window
	// when the full local render cannot fit beside other replace overlays. Keep
	// at least half a second, then take the largest free gap in the owning
	// scene. Phrase and other editorial text overlays retain their full window.
	if finalJobOverlayCanShrink(item) {
		type frameGap struct{ Start, End int64 }
		windows := make([]frameGap, 0, len(occupied))
		for _, prior := range occupied {
			if prior.SceneID != item.SceneID || prior.End <= sceneStartFrame || prior.Start >= sceneEndFrame {
				continue
			}
			start, end := prior.Start, prior.End
			if start < sceneStartFrame {
				start = sceneStartFrame
			}
			if end > sceneEndFrame {
				end = sceneEndFrame
			}
			windows = append(windows, frameGap{Start: start, End: end})
		}
		sort.Slice(windows, func(i, j int) bool { return windows[i].Start < windows[j].Start })
		minFrames := int64((fpsNum + 2*fpsDen - 1) / (2 * fpsDen)) // half a second
		if minFrames < 1 {
			minFrames = 1
		}
		best := frameGap{Start: sceneStartFrame, End: sceneStartFrame}
		cursor := sceneStartFrame
		considerGap := func(start, end int64) {
			if end-start > best.End-best.Start {
				best = frameGap{Start: start, End: end}
			}
		}
		for _, window := range windows {
			if window.Start > cursor {
				considerGap(cursor, window.Start)
			}
			if window.End > cursor {
				cursor = window.End
			}
		}
		if cursor < sceneEndFrame {
			considerGap(cursor, sceneEndFrame)
		}
		available := best.End - best.Start
		if available >= minFrames {
			if available > duration {
				available = duration
			}
			start := startFrame
			if start < best.Start {
				start = best.Start
			}
			if start+available > best.End {
				start = best.End - available
			}
			return start, start + available, nil
		}
		return 0, 0, fmt.Errorf("%w: overlay %q in scene %q", ErrFinalJobOptionalOverlayNoFreeGap, item.ID, item.SceneID)
	}
	return 0, 0, fmt.Errorf("final_job overlay %q cannot fit a non-overlapping %d-frame window in scene %q (scene frames [%d,%d), occupied: %s)", item.ID, duration, item.SceneID, sceneStartFrame, sceneEndFrame, finalJobSceneOccupancy(occupied, item.SceneID, sceneStartFrame, sceneEndFrame))
}

// finalJobSceneOccupancy renders the admitted windows of one scene for the
// no-space error, so a failed run names the overlay that owns the frames
// instead of only reporting that none are left.
func finalJobSceneOccupancy(occupied map[string]FinalJobFrameWindow, SceneID string, sceneStart, sceneEnd int64) string {
	spans := make([]string, 0, 8)
	for id, window := range occupied {
		if window.SceneID != SceneID || window.End <= sceneStart || window.Start >= sceneEnd {
			continue
		}
		spans = append(spans, fmt.Sprintf("%s[%d,%d)", id, window.Start, window.End))
	}
	sort.Strings(spans)
	if len(spans) > 8 {
		spans = append(spans[:8], fmt.Sprintf("+%d more", len(spans)-8))
	}
	if len(spans) == 0 {
		return "none"
	}
	return strings.Join(spans, ", ")
}

// finalJobOverlayCanShrink identifies replace-mode cards that may move within
// their scene and take a shorter source window to avoid the Master rejecting
// intersecting overlay frame windows. Entity cards render a full-frame image
// just like generic image overlays; number, location and organization cards are
// optional accents too. Phrase text keeps its certified window.
func finalJobOverlayCanShrink(item capmodel.OverlayItem) bool {
	switch item.Kind {
	case "image", "entity_card", "number", "location", "organization":
		return true
	default:
		return false
	}
}

type finalJobClipWindow struct {
	StartUS         int64
	previousStartUS int64
}

// FinalJobIntermediateClipWindows identifies clip scenes that combine
// original clip audio with generated narration. Final-job overlay renders
// use the scene's verified clip as their background, so overlays can retain
// their canonical scene windows without hiding the source picture.
func FinalJobIntermediateClipWindows(result *contract.GenerateResult) map[string]finalJobClipWindow {
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
				out[segment.ID] = finalJobClipWindow{StartUS: segment.TimelineStartUS, previousStartUS: prior.TimelineStartUS}
			}
			break
		}
	}
	return out
}
