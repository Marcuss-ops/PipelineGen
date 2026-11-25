// Package audio — asset_resolver.go is the single
// boundary where the audio intent block's asset_ids become physical
// paths.
//
// The public payload (BGM/SFX intents) references assets by asset_id
// only — never by filesystem path. This resolver turns those ids into
// the canonical ResolvedAudioAssets table (asset_id → verified local
// path) the Rust renderer consumes. It is deliberately decoupled from
// the payload, Drive, and the filesystem: it consumes one narrow port
// (AudioAssetSource) whose concrete adapter owns the registry lookup
// and Drive materialization mechanics.
//
// Pipeline position (audio intent block):
//
//	GenerateRequest (asset_ids)
//	    ↓
//	AudioAssetResolver.Resolve(bgm, sfx)
//	    ↓
//	audio.ResolvedAudioAssets (asset_id → path)
//	    ↓
//	AudioLayerResolver → CompileWithLayers → Rust
package audio

import (
	"context"
	"errors"
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// AssetSource resolves one canonical audio asset ID to verified local bytes.
// The composition adapter owns registry and Drive materialization details.
// The port is declared in scripts/contract (its consumer owns the interface)
// and aliased here so the resolver keeps naming it unqualified.
type AssetSource = contract.AudioAssetSource

// AudioAssetResolver resolves the asset_ids referenced by the audio
// intent block into a deduplicated, deterministically ordered
// ResolvedAudioAssets table. Same asset_id used by BGM and SFX yields a
// single entry (the renderer receives one asset per id).
type AssetResolver struct {
	source AssetSource
}

// NewAudioAssetResolver builds the resolver over the given asset source.
// A nil source fails closed at construction: an unwired resolver must
// never silently produce empty paths.
func NewAssetResolver(source AssetSource) (*AssetResolver, error) {
	if source == nil {
		return nil, errors.New("scriptgeneration: audio asset resolver requires an asset source")
	}
	return &AssetResolver{source: source}, nil
}

// Resolve converts the intents' asset_ids into ResolvedAudioAssets.
//
// Order is deterministic and follows intent order: BGM layers first (in
// declaration order), then SFX entries (in declaration order); the first
// occurrence of a duplicated id wins. Fail-closed: a blank asset_id, an
// unresolved id, or an id that resolves to an empty path fails the whole
// resolution — the run never renders a partial asset table.
func (r *AssetResolver) Resolve(ctx context.Context, bgm []scriptpkg.BackgroundMusicIntent, sfx []scriptpkg.SoundEffectIntent) (capabilityaudio.ResolvedAudioAssets, error) {
	if r == nil || r.source == nil {
		return nil, errors.New("scriptgeneration: audio asset resolver is not wired")
	}
	order := make([]string, 0, len(bgm)+len(sfx))
	seen := make(map[string]struct{}, len(bgm)+len(sfx))
	addID := func(id string) error {
		id = strings.TrimSpace(id)
		if id == "" {
			return errors.New("scriptgeneration: audio intent requires an asset_id")
		}
		if _, ok := seen[id]; ok {
			return nil
		}
		seen[id] = struct{}{}
		order = append(order, id)
		return nil
	}
	for _, b := range bgm {
		if err := addID(b.AssetID); err != nil {
			return nil, err
		}
	}
	for _, s := range sfx {
		if err := addID(s.AssetID); err != nil {
			return nil, err
		}
	}
	assets := make(capabilityaudio.ResolvedAudioAssets, 0, len(order))
	for _, id := range order {
		publicID := id
		resolved, err := r.source.ResolveAudioAsset(ctx, capabilityaudio.CanonicalAssetID(id))
		if err != nil {
			return nil, fmt.Errorf("resolve audio asset %q: %w", id, err)
		}
		if strings.TrimSpace(resolved.AssetID) != capabilityaudio.CanonicalAssetID(id) {
			return nil, fmt.Errorf("audio asset source returned id %q for requested id %q", resolved.AssetID, id)
		}
		if strings.TrimSpace(resolved.Path) == "" {
			return nil, fmt.Errorf("audio asset %q resolved to an empty path", id)
		}
		// Keep the public alias in the sealed plan. This makes the plan stable
		// and readable while the physical registry remains Drive-ID based.
		resolved.AssetID = publicID
		assets = append(assets, resolved)
	}
	return assets, nil
}

// ── merged from random_clip_start_sfx.go ─────────────────────────────────────

// RandomClipStartSFXIntents creates one deterministic random cue at the
// beginning of each real video segment. Synthetic freeze tails are excluded.
// Selecting one alias here lets the normal asset resolver handle the cue.
func RandomClipStartSFXIntents(timeline capabilityaudio.CanonicalTimeline) []scriptpkg.SoundEffectIntent {
	return RandomClipStartSFXIntentsWithPool(timeline, nil)
}

// RandomClipStartSFXIntentsWithPool is RandomClipStartSFXIntents over an
// explicit selection pool (the indexed SFX library projection). An empty pool
// keeps the built-in whoosh vocabulary, so a deployment without an indexed
// library behaves exactly as before.
func RandomClipStartSFXIntentsWithPool(timeline capabilityaudio.CanonicalTimeline, pool []string) []scriptpkg.SoundEffectIntent {
	return RandomClipStartSFXIntentsWithPoolExcluding(timeline, pool, nil)
}

// RandomClipStartSFXIntentsWithPoolExcluding is the random clip-start lane
// with a set of clip IDs that already carry an explicit cue. It keeps the
// explicit choice authoritative while still covering every other real clip.
func RandomClipStartSFXIntentsWithPoolExcluding(timeline capabilityaudio.CanonicalTimeline, pool []string, excludedClipIDs []string) []scriptpkg.SoundEffectIntent {
	excluded := make(map[string]struct{}, len(excludedClipIDs))
	for _, clipID := range excludedClipIDs {
		if clipID = strings.TrimSpace(clipID); clipID != "" {
			excluded[clipID] = struct{}{}
		}
	}
	intents := make([]scriptpkg.SoundEffectIntent, 0)
	for _, scene := range timeline.Segments {
		for _, clip := range scene.EffectiveVideoSegments() {
			if clip.AssetID == "" || clip.Freeze {
				continue
			}
			if _, skip := excluded[strings.TrimSpace(clip.AssetID)]; skip {
				continue
			}
			startUS := scene.TimelineStartUS + clip.TimelineOffsetUS
			if startUS < 0 || startUS >= timeline.DurationUS || startUS%1000 != 0 {
				continue
			}
			intent := scriptpkg.SoundEffectIntent{AtMS: startUS / 1000, GainDB: -6}
			intent.AssetID = RandomSFXID(timeline, len(intents), intent, pool)
			// Keep the cue short and clearly below the voice/music bed.
			// The canonical SFX bus is -10 dB, so -6 dB here yields -16 dB.
			intent.DurationMS = 250
			intents = append(intents, intent)
		}
	}
	return intents
}

// ClipStartSFXIntents expands explicit per-clip cues into absolute SFX
// intents at the canonical clip starts. A cue is fail-closed: a duplicate
// clip_id, an invalid cue, or a clip that never appears as a real video
// segment is a compile error, never a silent drop. The same clip used in two
// scenes intentionally emits one cue at each start.
func ClipStartSFXIntents(timeline capabilityaudio.CanonicalTimeline, cues []scriptpkg.ClipStartSoundEffectIntent) ([]scriptpkg.SoundEffectIntent, error) {
	if len(cues) == 0 {
		return nil, nil
	}
	byClip := make(map[string]scriptpkg.ClipStartSoundEffectIntent, len(cues))
	for i := range cues {
		cue := cues[i]
		if err := cue.Validate(); err != nil {
			return nil, fmt.Errorf("clip-start SFX cue %d: %w", i, err)
		}
		clipID := strings.TrimSpace(cue.ClipID)
		if _, duplicate := byClip[clipID]; duplicate {
			return nil, fmt.Errorf("clip-start SFX cue %d: duplicate clip_id %q", i, clipID)
		}
		byClip[clipID] = cue
	}
	intents := make([]scriptpkg.SoundEffectIntent, 0, len(cues))
	seen := make(map[string]struct{}, len(cues))
	for _, scene := range timeline.Segments {
		for _, clip := range scene.EffectiveVideoSegments() {
			if clip.AssetID == "" || clip.Freeze {
				continue
			}
			clipID := strings.TrimSpace(clip.AssetID)
			cue, ok := byClip[clipID]
			if !ok {
				continue
			}
			startUS := scene.TimelineStartUS + clip.TimelineOffsetUS
			if startUS < 0 || startUS >= timeline.DurationUS {
				return nil, fmt.Errorf("clip-start SFX: clip %q starts at %dus outside the %dus timeline", clipID, startUS, timeline.DurationUS)
			}
			intents = append(intents, scriptpkg.SoundEffectIntent{
				AssetID:    strings.TrimSpace(cue.AssetID),
				AtMS:       startUS / 1000,
				DurationMS: cue.EffectiveDurationMS(),
				GainDB:     cue.EffectiveGainDB(),
			})
			seen[clipID] = struct{}{}
		}
	}
	for clipID := range byClip {
		if _, ok := seen[clipID]; !ok {
			return nil, fmt.Errorf("clip-start SFX: clip %q is not present as a real video segment in the canonical timeline", clipID)
		}
	}
	return intents, nil
}
