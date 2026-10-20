package model

import (
	"encoding/json"
	"fmt"
	capanimreg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"
	"sort"
	"strings"
)

// isShortPhraseRecipe reports whether motionID is one of the registry's
// published exact-cardinality short-phrase recipes. It is the model-side
// projection of the SAME registry groups the selection layer derives its
// phrase.short.words_N pools from — no second copy of the motion list.
func isShortPhraseRecipe(motionID string) bool {
	for wordCount := 1; wordCount <= 5; wordCount++ {
		group := fmt.Sprintf("phrase.short.words_%d", wordCount)
		for _, id := range capanimreg.AnimationCandidates(group) {
			if id == motionID {
				return true
			}
		}
		for _, id := range capanimreg.Canonical().Candidates(group) {
			if id == motionID {
				return true
			}
		}
	}
	return false
}

// chain_coalesce.go owns the nearby-overlay handoff: nearby overlays are
// fused into one chain item, but each segment gets a non-overlapping timed
// window so its motion can be seen before the next one enters.
//
// Why: every OverlayItem becomes layers inside one Chronon plan, but N items
// still mean N independent entrances/exits with no transition between them.
// A chain item spans the scheduled segment windows and carries each
// segment's own text + motion plus a short handoff gap, so RenderingGen lowers
// it as one continuous shot: segment 1 entrance → camera passes under
// (fly-under dip) → fade reveals segment 2 with its own motion.
//
// Wire compat: Kind is the new "phrase_chain"/"image_chain"/"mixed_chain"
// (unknown kinds lower as text/image behaviour in RenderingGen), TemplateID
// is PHRASE_CHAIN / IMAGE_CHAIN / MIXED_CHAIN, and every segment rides
// Params["chain_segments"] (Params is an open map on both contracts, so
// strict decoders keep accepting the plan).
// MotionID/Text/PresetID mirror the FIRST segment so legacy consumers that
// ignore Params still render something sane. Fingerprint/RenderKey hash
// Params, so chains are content-addressed like any item.

const (
	// KindPhraseChain is one fused text overlay: N nearby phrases/keywords/
	// quotes with N concatenated motions, one camera fly-under + fade
	// between each pair.
	KindPhraseChain = "phrase_chain"
	// KindImageChain is one fused image overlay: N nearby images with N
	// concatenated motions and the same fly-under + fade transition.
	KindImageChain = "image_chain"
	// KindMixedChain is one fused overlay mixing nearby text + image + card +
	// number + map items (e.g. image@10s + phrase@12s): one chain item, each
	// segment lowered through its own per-kind compiler with the same
	// fly-under + fade.
	KindMixedChain = "mixed_chain"
	// KindMapChain is one fused overlay of nearby map items: one chain item
	// carrying each map segment's own georeference + motion.
	KindMapChain = "map_chain"
	// TemplatePhraseChain / TemplateImageChain / TemplateMixedChain /
	// TemplateMapChain are the registry templates the chains lower through
	// (unknown-template → primitive behaviour + producer kind authoritative
	// in RenderingGen).
	TemplatePhraseChain = "PHRASE_CHAIN"
	TemplateImageChain  = "IMAGE_CHAIN"
	TemplateMixedChain  = "MIXED_CHAIN"
	TemplateMapChain    = "MAP_CHAIN"
	// ChainCameraFlyUnderFade is the Params["chain_camera"] transition every
	// chain requests: the camera dips under during the gap, then fades the
	// next segment in with its own motion.
	ChainCameraFlyUnderFade = "fly_under_fade"
)

const (
	// 5s: fuse same-scene overlays with at most five seconds of empty
	// timeline between them into one multi-animation chain item.
	defaultChainGapUS         int64 = 5_000_000
	maxChainGapUS             int64 = 5_000_000
	defaultChainMaxSegments         = 8
	maxChainSegmentsHard            = 32
	minChainSegmentDurationUS int64 = 2_500_000
	chainHandoffGapUS         int64 = 400_000
)

