package overlayproject

// overlay_sfx_projection.go owns the ONE projection of image-bound SFX cues
// onto the canonical master audio plan.
//
// The cue is no longer written into the RenderingGen payload
// (item.sound_effect): that contract fights the worker's video-only output
// profile, its ValidateOverlay gate, the P4 segment lane and the canonical -an
// encode. Instead the overlay plan — which already carries the final item and
// image-layer windows — is projected into absolute SoundEffectIntent values
// and appended to the CompiledAudioPlan, the same master that already mixes
// voiceover, BGM and clip-start whooshes.
//
// Selection is deterministic: an explicit pin cue wins, otherwise the
// random_sfx_on_image_overlay lane picks a bound whoosh alias from the plan /
// item / content identity. Composite and chain items emit one cue per distinct
// image-layer start, so a fused chain keeps one precise cue per photo.

import (
	"fmt"
	"sort"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	OverlaySFXCueDurationMS = int64(250)
	OverlaySFXCueGainDB     = -18.0
)

// overlaySFXCueWindow is one visible window inside an overlay item, relative
// to the item start. A single-layer image has exactly one window; a composite
// or a fused image chain has one per distinct image-layer start.
type overlaySFXCueWindow struct {
	startOffsetMS int64
	durationMS    int64
}

// OverlaySFXIntentsForPlan projects the cues of one overlay plan. It fails
// closed: a scene that declares more cues than planned image items, a cue that
// does not fit its item, or an invalid cue is a job error, never a silent
// drop. Plans without image items legitimately return no cues.
//
// ONE cue per image item, anchored at the item start: a chain of photos is a
// single clip, so repeating the effect on every chain segment made the same
// sound play several times inside one clip. pool is the indexed SFX library
// projection; nil keeps the built-in whoosh vocabulary.
func OverlaySFXIntentsForPlan(req contract.GenerateRequest, plan *capmodel.OverlayPlan, pool []string) ([]scriptpkg.SoundEffectIntent, error) {
	if plan == nil {
		return nil, nil
	}
	explicit, err := explicitImageSFXCues(req.OverlayImages, plan)
	if err != nil {
		return nil, err
	}
	if len(pool) == 0 {
		pool = scriptpkg.BuiltInWhooshAliases()
	}
	intents := make([]scriptpkg.SoundEffectIntent, 0, len(plan.Items))
	for index := range plan.Items {
		item := &plan.Items[index]
		if !capoverlay.IsImageOverlayItem(*item) {
			continue
		}
		windows := overlaySFXCueWindows(*item)
		if len(windows) == 0 {
			continue
		}
		// The cue belongs to the clip start, not to every internal segment.
		window := windows[0]
		cue := explicit[item.ID]
		durationMS := OverlaySFXCueDurationMS
		gainDB := OverlaySFXCueGainDB
		assetID := ""
		if cue != nil {
			assetID = strings.TrimSpace(cue.AssetID)
			if cue.DurationMS != nil {
				durationMS = *cue.DurationMS
			}
			if cue.GainDB != nil {
				gainDB = *cue.GainDB
			}
		} else if req.RandomSFXOnImageOverlay {
			assetID = selectOverlayWhoosh(pool, plan, *item, window.startOffsetMS)
		}
		if assetID == "" {
			continue
		}
		if durationMS > window.durationMS {
			durationMS = window.durationMS
		}
		if durationMS <= 0 {
			return nil, fmt.Errorf("overlay SFX: item %q has no positive visible window for its cue", item.ID)
		}
		intents = append(intents, scriptpkg.SoundEffectIntent{
			AssetID:    assetID,
			AtMS:       item.StartMs + window.startOffsetMS,
			DurationMS: durationMS,
			GainDB:     gainDB,
		})
	}
	// Chronological order keeps the master plan readable and makes event ids
	// (overlay-sfx-N) stable for a given plan.
	sort.SliceStable(intents, func(i, j int) bool {
		if intents[i].AtMS != intents[j].AtMS {
			return intents[i].AtMS < intents[j].AtMS
		}
		return intents[i].AssetID < intents[j].AssetID
	})
	return intents, nil
}

