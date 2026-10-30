package scriptgeneration

// render_queue_split.go owns the per-scene split of the overlay manifest
// composition (TICKET-OVERLAY-RENDER-PER-SCENE-2026-10-29, Gand A).
//
// Design: the manifest-timeline composition is currently one artifact per
// language plan, whose cache key (and the whole GPU pass) covers the ENTIRE
// plan timeline, so every scene's render waits for every other scene. The
// split submits ONE composition per scene window instead: smaller packed
// artifacts, per-scene cache digests, and N independent GPU round-trips that
// can start while later scenes are still in flight.
//
// Contracts preserved unchanged (the reason shape matters):
//   - manifest entries stay PLAN-ABSOLUTE (in_us/out_us), per
//     OVERLAY-MANIFEST-CONTRACT.md §3 — the split never re-dates a window;
//   - one entry never renders from, or is admitted against, a half-truth:
//     the fitted-caption single-frame gate stays a PLAN-LEVEL admission
//     (admitManifestCaptionWindows over ALL manifest candidates) run once
//     before any GPU work, so the split refuses exactly what the single
//     path refuses;
//   - per-item identity (item_id + content-hash, overlay_manifest_identity.go)
//     is unchanged: the per-item cache namespace is identical in both paths;
//   - the assembled reference keeps ONE RenderReference per language, with
//     N Batches (one per scene) and per-item Artifact rows so
//     editing_timeline's overlayArtifactForItem resolves each item to the
//     artifact that actually owns its frames;
//   - FINAL jobs never take the split: the remote final worker consumes the
//     single-timeline manifest wire today (results.OverlayRender.Manifest +
//     one Artifact), so the split is callers-side gated to non-final jobs
//     until the worker contract lands. render_queue_split stays
//     fail-generic: the caller falls back to the single-timeline submission
//     on ANY error, preserving today's exact behaviour.
//
// Feature gate: OVERLAY_RENDER_PER_SCENE, default ENABLED. The split is the
// certified fast path (one composition and one cache row per scene window);
// only an explicit 0/false/no/off/disabled returns to the single-timeline
// submission, mirroring the render-cache kill switch. Width:
// OVERLAY_RENDER_PER_SCENE_WORKERS (default 3, the certified GPU lane count),
// the submission pipelining bound — RenderingGen's GPU admission remains the
// throughput authority.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// OverlayPlanSplitEnqueuer is the optional capability of an enqueuer that can
// submit one composition per scene window. Capability-checked by the caller;
// enqueuers without it keep the single-timeline submission.
type OverlayPlanSplitEnqueuer interface {
	EnqueueChrononPlanSplit(context.Context, capoverlay.OverlayPlan) (RenderReference, error)
}

// overlayPerSceneSplitEnabled reads the feature gate once per call. It is
// default-ON with an explicit kill switch: the split is already the measured
// path (the golden series runs it enabled), so an unset variable must not
// silently fall back to the 6.5x slower single-timeline submission on any
// entry point that does not source start_server.sh.
func overlayPerSceneSplitEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OVERLAY_RENDER_PER_SCENE"))) {
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return true
	}
}

// overlayPerSceneSplitWorkers is the submission pipelining bound for the
// per-scene compositions of ONE language plan. It is deliberately modest: the
// remote worker's GPU admission is the throughput authority, and extra
// concurrent submissions would only deepen queueing (see backpressure.go).
func overlayPerSceneSplitWorkers() int {
	if n := parseIntEnv("OVERLAY_RENDER_PER_SCENE_WORKERS", 3); n >= 1 {
		return n
	}
	return 3
}

func parseIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return fallback
	}
	return n
}

// sceneRenderGroup is one scene window's renderable manifest subset, in plan
// order (firstIndex drives the group ordering and the assembled reference's
// batch order).
type sceneRenderGroup struct {
	sceneID         string
	groupFirstIndex int
	candidates      []overlayItemCandidate
}

// groupManifestCandidatesByScene splits the manifest candidates by SceneID,
// preserving plan order both within and across the groups (groupFirstIndex
// pins the scene's first plan position; the sort restores canonical order
// after map iteration). An item whose window crosses a scene boundary
// belongs to the scene of its START window — its plan-absolute entry remains
// verbatim either way.
func groupManifestCandidatesByScene(candidates []overlayItemCandidate) []sceneRenderGroup {
	byID := make(map[string]int, 8)
	groups := make([]sceneRenderGroup, 0, 8)
	for _, candidate := range candidates {
		sceneID := strings.TrimSpace(candidate.source.SceneID)
		if index, ok := byID[sceneID]; ok {
			groups[index].candidates = append(groups[index].candidates, candidate)
			continue
		}
		groups = append(groups, sceneRenderGroup{
			sceneID: sceneID, groupFirstIndex: candidate.index,
			candidates: []overlayItemCandidate{candidate},
		})
		byID[sceneID] = len(groups) - 1
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].groupFirstIndex < groups[j].groupFirstIndex
	})
	return groups
}