// ChainSegment is one animation inside a chain item. StartUS/DurationUS are
// ABSOLUTE plan microseconds (same basis as OverlayItem) so the worker never
// re-derives timing; it only offsets them against the chain start. Map,
// ImageLayers, captions, styles and assets ride per segment so numbers,
// cards, composite images and maps lower through their own compilers.
type ChainSegment struct {
	Text                string              `json:"text,omitempty"`
	GroupID             string              `json:"group_id,omitempty"`
	SubgroupID          string              `json:"subgroup_id,omitempty"`
	MotionID            string              `json:"motion_id,omitempty"`
	PresetID            string              `json:"preset_id,omitempty"`
	ImagePresetID       string              `json:"image_preset_id,omitempty"`
	TemplateID          string              `json:"template_id,omitempty"`
	Kind                string              `json:"kind,omitempty"`
	EntityID            string              `json:"entity_id,omitempty"`
	EntityCaption       string              `json:"entity_caption,omitempty"`
	CaptionMotionID     string              `json:"caption_motion_id,omitempty"`
	CaptionMotionParams map[string]any      `json:"caption_motion_params,omitempty"`
	CaptionFontFamily   string              `json:"caption_font_family,omitempty"`
	CaptionLayout       string              `json:"caption_layout,omitempty"`
	CaptionColor        string              `json:"caption_color,omitempty"`
	EntityStyleID       string              `json:"entity_style_id,omitempty"`
	CameraMotionID      string              `json:"camera_motion_id,omitempty"`
	StartUS             int64               `json:"start_us"`
	DurationUS          int64               `json:"duration_us"`
	MotionParams        map[string]any      `json:"motion_params,omitempty"`
	AssetID             string              `json:"asset_id,omitempty"`
	Assets              []OverlayAssetRef   `json:"assets,omitempty"`
	ImageLayers         []OverlayImageLayer `json:"image_layers,omitempty"`
	Frame               *OverlayItemFrame   `json:"frame,omitempty"`
	Params              map[string]any      `json:"params,omitempty"`
	Style               map[string]any      `json:"style,omitempty"`
	Map                 *MapOverlay         `json:"map,omitempty"`
}

// chainConfig resolves the gap + segment ceiling from AnimationCounts (the
// user's chosen configurability surface). Direct callers with no config keep
// the legacy path; production request building injects a 5000ms default gap.
// chain_disabled > 0 forces chaining off even when a gap is configured.
//
// \tchain_gap_ms:       max empty interval between two overlays to fuse.
// \tchain_enabled:      when > 0, chain with the default 5000ms gap.
// \tchain_max_segments: max segments per chain item (default 8, hard 32).
// \tchain_disabled:     when > 0, coalescing is skipped entirely.
func chainConfig(counts map[string]int) (maxGapUS int64, maxSegments int, disabled bool) {
	if counts == nil || counts["chain_disabled"] > 0 {
		return 0, 0, true
	}
	if v := counts["chain_gap_ms"]; v > 0 {
		// The runtime contract caps the empty interval at five seconds even
		// when a payload asks for more. Bound before multiplying to avoid
		// overflowing int64 for malformed large configuration values.
		if int64(v) > maxChainGapUS/1000 {
			maxGapUS = maxChainGapUS
		} else {
			maxGapUS = int64(v) * 1000
		}
	} else if counts["chain_enabled"] > 0 {
		maxGapUS = defaultChainGapUS
	} else {
		return 0, 0, true
	}
	if v := counts["chain_max_segments"]; v > 0 {
		maxSegments = v
	}
	if maxSegments <= 0 {
		maxSegments = defaultChainMaxSegments
	}
	if maxSegments > maxChainSegmentsHard {
		maxSegments = maxChainSegmentsHard
	}
	return maxGapUS, maxSegments, false
}

// chainFamily groups chainable kinds. Homogeneous groups keep their
// dedicated chain kind; heterogeneous groups fuse as mixed_chain with each
// segment keeping its own lowering. Only video/shape kinds stay out (they own
// the full canvas and have no per-segment chain lowering yet).
func chainFamily(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "text_phrase", "keyword", "important_word", "quote", "brand_text", "brand-text", "number":
		return "text"
	case "image", "entity_image", "product", "logo":
		return "image"
	case "entity_card", "organization", "location", "concept", "lower_third":
		return "card"
	case "map":
		return "map"
	default:
		return ""
	}
}

