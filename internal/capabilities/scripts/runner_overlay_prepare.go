// Package scriptgeneration — runner_overlay_prepare.go owns the read-only
// snapshot and pure compute helpers the overlay.prepare branch uses while
// running concurrently with TTS. The branch never touches the mutable result
// (or result.Scenes) mid-flight: it works from a scene-text snapshot + the
// fenced VidRush segments, computes the grounded annotations and the
// pre-timing OverlayIntents as values, and the caller applies them to the
// durable result after the join. This keeps overlay.prepare from ever waiting
// for TTS or final audio without introducing a data race with the TTS writer.
package scriptgeneration

import (
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// sceneTextSnapshot is the read-only scene identity the prepare branch needs
// (id, index, source-language text, and any pre-existing annotations). It
// deliberately excludes Voiceover and duration fields so the branch can run
// concurrently with TTS without racing the TTS writer. Annotations is a shared
// read-only pointer: the branch never mutates it.
type sceneTextSnapshot struct {
	ID          string
	Index       int
	Text        string
	Annotations *scriptpkg.SceneAnnotations
	ClipIDs     []string
}

// snapshotSceneText projects the source-language text (and any pre-existing
// annotations) of every scene into a lightweight read-only snapshot. It is
// taken before the concurrent TTS and prepare branches start, so both branches
// read stable text and the prepare branch never reads the mutable Scene struct.
func snapshotSceneText(scenes []Scene, language Language) []sceneTextSnapshot {
	out := make([]sceneTextSnapshot, 0, len(scenes))
	for i := range scenes {
		clipIDs := make([]string, 0, len(scenes[i].Clips)+1)
		for _, clip := range scenes[i].Clips {
			if clip != nil && clip.ID != "" {
				clipIDs = append(clipIDs, clip.ID)
			}
		}
		if scenes[i].Clip != nil && scenes[i].Clip.ID != "" {
			clipIDs = append(clipIDs, scenes[i].Clip.ID)
		}
		out = append(out, sceneTextSnapshot{ID: scenes[i].ID, Index: i, Text: scenes[i].Text[language], Annotations: scenes[i].Annotations, ClipIDs: clipIDs})
	}
	return out
}

// vidRushPrepareResult carries the computed outputs of the prepare branch,
// applied to the durable result by the caller after the join.
type vidRushPrepareResult struct {
	segments    []scriptpkg.VidRushSegmentResult
	annotations map[int]*scriptpkg.SceneAnnotations
	intents     []capabilityoverlay.OverlayIntent
}

// vidRushPrepareOutcome is the channel payload the prepare branch sends back
// to the main goroutine. skeletons carries the per-language document
// skeletons rendered at SceneTextReady (the early DocsPrepare pass) for the
// late-bound injection in the document phase; it is nil when the renderer
// does not implement the early/late split.
type vidRushPrepareOutcome struct {
	result     vidRushPrepareResult
	skeletons  map[Language]string
	prefetched *AudioPrefetchResult
	err        error
}

// applyVidRushPrepareProjections projects the prepare branch's outputs onto
// the durable result: the entity aggregate, per-scene annotations + typed
// entity results, compatibility surfaces, and the pre-timing OverlayIntents.
// It is shared by both the serial and parallel orchestration paths so the
// projection contract has exactly one owner.
func applyVidRushPrepareProjections(result *GenerateResult, prepared vidRushPrepareResult) {
	if len(prepared.segments) > 0 {
		result.Entities = aggregateEntityResult(prepared.segments)
		for idx, ann := range prepared.annotations {
			if idx >= 0 && idx < len(result.Scenes) {
				result.Scenes[idx].Annotations = ann
			}
		}
		applySegmentEntityResults(result, prepared.segments)
	}
	projectEntityCompatibility(result, prepared.segments)
	result.OverlayIntents = prepared.intents
}

// computeSegmentEntityAnnotations builds the per-scene grounded annotations
// from the read-only snapshot + the fenced segments, keyed by scene index. It
// mirrors applySegmentEntityAnnotations matching (SceneID, then Position) but
// never mutates result, so it is safe to run concurrently with TTS. Pre-
// existing annotations on the snapshot are preserved and only overridden when
// a matching segment produces grounded annotations (the same precedence the
// sequential flow used), so a run without a VidRush pipeline still plans
// intents from the scenes' own annotations.
func computeSegmentEntityAnnotations(snapshot []sceneTextSnapshot, language Language, segments []scriptpkg.VidRushSegmentResult) map[int]*scriptpkg.SceneAnnotations {
	annotations := make(map[int]*scriptpkg.SceneAnnotations)
	for _, s := range snapshot {
		if s.Annotations != nil {
			annotations[s.Index] = s.Annotations
		}
	}
	indexByID := make(map[string]int, len(snapshot))
	for _, s := range snapshot {
		indexByID[s.ID] = s.Index
	}
	for _, seg := range segments {
		idx, ok := indexByID[seg.SceneID]
		if !ok && seg.SceneID == "" {
			// Fall back to position matching when the segment carries no scene
			// identity (legacy barriers), mirroring applySegmentEntityAnnotations.
			if seg.Position >= 0 && seg.Position < len(snapshot) {
				idx, ok = seg.Position, true
			}
		}
		if !ok {
			continue
		}
		text := strings.TrimSpace(snapshot[idx].Text)
		if text == "" {
			continue
		}
		if ann := projectEntityAnnotations(text, string(language), seg); ann != nil {
			annotations[idx] = ann
		}
	}
	return annotations
}
