package overlays

import (
	"fmt"
	"sort"
	"strings"
)

// chain_coalesce.go owns the Multi-Animations Runtime: nearby overlays are
// fused into ONE chain item carrying concatenated animations instead of N
// independent items.
//
// Why: every OverlayItem becomes layers inside one Chronon plan, but N items
// still mean N independent entrances/exits with no transition between them.
// A chain item spans first.Start → last.End and carries each segment's own
// text + motion plus the gap between them, so RenderingGen lowers it as one
// continuous shot: segment 1 entrance → camera passes under (fly-under dip)
// → fade reveals segment 2 with its own motion → … — no 1000 popups.
//
// Wire compat: Kind is the new "phrase_chain"/"image_chain" (unknown kinds
// lower as text/image behaviour in RenderingGen), TemplateID is PHRASE_CHAIN
// / IMAGE_CHAIN, and every segment rides Params["chain_segments"] (Params is
// an open map on both contracts, so strict decoders keep accepting the plan).
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
	// TemplatePhraseChain / TemplateImageChain are the registry templates the
	// chains lower through (unknown-template → primitive behaviour + producer
	// kind authoritative in RenderingGen).
	TemplatePhraseChain = "PHRASE_CHAIN"
	TemplateImageChain  = "IMAGE_CHAIN"
	// ChainCameraFlyUnderFade is the Params["chain_camera"] transition every
	// chain requests: the camera dips under during the gap, then fades the
	// next segment in with its own motion.
	ChainCameraFlyUnderFade = "fly_under_fade"
)

const (
	defaultChainGapUS       int64 = 600_000 // 0.6s: fuse when the hole is smaller
	defaultChainMaxSegments       = 8
	maxChainSegmentsHard          = 32
)

// ChainSegment is one animation inside a chain item. StartUS/DurationUS are
// ABSOLUTE plan microseconds (same basis as OverlayItem) so the worker never
// re-derives timing; it only offsets them against the chain start.
type ChainSegment struct {
	Text         string         `json:"text,omitempty"`
	MotionID     string         `json:"motion_id,omitempty"`
	PresetID     string         `json:"preset_id,omitempty"`
	TemplateID   string         `json:"template_id,omitempty"`
	Kind         string         `json:"kind,omitempty"`
	StartUS      int64          `json:"start_us"`
	DurationUS   int64          `json:"duration_us"`
	MotionParams map[string]any `json:"motion_params,omitempty"`
	AssetID      string         `json:"asset_id,omitempty"`
}