// explicitImageSFXCues binds each scene's pin cues positionally, in start
// order, exactly like the removed item-level attach did: cue N of a scene goes
// to the Nth planned image item of that scene.
func explicitImageSFXCues(pins []scriptpkg.OverlayImageAsset, plan *capmodel.OverlayPlan) (map[string]*scriptpkg.OverlayImageSoundEffect, error) {
	sceneCues, sceneOrder := groupImageSFXByScene(pins)
	if len(sceneOrder) == 0 {
		return nil, nil
	}
	itemsByScene := ImageOverlayItemsBySceneWhere(plan, func(*capmodel.OverlayItem) bool { return true })
	bound := make(map[string]*scriptpkg.OverlayImageSoundEffect)
	for _, scene := range sceneOrder {
		cues := sceneCues[scene]
		candidates := itemsByScene[scene]
		if len(cues) > len(candidates) {
			return nil, fmt.Errorf("overlay_images.sound_effect: scene %q declares %d cues but the overlay plan has %d image items", scene, len(cues), len(candidates))
		}
		for cueIndex, cue := range cues {
			item := candidates[cueIndex]
			if err := cue.Validate(); err != nil {
				return nil, fmt.Errorf("overlay_images.sound_effect: item %q: %w", item.ID, err)
			}
			bound[item.ID] = &cues[cueIndex]
		}
	}
	return bound, nil
}

// overlaySFXCueWindows lists the visible windows of one image item. A
// single-layer item is visible for the whole item. A composite/chain emits one
// window per distinct image-layer start; layers that start together share one
// window (the widest end), so a simultaneous image stack still gets a single
// cue.
func overlaySFXCueWindows(item capmodel.OverlayItem) []overlaySFXCueWindow {
	itemDurationMS := item.EndMs - item.StartMs
	if itemDurationMS <= 0 {
		return nil
	}
	if len(item.ImageLayers) == 0 {
		return []overlaySFXCueWindow{{startOffsetMS: 0, durationMS: itemDurationMS}}
	}
	starts := make([]int64, 0, len(item.ImageLayers))
	endByStart := make(map[int64]int64, len(item.ImageLayers))
	for _, layer := range item.ImageLayers {
		start := layer.StartMS
		end := layer.EndMS
		if start < 0 {
			start = 0
		}
		if end > itemDurationMS {
			end = itemDurationMS
		}
		if end <= start {
			continue
		}
		current, seen := endByStart[start]
		if !seen {
			starts = append(starts, start)
			endByStart[start] = end
			continue
		}
		if end > current {
			endByStart[start] = end
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	windows := make([]overlaySFXCueWindow, 0, len(starts))
	for _, start := range starts {
		windows = append(windows, overlaySFXCueWindow{startOffsetMS: start, durationMS: endByStart[start] - start})
	}
	return windows
}

// selectOverlayWhoosh picks a cue from stable plan/item/window/content
// identity, so retries are idempotent and different photos normally receive
// different cues. The pool is either the indexed SFX library projection or the
// built-in whoosh aliases when no library is indexed.
func selectOverlayWhoosh(aliases []string, plan *capmodel.OverlayPlan, item capmodel.OverlayItem, startOffsetMS int64) string {
	if len(aliases) == 0 {
		return ""
	}
	hashes := make([]string, 0, len(item.AssetRefs))
	for _, ref := range item.AssetRefs {
		hashes = append(hashes, strings.ToLower(strings.TrimSpace(ref.SHA256)))
	}
	sort.Strings(hashes)
	identity := fmt.Sprintf("%d:%s|%d:%s|%d|%s",
		len(plan.PlanID), plan.PlanID, len(item.ID), item.ID, startOffsetMS, strings.Join(hashes, ","))
	seed := digest.SHA256Sum([]byte(identity))
	return aliases[int(seed[0])%len(aliases)]
}