// groupManifestCandidatesIntoMicroBatches refines the scene groups into
// micro-batches: within each scene, items whose literal windows overlap in
// time form one connected component and always compose together. Components
// never overlap each other in time, so no item's frames can be split across
// two independently rendered compositions, and each item keeps its plan order
// (candidate.index) inside its component.
func groupManifestCandidatesIntoMicroBatches(plan capoverlay.OverlayPlan, candidates []overlayItemCandidate) ([]sceneRenderGroup, error) {
	var out []sceneRenderGroup
	for _, scene := range groupManifestCandidatesByScene(candidates) {
		type windowed struct {
			candidate overlayItemCandidate
			in, out   int64
		}
		windows := make([]windowed, 0, len(scene.candidates))
		for _, candidate := range scene.candidates {
			inUS, outUS, err := candidateLiteralWindow(plan, candidate)
			if err != nil {
				return nil, err
			}
			windows = append(windows, windowed{candidate: candidate, in: inUS, out: outUS})
		}
		sort.SliceStable(windows, func(i, j int) bool {
			if windows[i].in != windows[j].in {
				return windows[i].in < windows[j].in
			}
			return windows[i].candidate.index < windows[j].candidate.index
		})
		current := -1
		var currentOut int64
		for _, w := range windows {
			if current < 0 || w.in >= currentOut {
				out = append(out, sceneRenderGroup{sceneID: scene.sceneID, groupFirstIndex: w.candidate.index})
				current = len(out) - 1
				currentOut = w.out
			} else if w.out > currentOut {
				currentOut = w.out
			}
			out[current].candidates = append(out[current].candidates, w.candidate)
			if w.candidate.index < out[current].groupFirstIndex {
				out[current].groupFirstIndex = w.candidate.index
			}
		}
	}
	for i := range out {
		sort.SliceStable(out[i].candidates, func(a, b int) bool {
			return out[i].candidates[a].index < out[i].candidates[b].index
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].groupFirstIndex < out[j].groupFirstIndex
	})
	return out, nil
}

// planSpansMultipleScenes reports whether the plan's MANIFEST work (ordinary
// items; maps and chains keep their dedicated per-item paths) covers more
// than one scene. Plans whose manifest work is single-scene never split: the
// single submission is then exactly the split's degenerate case.
func planSpansMultipleScenes(plan capoverlay.OverlayPlan) bool {
	manifestCandidates, dedicatedCandidates := partitionOverlayRenderWork(plan)
	if len(manifestCandidates) == 0 && len(dedicatedCandidates) == 0 {
		return false
	}
	scenes := make(map[string]struct{}, 8)
	for _, candidate := range manifestCandidates {
		scenes[strings.TrimSpace(candidate.source.SceneID)] = struct{}{}
	}
	return len(scenes) > 1
}

// EnqueueChrononPlanSplit submits the plan as ONE composition per scene
// window and assembles the per-scene artifacts into a single language
// reference (N Batches, per-item Artifact rows). The DESIGN comment on top of
// this file owns the contract; the caller owns the fallback on any error.
func (e *QueueRenderEnqueuer) EnqueueChrononPlanSplit(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	manifestCandidates, dedicatedCandidates := partitionOverlayRenderWork(plan)
	if len(manifestCandidates) == 0 {
		// Dedicated-only plans take the existing dedicated path verbatim —
		// a split would add nothing but a wrapper, and maps/chains never were
		// timeline compositions.
		if len(dedicatedCandidates) == 0 {
			return RenderReference{}, fmt.Errorf(
				"overlay plan %q has no renderable candidates: declare the background in the plan Background block, not as a legacy BACKGROUND item",
				plan.PlanID)
		}
		return e.enqueueDedicatedOverlayItems(ctx, plan, dedicatedCandidates)
	}
	// PLAN-LEVEL fitted-caption admission BEFORE any GPU work: identical
	// refuse-set to the single path (fail-closed, no half-truth admission).
	if err := admitManifestCaptionWindows(manifestCandidates, plan); err != nil {
		return RenderReference{}, err
	}
	groups, err := groupManifestCandidatesIntoMicroBatches(plan, manifestCandidates)
	if err != nil {
		return RenderReference{}, err
	}
	return runSplitOverlayWork(ctx, plan,
		func(splitCtx context.Context) ([]RenderReference, error) {
			results, err := concurrent.Map(splitCtx, groups, overlayPerSceneSplitWorkers(),
				func(groupCtx context.Context, _ int, group sceneRenderGroup) (RenderReference, error) {
					return e.enqueueOverlayManifestTimeline(groupCtx, plan, group.candidates, nil)
				})
			if err != nil {
				return nil, fmt.Errorf("overlay per-scene split: %w", err)
			}
			if err := splitCtx.Err(); err != nil {
				return nil, err
			}
			return results, nil
		},
		func(dedicatedCtx context.Context) (*RenderReference, error) {
			if len(dedicatedCandidates) == 0 {
				return nil, nil
			}
			ref, err := e.enqueueDedicatedOverlayItems(dedicatedCtx, plan, dedicatedCandidates)
			if err != nil {
				return nil, err
			}
			return &ref, nil
		})
}