// chainConfig resolves the gap + segment ceiling from AnimationCounts (the
// user's chosen configurability surface). Chaining is OPT-IN so the legacy
// path stays intact: it runs only when the caller sets chain_gap_ms > 0 or
// chain_enabled > 0. chain_disabled > 0 forces it off even then.
//
//	chain_gap_ms:       max hole between two overlays to fuse (enables
//	                     chaining; no default — unset means legacy items).
//	chain_enabled:      when > 0, chain with the default 600ms gap.
//	chain_max_segments: max segments per chain item (default 8, hard 32).
//	chain_disabled:     when > 0, coalescing is skipped entirely.
func chainConfig(counts map[string]int) (maxGapUS int64, maxSegments int, disabled bool) {
	if counts == nil || counts["chain_disabled"] > 0 {
		return 0, 0, true
	}
	if v := counts["chain_gap_ms"]; v > 0 {
		maxGapUS = int64(v) * 1000
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

// chainFamily groups chainable kinds: text with text, image with image —
// never mixed, never maps/numbers (their geometry and card budget are
// distinct and must keep their own lowering).
func chainFamily(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "text_phrase", "keyword", "important_word", "quote", "brand_text", "brand-text":
		return "text"
	case "image", "entity_image", "product", "logo":
		return "image"
	default:
		return ""
	}
}

// chainable reports whether an item may enter a chain. Composite entity
// images (multi ImageLayers), maps and numbers/dates keep their own path.
// Image-family items must carry at least one asset: an asset-less card can
// never lower as an image layer, so it stays standalone instead of poisoning
// the whole chain.
func chainable(item OverlayItem) bool {
	family := chainFamily(item.Kind)
	if family == "" {
		return false
	}
	if len(item.ImageLayers) > 0 {
		return false
	}
	if family == "image" && len(item.AssetRefs) == 0 {
		return false
	}
	if item.Map != nil {
		return false
	}
	switch item.TemplateID {
	case "TIMELINE_DATE_CARD", "METRIC_STAT_CARD", "MAP":
		return false
	}
	if item.DurationUS <= 0 && item.EndMs <= item.StartMs {
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
		family := chainFamily(sorted[i].Kind)
		scene := sorted[i].SceneID
		group := []OverlayItem{sorted[i]}
		j := i + 1
		for j < len(sorted) && len(group) < maxSegments {
			next := sorted[j]
			if !chainable(next) || chainFamily(next.Kind) != family || next.SceneID != scene {
				break
			}
			prevEnd := group[len(group)-1].EndUSValue()
			gap := next.StartUSValue() - prevEnd
			if gap < 0 || gap > maxGapUS {
				break
			}
			group = append(group, next)
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

// fuseChain builds the single chain item spanning group[0].Start →
// group[N].End. The first segment's Preset/Motion/Text stay on the item for
// legacy readers; the full animation list rides Params.
func fuseChain(group []OverlayItem, chainIndex int) OverlayItem {
	first := group[0]
	last := group[len(group)-1]
	startUS := first.StartUSValue()
	endUS := last.EndUSValue()
	durationUS := endUS - startUS
	if durationUS <= 0 {
		durationUS = first.EndUSValue() - first.StartUSValue()
	}
	family := chainFamily(first.Kind)
	kind, template := KindPhraseChain, TemplatePhraseChain
	if family == "image" {
		kind, template = KindImageChain, TemplateImageChain
	}
	segments := make([]ChainSegment, 0, len(group))
	motions := make([]string, 0, len(group))
	seenAssets := make(map[string]struct{})
	assets := make([]OverlayAssetRef, 0)
	for _, item := range group {
		seg := ChainSegment{
			Text: item.Text, MotionID: item.MotionID, PresetID: item.PresetID,
			TemplateID: item.TemplateID, Kind: item.Kind,
			StartUS: item.StartUSValue(), DurationUS: item.EndUSValue() - item.StartUSValue(),
			MotionParams: item.MotionParams,
		}
		if len(item.AssetRefs) > 0 {
			seg.AssetID = item.AssetRefs[0].AssetID
		}
		segments = append(segments, seg)
		motions = append(motions, item.MotionID)
		for _, ref := range item.AssetRefs {
			if _, seen := seenAssets[ref.AssetID]; seen || strings.TrimSpace(ref.AssetID) == "" {
				continue
			}
			seenAssets[ref.AssetID] = struct{}{}
			assets = append(assets, ref)
		}
	}
	params := make(map[string]any, len(first.Params)+6)
	for k, v := range first.Params {
		params[k] = v
	}
	params["chain_segments"] = segments
	params["chain_motions"] = motions
	params["chain_count"] = len(segments)
	params["chain_camera"] = ChainCameraFlyUnderFade
	params["chain_gap_us"] = last.StartUSValue() - first.EndUSValue()
	id := fmt.Sprintf("%s:chain-%s:%d", strings.TrimSpace(first.SceneID), family, chainIndex)
	if strings.TrimSpace(first.SceneID) == "" {
		id = fmt.Sprintf("chain-%s:%d", family, chainIndex)
	}
	return OverlayItem{
		ID: id, SceneID: first.SceneID, Kind: kind, TemplateID: template,
		PresetID: first.PresetID, MotionID: first.MotionID, MotionParams: first.MotionParams,
		Text: first.Text, EntityCaption: first.EntityCaption, CaptionMotionID: first.CaptionMotionID,
		StartMs: startUS / 1000, EndMs: (endUS + 999) / 1000,
		StartUS: startUS, DurationUS: durationUS,
		AssetRefs: assets, Params: params,
	}
}

// ValidateChainItem fails closed on a malformed chain: wrong template, fewer
// than two segments, or segments outside the chain window. Called from
// OverlayPlan.Validate so a half-built chain never reaches the worker.
func ValidateChainItem(item OverlayItem) error {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	if kind != KindPhraseChain && kind != KindImageChain {
		return nil
	}
	wantTemplate := TemplatePhraseChain
	if kind == KindImageChain {
		wantTemplate = TemplateImageChain
	}
	if item.TemplateID != wantTemplate {
		return fmt.Errorf("overlay plan: chain item %q kind %q requires template %q", item.ID, item.Kind, wantTemplate)
	}
	raw, ok := item.Params["chain_segments"]
	if !ok {
		return fmt.Errorf("overlay plan: chain item %q carries no chain_segments", item.ID)
	}
	segments, ok := raw.([]ChainSegment)
	if !ok || len(segments) < 2 {
		return fmt.Errorf("overlay plan: chain item %q needs at least two segments", item.ID)
	}
	start, end := item.StartUSValue(), item.EndUSValue()
	for n, seg := range segments {
		if seg.DurationUS <= 0 || seg.StartUS < start || seg.StartUS+seg.DurationUS > end {
			return fmt.Errorf("overlay plan: chain item %q segment %d escapes the chain window", item.ID, n)
		}
		if strings.TrimSpace(seg.MotionID) == "" && strings.TrimSpace(seg.Text) == "" && strings.TrimSpace(seg.AssetID) == "" {
			return fmt.Errorf("overlay plan: chain item %q segment %d carries no content", item.ID, n)
		}
	}
	return nil
}