// chainable reports whether an item may enter a chain. Image-family items
// must carry at least one asset and map items their georeference; anything
// without certified timing stays standalone instead of poisoning the chain.
func chainable(item OverlayItem) bool {
	// A chart comparison is atomic: the label card and its bars share one
	// chart_group_id and PruneOrphanChartBars deletes either side stranded
	// without the other. Fusing the label (kind text_phrase) into a phrase
	// chain entombs it as a plain segment while the bars stay top-level and
	// die as orphans (live C1 2026-10-19: label survived inside a chain,
	// both bars pruned, chart dead). Both stay standalone through
	// coalescing so the group survives or is dropped only as a unit.
	if item.Params != nil && (item.Params["chart_card"] == true || item.Params["chart_bar"] == true) {
		return false
	}
	family := chainFamily(item.Kind)
	if family == "" {
		return false
	}
	if family == "image" && len(item.AssetRefs) == 0 && len(item.ImageLayers) == 0 {
		return false
	}
	if family == "map" && (item.Map == nil || len(item.AssetRefs) == 0) {
		return false
	}
	// SFX currently lowers to the plan-level audio bus. Keep it standalone
	// until the renderer supports segment-scoped audio clips.
	if item.SoundEffect != nil {
		return false
	}
	if item.DurationUS <= 0 && item.EndMs <= item.StartMs {
		return false
	}
	// A short-phrase animation renders as its own clip: fusing several of them
	// into one phrase_chain played multiple short-phrase animations inside a
	// single overlay mp4, which the run owner rejected. Anything else — long
	// copy, images, maps — keeps chaining.
	if family == "text" && isShortPhraseRecipe(item.MotionID) {
		return false
	}
	return true
}

// CoalesceChainedOverlays fuses nearby chainable items into single chain
// items. Items are sorted by (start, id) first so the output is deterministic
// regardless of caller order; non-chainable items pass through untouched and
// in place. A chain never crosses scenes (captions and scene budgets are
// scene-scoped) and never exceeds the configured gap / segment ceiling.
func CoalesceChainedOverlays(items []OverlayItem, counts map[string]int) []OverlayItem {
	maxGapUS, maxSegments, disabled := chainConfig(counts)
	if disabled || len(items) < 2 {
		return items
	}
	sorted := append([]OverlayItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		si, sj := sorted[i].StartUSValue(), sorted[j].StartUSValue()
		if si != sj {
			return si < sj
		}
		return sorted[i].ID < sorted[j].ID
	})
	out := make([]OverlayItem, 0, len(sorted))
	i := 0
	chainIndex := 0
	for i < len(sorted) {
		if !chainable(sorted[i]) {
			out = append(out, sorted[i])
			i++
			continue
		}
		scene := sorted[i].SceneID
		group := []OverlayItem{sorted[i]}
		groupEnd := sorted[i].EndUSValue()
		j := i + 1
		for j < len(sorted) && len(group) < maxSegments {
			next := sorted[j]
			if !chainable(next) || next.SceneID != scene {
				break
			}
			// Fuse nearby AND overlapping items (image@10s + phrase@12s
			// overlap but must still become one chain): the gap is
			// measured against the furthest end seen so far, and any
			// overlap (negative gap) is always fusable.
			gap := next.StartUSValue() - groupEnd
			if gap > maxGapUS {
				break
			}
			group = append(group, next)
			if next.EndUSValue() > groupEnd {
				groupEnd = next.EndUSValue()
			}
			j++
		}
		if len(group) < 2 {
			out = append(out, sorted[i])
			i++
			continue
		}
		out = append(out, fuseChain(group, chainIndex))
		chainIndex++
		i = j
	}
	return out
}