// runSplitOverlayWork overlaps independent dedicated items with the scene
// compositions. Both paths use the existing GPU admission authority; the
// split pool and dedicated pool retain their own bounded submission widths.
// No partial reference escapes if either branch fails or is cancelled.
func runSplitOverlayWork(ctx context.Context, plan capoverlay.OverlayPlan,
	scenes func(context.Context) ([]RenderReference, error),
	dedicated func(context.Context) (*RenderReference, error),
) (RenderReference, error) {
	parent, cancel := context.WithCancel(ctx)
	defer cancel()
	group, workCtx := concurrent.WithContext(parent)
	var sceneRefs []RenderReference
	var dedicatedRef *RenderReference
	group.Go("overlay-scene-compositions", func() error {
		var err error
		sceneRefs, err = scenes(workCtx)
		return err
	})
	group.Go("overlay-dedicated-items", func() error {
		var err error
		dedicatedRef, err = dedicated(workCtx)
		return err
	})
	if err := group.Wait(); err != nil {
		return RenderReference{}, err
	}
	if err := ctx.Err(); err != nil {
		return RenderReference{}, err
	}
	return assembleSplitManifestReference(plan, sceneRefs, dedicatedRef)
}

// assembleSplitManifestReference folds the per-scene composition references
// into ONE language reference:
//
//   - Batches: one OverlayBatchRenderReference per scene, in plan order —
//     identified by the scene manifest's own digest (content address), spanning
//     the scene's plan-absolute window, carrying ITS artifact.
//   - Items: one row per plan item in editorial order; every row carries the
//     artifact of the scene that owns its frames (this is what makes
//     editing_timeline's overlayArtifactForItem resolve per item without any
//     consumer change).
//   - Artifact/JobID/Status at the top level: the FIRST scene's identity, so
//     legacy top-level consumers keep a reference shape they already read.
//     Manifest is deliberately nil: the single-timeline manifest wire is the
//     final-job handoff, which stays on the single path until the worker
//     contract lands.
func assembleSplitManifestReference(plan capoverlay.OverlayPlan, sceneRefs []RenderReference, dedicatedRef *RenderReference) (RenderReference, error) {
	if len(sceneRefs) == 0 {
		return RenderReference{}, fmt.Errorf("overlay per-scene split has no scene compositions")
	}
	for _, ref := range sceneRefs {
		if len(ref.Batches) != 1 {
			return RenderReference{}, fmt.Errorf("overlay per-scene split: unexpected batch count %d in a scene composition", len(ref.Batches))
		}
	}
	batches := make([]OverlayBatchRenderReference, 0, len(sceneRefs))
	rows := make([]OverlayItemRenderReference, 0, 16)
	for _, ref := range sceneRefs {
		batch := ref.Batches[0]
		if batch.Manifest == nil {
			batch.Manifest = ref.Manifest
		}
		batches = append(batches, batch)
		for _, row := range ref.Items {
			row.Artifact = ref.Artifact
			rows = append(rows, row)
		}
	}
	sortRenderItemsByPlanOrder(rows, planItemOrderIndex(plan))
	if dedicatedRef != nil {
		rows = mergeRenderItemsByPlanOrder(rows, dedicatedRef.Items, plan)
	}
	first := sceneRefs[0]
	return RenderReference{
		JobID:    first.JobID,
		Status:   first.Status,
		Artifact: first.Artifact,
		Items:    rows,
		Batches:  batches,
		Manifest: nil,
	}, nil
}

// planItemOrderIndex maps item id → plan position for the editorial-order
// restore of the assembled rows.
func planItemOrderIndex(plan capoverlay.OverlayPlan) map[string]int {
	order := make(map[string]int, len(plan.Items))
	for index, item := range plan.Items {
		order[item.ID] = index
	}
	return order
}
