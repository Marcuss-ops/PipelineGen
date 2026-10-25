package scriptgeneration

import (
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// defaultSeparateItemRenderWorkers is how many per-item overlay renders may be
// in flight at once when the caller does not tune the pool. 4 (was 2, tuned
// 2026-09-27 from a live queue-wait audit): retuned 2026-10-23 together with
// TICKET-OVERLAY-THROUGHPUT fix 1, which moved the receipt verification (probe + sha256 + full-decode gate, ~0,9 s per item) OFF the render lane.
// One lane then needs ≈1,6 in-flight children to stay fed through the
// off-lane receipt+post tail (golden v12 mix: ~1,7 s of lane service + ~1,0 s
// of tail per item), so the dual-lane topology needs 3,2 → 4. The worker's
// gpu_lanes remains the sole GPU authority: this width pipelines the per-item pre/post chain, it does not raise GPU load.
const defaultSeparateItemRenderWorkers = 4

// overlayItemMaxWidth caps an operator-tuned pool (config.Scripts.SeparateItemRenderWorkers /
// VELOX_SCRIPTS_SEPARATE_ITEM_RENDER_WORKERS). 6: the widest render topology
// certified on this host is 3 lanes (≈5 children by the same 1,6 tail ratio),
// and the in-flight width — not the batch depth — bounds RAM and queue
// pressure.

// overlayMapRenderWidth bounds how many COMPLEX map items may render at once.
// 2 (was 1, retuned 2026-10-23 on the golden v14/v15 window audit): the two
// maps of the golden mix are the window's critical path — measured 28,0 s +
// 20,8 s of map-process wall serialized at width 1 stretched the overlay
// window to 59–64 s while the three GPU lanes finished 20 s earlier. One map's
// LOD raster pyramid peaks at ~787 MB of plates (14 plates, telemetry
// `plates.bytes`); two concurrent pyramids are ~1,6 GB of RAM on a host with
// >20 GB free, and the map frame pipeline's NVENC sessions sit inside the
// certified multi-session baseline (3 render lanes). Width 2 is therefore
// safe on the measured basis; a bigger host may raise it further, a smaller
// one must drop back to 1.
const overlayMapRenderWidth = 2

// Batch depth no longer narrows the pool (retuned 2026-10-23). The 2026-09-27
// depth clamp existed because child queue wait looked like waste at width 4
// behind gpu_lanes=2; the 2026-10-22 golden runs corrected that attribution —
// the overlay window is 100% render service and child queue wait is pipeline
// slack, not waste. With the receipt verification off the lane (fix 1) a pool
// below the feed width starves the lanes and pins throughput at one serialized
// render service (measured 25-29 item/min at width 1; 37,8-41,7 at width 2
// with the receipt still in-lane). The map class keeps its own pool
// (overlayMapRenderWidth, one LOD raster pyramid per map), retuned 2026-10-23.
const overlayItemMaxWidth = 6

// resolveOverlayItemWorkers applies the in-flight width policy: the pool never
// exceeds the caller's requested width and never exceeds overlayItemMaxWidth.
// Batch depth is deliberately not an input — only in-flight width feeds (or
// starves) the render lanes. The rule stays a pure function of the request so
// it is testable without a queue and cannot drift from what the enqueue path
// applies.
func resolveOverlayItemWorkers(requested int) int {
	if requested > overlayItemMaxWidth {
		return overlayItemMaxWidth
	}
	return requested
}

// overlayItemCandidate is one renderable overlay item together with its
// position in the parent plan. The position is preserved because it is part of
// the child plan identity (`<plan>:item:%03d:<id>`), so concurrent work must not
// renumber items.
type overlayItemCandidate struct {
	index  int
	source capoverlay.OverlayItem
}

// overlayItemRenderResult carries both the per-item reference the caller
// publishes in `Items` and the full RenderReference the first item returns for
// backwards compatibility.
type overlayItemRenderResult struct {
	full            RenderReference
	item            OverlayItemRenderReference
	additionalItems []OverlayItemRenderReference
	batch           *OverlayBatchRenderReference
	order           int
}

func overlayBatchAdmissionItem(work overlayBatchWork) capoverlay.OverlayItem {
	return capoverlay.OverlayItem{
		ID: work.id, SceneID: work.sceneID, Kind: "overlay_batch",
		StartUS: work.startUS, DurationUS: work.endUS - work.startUS,
		StartMs: work.startUS / 1000, EndMs: (work.endUS + 999) / 1000,
	}
}

type overlayItemPublicationMetadata struct {
	JobID            string
	ResultJobID      string
	ItemID           string
	BatchID          string
	BatchItemIDs     []string
	ItemKind         string
	MotionID         string
	EntityID         string
	EntityName       string
	EntityIDs        []string
	EntityLabels     []string
	Text             string
	SourceStartUS    int64
	SourceEndUS      int64
	TargetDurationUS int64
	BatchEndUS       int64
}

const (
	overlayItemPaddingUS int64 = 2_000_000
	// shortPhraseAnimationTailUS is the settled hold a phrase clip carries
	// after the spoken window. The clip IS what the final video shows, so the
	// tail is the only lever that makes a short phrase last longer: at 200ms
	// the text was cut the moment the voice stopped, and at two full seconds
	// the glyph stagger sweep stretched across the padding so words sat
	// half-faded (gray) for ~2s before disappearing.
	shortPhraseAnimationTailUS int64 = 1_000_000
	// shortPhraseAnimationMinDurationUS is the shortest a phrase clip may be.
	// A two-second annotation otherwise ends before its entrance is readable.
	shortPhraseAnimationMinDurationUS int64 = 3_500_000
	// maxPhraseOverlayDurationUS bounds one phrase clip so a very long spoken
	// window keeps a sane tail ceiling.
	maxPhraseOverlayDurationUS int64 = 8_000_000
	maxOverlayItemDurationUS   int64 = 5_000_000
	// maxCompositeOverlayDurationUS must cover the largest camera journey the
	// composer can emit: 5 children, consecutive mention anchors up to
	// entityImageMergeGapMS (5s) apart, each slot spaced at least
	// hold (1.5s) + travel (2s) = 3.5s. Worst case offsets reach
	// 4*5000ms = 20000ms, plus the final hold: 21500ms. Capping below
	// that truncates the parent while layers keep their windows, which
	// fails child-plan validation (x4 broke at the 8s cap).
	maxCompositeOverlayDurationUS int64 = 22_000_000
)

// phraseWindowLimitUS is the longest the phrase clip may run before it would
// overlap the next overlay or leave the plan: the distance to the next item's
// start (or the plan end), floored at the certified spoken window so the
// interval itself is never truncated. A phrase with no free space returns the
// spoken window, i.e. it keeps its current length instead of growing.
func phraseWindowLimitUS(parent capoverlay.OverlayPlan, sourceID string, startUS, spokenUS int64) int64 {
	limit := int64(0)
	if next := nextPlanItemStartUS(parent, sourceID, startUS); next > 0 {
		limit = next - startUS
	}
	if parent.DurationMS > 0 {
		if end := parent.DurationMS*1000 - startUS; limit == 0 || end < limit {
			limit = end
		}
	}
	if limit <= 0 || limit < spokenUS {
		return spokenUS
	}
	return limit
}

// nextPlanItemStartUS is the start of the next overlay in the parent plan
// (absolute microseconds), or 0 when the source is the last timed item.
func nextPlanItemStartUS(parent capoverlay.OverlayPlan, sourceID string, startUS int64) int64 {
	next := int64(0)
	for _, item := range parent.Items {
		if item.ID == sourceID {
			continue
		}
		itemStart := item.StartUSValue()
		if itemStart <= startUS {
			continue
		}
		if next == 0 || itemStart < next {
			next = itemStart
		}
	}
	return next
}

// phraseEntranceFrames is the phrase entrance window: one third of the clip in
// output frames, never zero. Deriving it from the final clip keeps the contract
// "the text is settled for the last two thirds of its window" true whatever the
// planner granted the phrase, instead of summing a per-call padding into an
// entrance computed for a shorter clip.
func phraseEntranceFrames(clipUS int64, fpsNum, fpsDen int) int {
	if clipUS <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	denominator := 3 * 1_000_000 * int64(fpsDen)
	frames := (clipUS*int64(fpsNum) + denominator - 1) / denominator
	if frames < 1 {
		frames = 1
	}
	return int(frames)
}

// plannedEnterFrames reads an enter_frames value the planner stamped, whatever
// numeric type it survived a JSON round-trip as. Absent or non-numeric is 0.
func plannedEnterFrames(params map[string]any) int {
	switch value := params["enter_frames"].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func separateOverlayItemPlan(parent capoverlay.OverlayPlan, source capoverlay.OverlayItem, index int) (capoverlay.OverlayPlan, *overlayItemPublicationMetadata, error) {
	startUS, durationUS := source.StartUS, source.DurationUS
	if durationUS <= 0 {
		startUS = source.StartMs * 1000
		durationUS = (source.EndMs - source.StartMs) * 1000
	}
	if startUS < 0 || durationUS <= 0 {
		return capoverlay.OverlayPlan{}, nil, fmt.Errorf("overlay item %q has no positive certified duration", source.ID)
	}
	targetUS := durationUS + overlayItemPaddingUS
	maxDurationUS := maxOverlayItemDurationUS
	if strings.EqualFold(strings.TrimSpace(source.TemplateID), "IMPORTANT_PHRASE") || source.Kind == "text_phrase" || source.Kind == string(capoverlay.KindImportantPhrase) {
		// A short phrase holds a settled tail after the spoken window and never
		// renders shorter than the minimum, so the entrance is visible instead of
		// being cut the moment the voice stops. The entrance is then one third of
		// the FINAL clip: growing the clip slows the animation down instead of
		// leaving settled text parked for the whole tail.
		targetUS = durationUS + shortPhraseAnimationTailUS
		if targetUS < shortPhraseAnimationMinDurationUS {
			targetUS = shortPhraseAnimationMinDurationUS
		}
		if limit := phraseWindowLimitUS(parent, source.ID, startUS, durationUS); limit < targetUS {
			targetUS = limit
		}
		if targetUS > maxPhraseOverlayDurationUS {
			targetUS = maxPhraseOverlayDurationUS
		}
		maxDurationUS = targetUS
		// Overlay items may share MotionParams after shallow copies in the
		// semantic planner. Batch windows are prepared concurrently, so never
		// write the child-specific entrance back into the parent/shared map.
		motionParams := make(map[string]any, len(source.MotionParams)+1)
		for key, value := range source.MotionParams {
			motionParams[key] = value
		}
		source.MotionParams = motionParams
		fpsNum, fpsDen := parent.FPSNum, parent.FPSDen
		if fpsNum <= 0 || fpsDen <= 0 {
			fpsNum, fpsDen = 24, 1
		}
		// Preserve the planner's entrance when the final clip is shorter, while
		// allowing the clip's one-third window to slow an entrance if the phrase
		// can safely hold a longer settled tail.
		entrance := phraseEntranceFrames(targetUS, fpsNum, fpsDen)
		if planned := plannedEnterFrames(source.MotionParams); planned > entrance {
			entrance = planned
		}
		source.MotionParams["enter_frames"] = entrance
	}
	if source.Map != nil {
		// The planner certifies a map window long enough for its camera and all
		// per-pin spoken timing. Preserve that window in the child render; a
		// fixed 8-second clip can truncate later pins and fail map admission.
		targetUS = durationUS
		maxDurationUS = durationUS
	}
	if len(source.ImageLayers) > 0 {
		// A composite's children carry staggered windows relative to the
		// parent. Preserve the entire (up to 5s + 3s mention gap) composition
		// instead of applying the ordinary 5s single-item cap.
		targetUS = durationUS
		maxDurationUS = maxCompositeOverlayDurationUS
	}
	if capoverlay.IsChainItemKind(source.Kind) {
		// A chain item carries its segments at absolute plan times, but the
		// child plan is rebased to start at zero: rebase a COPY of the
		// segments (the parent plan's Params map must not be mutated) so
		// both this validation and the RenderingGen window check see
		// child-local times. Keep the full span: truncating a chain to the
		// 5s single-item cap would push later segments outside the child
		// window and fail validation downstream.
		rebased, ok := capoverlay.RebaseChainSegments(source.Params, startUS)
		if !ok {
			return capoverlay.OverlayPlan{}, nil, fmt.Errorf("overlay item %q chain segments are not decodable", source.ID)
		}
		params := make(map[string]any, len(source.Params))
		for k, v := range source.Params {
			params[k] = v
		}
		params["chain_segments"] = rebased
		source.Params = params
		targetUS = durationUS
		maxDurationUS = durationUS
	}
	if targetUS > maxDurationUS {
		targetUS = maxDurationUS
	}
	if targetUS <= 0 {
		return capoverlay.OverlayPlan{}, nil, fmt.Errorf("overlay item %q has invalid target duration", source.ID)
	}
	child := parent
	child.PlanID = overlayItemChildPlanID(parent.PlanID, index, source.ID)
	child.VideoID = child.PlanID
	child.DurationMS = (targetUS + 999) / 1000
	child.Fingerprint = ""
	child.Items = []capoverlay.OverlayItem{source}
	child.Items[0].StartUS = 0
	child.Items[0].DurationUS = targetUS
	child.Items[0].StartMs = 0
	child.Items[0].EndMs = child.DurationMS
	child.Items[0].RenderKey = ""
	if err := child.Validate(); err != nil {
		return capoverlay.OverlayPlan{}, nil, fmt.Errorf("build child plan for %q: %w", source.ID, err)
	}
	metadata := &overlayItemPublicationMetadata{
		JobID:       textutil.FirstNonEmpty(parent.DriveJobID, parent.PlanID),
		ResultJobID: textutil.FirstNonEmpty(parent.ResultJobID, parent.DriveJobID),
		ItemID:      source.ID, ItemKind: source.Kind, MotionID: source.MotionID, EntityID: source.EntityID,
		Text: source.Text, SourceStartUS: startUS, SourceEndUS: startUS + durationUS,
		TargetDurationUS: targetUS,
	}
	if capoverlay.IsChainItemKind(source.Kind) {
		if segments, ok := capoverlay.DecodeChainSegments(source.Params); ok {
			for _, segment := range segments {
				if segment.Kind != string(capoverlay.KindEntityImage) && segment.Kind != string(capoverlay.KindEntityCard) {
					continue
				}
				metadata.ItemKind = segment.Kind
				metadata.EntityName = strings.TrimSpace(segment.EntityCaption)
				metadata.MotionID = strings.TrimSpace(segment.MotionID)
				break
			}
		}
	}
	if source.EntityRef != nil {
		if metadata.EntityName == "" {
			metadata.EntityName = source.EntityRef.Name
		}
	}
	if source.Map != nil {
		metadata.EntityIDs, metadata.EntityLabels = mapEntityLineage(source.Map)
	}
	return child, metadata, nil
}

func overlayItemChildPlanID(parentPlanID string, index int, itemID string) string {
	full := fmt.Sprintf("%s:item:%03d:%s", parentPlanID, index, itemID)
	const maxPlanIDBytes = 180
	if len(full) <= maxPlanIDBytes {
		return full
	}
	// Byte-identical to the old direct sha256.Sum256 call: the digest SSOT
	// (godlike/06) centralises SHA-256 in internal/kernel/digest and the
	// delegation is verified golden old==new, so persisted plan IDs do not
	// change.
	sum := digest.SHA256String(full)
	const prefixLimit = 160
	var prefix strings.Builder
	for _, r := range full {
		if prefix.Len()+len(string(r)) > prefixLimit {
			break
		}
		prefix.WriteRune(r)
	}
	return strings.TrimRight(prefix.String(), ":-_") + "-" + sum[:16]
}