// fuseChain builds the single chain item with segments laid out sequentially.
// Homogeneous text/image/map groups keep their dedicated chain kind;
// anything heterogeneous (or card groups) becomes mixed_chain. The first
// segment's Preset/Motion stay on the item for legacy readers; Text mirrors
// the first non-empty text so legacy readers still render something sane;
// the full animation list rides Params.
func fuseChain(group []OverlayItem, chainIndex int) OverlayItem {
	first := group[0]
	startUS := first.StartUSValue()
	families := make(map[string]bool)
	for _, item := range group {
		if s := item.StartUSValue(); s < startUS {
			startUS = s
		}
		families[chainFamily(item.Kind)] = true
	}
	cursorUS := startUS
	kind, template, family := KindMixedChain, TemplateMixedChain, "mixed"
	if len(families) == 1 {
		switch {
		case families["text"]:
			kind, template, family = KindPhraseChain, TemplatePhraseChain, "text"
		case families["image"]:
			kind, template, family = KindImageChain, TemplateImageChain, "image"
		case families["map"]:
			kind, template, family = KindMapChain, TemplateMapChain, "map"
		}
	}
	segments := make([]ChainSegment, 0, len(group))
	motions := make([]string, 0, len(group))
	seenAssets := make(map[string]struct{})
	assets := make([]OverlayAssetRef, 0)
	for index, item := range group {
		segmentStartUS := item.StartUSValue()
		segmentDurationUS := item.EndUSValue() - item.StartUSValue()
		// Preserve certified phrase timing exactly: extending a short spoken
		// phrase changes its editorial window and can overlap later narration.
		// Image and map segments need a minimum hold for the visual transition.
		if (chainFamily(item.Kind) != "text" || families["map"]) && segmentDurationUS < minChainSegmentDurationUS {
			segmentDurationUS = minChainSegmentDurationUS
		}
		if index > 0 {
			segmentStartUS = cursorUS + chainHandoffGapUS
		}
		seg := ChainSegment{
			Text: item.Text, GroupID: item.GroupID, SubgroupID: item.SubgroupID,
			MotionID: item.MotionID, PresetID: item.PresetID,
			ImagePresetID: item.ImagePresetID, TemplateID: item.TemplateID,
			Kind: item.Kind, EntityID: item.EntityID,
			EntityCaption: item.EntityCaption, CaptionMotionID: item.CaptionMotionID,
			CaptionMotionParams: cloneAnyMap(item.CaptionMotionParams),
			CaptionFontFamily:   item.CaptionFontFamily, CaptionLayout: item.CaptionLayout,
			CaptionColor: item.CaptionColor, EntityStyleID: item.EntityStyleID,
			CameraMotionID: item.CameraMotionID,
			StartUS:        segmentStartUS, DurationUS: segmentDurationUS,
			MotionParams: cloneAnyMap(item.MotionParams),
			Style:        cloneAnyMap(item.Style),
			Assets:       append([]OverlayAssetRef(nil), item.AssetRefs...),
			ImageLayers:  cloneOverlayImageLayers(item.ImageLayers),
			Frame:        cloneOverlayFrame(item.Frame),
			Params:       cloneAnyMap(item.Params),
			Map:          item.Map,
		}
		if len(item.AssetRefs) > 0 {
			seg.AssetID = item.AssetRefs[0].AssetID
		}
		segments = append(segments, seg)
		motions = append(motions, item.MotionID)
		cursorUS = segmentStartUS + segmentDurationUS
		for _, ref := range item.AssetRefs {
			if _, seen := seenAssets[ref.AssetID]; seen || strings.TrimSpace(ref.AssetID) == "" {
				continue
			}
			seenAssets[ref.AssetID] = struct{}{}
			assets = append(assets, ref)
		}
	}
	// Segment params and styles already live on ChainSegment. The container
	// must not inherit the first child's runtime style: RenderingGen validates
	// the container before lowering it, and a mixed chain has no single preset
	// family (for example font_family on an image-first mixed chain).
	params := make(map[string]any, 6)
	recapCount := 0
	for _, segment := range segments {
		if segment.Params["recap"] == true {
			recapCount++
		}
	}
	if recapCount > 0 {
		params["recap_count"] = recapCount
	}
	params["chain_segments"] = segments
	params["chain_motions"] = motions
	params["chain_count"] = len(segments)
	params["chain_camera"] = ChainCameraFlyUnderFade
	params["chain_gap_us"] = chainHandoffGapUS
	id := fmt.Sprintf("%s:chain-%s:%d", strings.TrimSpace(first.SceneID), family, chainIndex)
	if strings.TrimSpace(first.SceneID) == "" {
		id = fmt.Sprintf("chain-%s:%d", family, chainIndex)
	}
	legacyText := first.Text
	if strings.TrimSpace(legacyText) == "" {
		for _, seg := range segments {
			if strings.TrimSpace(seg.Text) != "" {
				legacyText = seg.Text
				break
			}
		}
	}
	return OverlayItem{
		ID: id, SceneID: first.SceneID, Kind: kind, TemplateID: template,
		// A chain is only a container. Its children carry the authoritative
		// preset and motion, and may belong to different preset families.
		// Copying the first child's preset here makes a mixed image/text chain
		// fail root validation before RenderingGen can lower its segments.
		PresetID: "", MotionID: "", MotionParams: nil,
		Text:    legacyText,
		StartMs: startUS / 1000, EndMs: (cursorUS + 999) / 1000,
		StartUS: startUS, DurationUS: cursorUS - startUS,
		AssetRefs: assets, Params: params,
	}
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneOverlayImageLayers(src []OverlayImageLayer) []OverlayImageLayer {
	if src == nil {
		return nil
	}
	out := append([]OverlayImageLayer(nil), src...)
	for i := range out {
		out[i].MotionParams = cloneAnyMap(out[i].MotionParams)
		out[i].Params = cloneAnyMap(out[i].Params)
		out[i].Frame = cloneOverlayFrame(out[i].Frame)
	}
	return out
}

func cloneOverlayFrame(src *OverlayItemFrame) *OverlayItemFrame {
	if src == nil {
		return nil
	}
	out := *src
	if src.Border != nil {
		border := *src.Border
		out.Border = &border
	}
	if src.Shadow != nil {
		shadow := *src.Shadow
		out.Shadow = &shadow
	}
	if src.Stroke != nil {
		stroke := *src.Stroke
		out.Stroke = &stroke
	}
	return &out
}

// IsChainItemKind reports whether an overlay kind is a fused multi-animation
// chain rather than a standalone item.
func IsChainItemKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case KindPhraseChain, KindImageChain, KindMixedChain, KindMapChain:
		return true
	}
	return false
}

