// Package scriptgeneration — audio_asset_resolver.go is the single
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
package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// AudioAssetResolver resolves the asset_ids referenced by the audio
// intent block into a deduplicated, deterministically ordered
// ResolvedAudioAssets table. Same asset_id used by BGM and SFX yields a
// single entry (the renderer receives one asset per id).
type AudioAssetResolver struct {
	source AudioAssetSource
}

// NewAudioAssetResolver builds the resolver over the given asset source.
// A nil source fails closed at construction: an unwired resolver must
// never silently produce empty paths.
func NewAudioAssetResolver(source AudioAssetSource) (*AudioAssetResolver, error) {
	if source == nil {
		return nil, errors.New("scriptgeneration: audio asset resolver requires an asset source")
	}
	return &AudioAssetResolver{source: source}, nil
}

// Resolve converts the intents' asset_ids into ResolvedAudioAssets.
//
// Order is deterministic and follows intent order: BGM layers first (in
// declaration order), then SFX entries (in declaration order); the first
// occurrence of a duplicated id wins. Fail-closed: a blank asset_id, an
// unresolved id, or an id that resolves to an empty path fails the whole
// resolution — the run never renders a partial asset table.
func (r *AudioAssetResolver) Resolve(ctx context.Context, bgm []scriptpkg.BackgroundMusicIntent, sfx []scriptpkg.SoundEffectIntent) (audio.ResolvedAudioAssets, error) {
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
	assets := make(audio.ResolvedAudioAssets, 0, len(order))
	for _, id := range order {
		publicID := id
		resolved, err := r.source.ResolveAudioAsset(ctx, audio.CanonicalAssetID(id))
		if err != nil {
			return nil, fmt.Errorf("resolve audio asset %q: %w", id, err)
		}
		if strings.TrimSpace(resolved.AssetID) != audio.CanonicalAssetID(id) {
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