// DecodeChainSegments extracts the segment list from an item's Params.
// Params travel through JSON (durable runs, child-plan splits), where the
// list decodes as []any instead of []ChainSegment, so a direct type
// assertion alone rejects healthy deserialized chains. Decode via a JSON
// round-trip instead, exactly like the RenderingGen consumer.
func DecodeChainSegments(params map[string]any) ([]ChainSegment, bool) {
	raw, ok := params["chain_segments"]
	if !ok || raw == nil {
		return nil, false
	}
	if segments, ok := raw.([]ChainSegment); ok {
		return segments, true
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var segments []ChainSegment
	if err := json.Unmarshal(data, &segments); err != nil {
		return nil, false
	}
	return segments, true
}

// RebaseChainSegments returns a copy of the chain segments shifted so the
// chain starts at zero (child-plan-local times). The input Params are never
// mutated: the child split owns the returned slice.
func RebaseChainSegments(params map[string]any, baseUS int64) ([]ChainSegment, bool) {
	segments, ok := DecodeChainSegments(params)
	if !ok {
		return nil, false
	}
	rebased := make([]ChainSegment, 0, len(segments))
	for _, seg := range segments {
		seg.StartUS -= baseUS
		rebased = append(rebased, seg)
	}
	return rebased, true
}

// ValidateChainItem fails closed on a malformed chain: wrong template, fewer
// than two segments, or segments outside the chain window. Called from
// OverlayPlan.Validate so a half-built chain never reaches the worker.
func ValidateChainItem(item OverlayItem) error {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	if kind != KindPhraseChain && kind != KindImageChain && kind != KindMixedChain && kind != KindMapChain {
		return nil
	}
	wantTemplate := TemplatePhraseChain
	switch kind {
	case KindImageChain:
		wantTemplate = TemplateImageChain
	case KindMixedChain:
		wantTemplate = TemplateMixedChain
	case KindMapChain:
		wantTemplate = TemplateMapChain
	}
	if item.TemplateID != wantTemplate {
		return fmt.Errorf("overlay plan: chain item %q kind %q requires template %q", item.ID, item.Kind, wantTemplate)
	}
	segments, ok := DecodeChainSegments(item.Params)
	if !ok {
		return fmt.Errorf("overlay plan: chain item %q carries no chain_segments", item.ID)
	}
	if len(segments) < 2 {
		return fmt.Errorf("overlay plan: chain item %q needs at least two segments", item.ID)
	}
	start, end := item.StartUSValue(), item.EndUSValue()
	lastStartUS := int64(-1)
	for n, seg := range segments {
		if seg.DurationUS <= 0 || seg.StartUS < 0 || seg.StartUS > int64(^uint64(0)>>1)-seg.DurationUS || seg.StartUS < start || seg.StartUS+seg.DurationUS > end {
			return fmt.Errorf("overlay plan: chain item %q segment %d escapes the chain window", item.ID, n)
		}
		if seg.StartUS < lastStartUS {
			return fmt.Errorf("overlay plan: chain item %q segments are not ordered by start time at index %d", item.ID, n)
		}
		lastStartUS = seg.StartUS
		if strings.TrimSpace(seg.MotionID) == "" && strings.TrimSpace(seg.Text) == "" && strings.TrimSpace(seg.AssetID) == "" && len(seg.Assets) == 0 && seg.Map == nil && strings.TrimSpace(seg.EntityCaption) == "" && len(seg.ImageLayers) == 0 {
			return fmt.Errorf("overlay plan: chain item %q segment %d carries no content", item.ID, n)
		}
	}
	return nil
}

// NOTE (2026-10-11): a concurrent in-flight edit referenced
// seg.SoundEffect here, but ChainSegment carries no SoundEffect and
// chainable() already keeps SFX items out of chains, so the check was
// uncompilable dead code. Removed to restore the package build; segment-
// scoped audio remains unsupported by construction.
