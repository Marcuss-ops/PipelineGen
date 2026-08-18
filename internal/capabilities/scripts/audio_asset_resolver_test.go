// Package scriptgeneration — audio_asset_resolver_test.go: regression-guard
// for the BGM/SFX asset_id → ResolvedAudioAssets resolution.
package scriptgeneration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// fakeAudioAssetSource is a deterministic in-memory AudioAssetSource:
// assets[assetID] → resolved asset (path + duration), plus an optional
// failing id.
type fakeAudioAssetSource struct {
	assets   map[string]audio.ResolvedAudioAsset
	failOn   string
	failErr  error
	requests []string
}

func (f *fakeAudioAssetSource) ResolveAudioAsset(_ context.Context, assetID string) (audio.ResolvedAudioAsset, error) {
	f.requests = append(f.requests, assetID)
	if f.failOn == assetID {
		if f.failErr == nil {
			f.failErr = errors.New("boom")
		}
		return audio.ResolvedAudioAsset{}, f.failErr
	}
	if resolved, ok := f.assets[assetID]; ok {
		return resolved, nil
	}
	return audio.ResolvedAudioAsset{}, errors.New("unknown asset " + assetID)
}

func newFakeAudioAssetSource(paths map[string]string) *fakeAudioAssetSource {
	out := &fakeAudioAssetSource{assets: make(map[string]audio.ResolvedAudioAsset, len(paths))}
	for id, path := range paths {
		out.assets[id] = audio.ResolvedAudioAsset{AssetID: id, Path: path}
	}
	return out
}

func TestAudioAssetResolver_ResolvesBGMAgainstSource(t *testing.T) {
	source := newFakeAudioAssetSource(map[string]string{"music_abc": "/data/music/music_abc.m4a"})
	resolver, err := NewAudioAssetResolver(source)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := resolver.Resolve(context.Background(),
		[]scriptpkg.BackgroundMusicIntent{{AssetID: "music_abc"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 {
		t.Fatalf("assets = %+v, want exactly one", assets)
	}
	if assets[0].AssetID != "music_abc" || assets[0].Path != "/data/music/music_abc.m4a" {
		t.Fatalf("asset = %+v", assets[0])
	}
}

// TestAudioAssetResolver_DedupAndOrder certifies the deterministic order:
// BGM layers first (declaration order), then SFX (declaration order), with
// the first occurrence of a duplicated id winning.
func TestAudioAssetResolver_DedupAndOrder(t *testing.T) {
	source := newFakeAudioAssetSource(map[string]string{
		"music_1":    "/p/m1.m4a",
		"music_2":    "/p/m2.m4a",
		"sfx_whoosh": "/p/whoosh.m4a",
		"sfx_boom":   "/p/boom.m4a",
	})
	resolver, err := NewAudioAssetResolver(source)
	if err != nil {
		t.Fatal(err)
	}
	bgm := []scriptpkg.BackgroundMusicIntent{
		{AssetID: "music_1"},
		{AssetID: "music_2"},
		{AssetID: "music_1"}, // duplicate inside BGM
	}
	sfx := []scriptpkg.SoundEffectIntent{
		{AssetID: "sfx_whoosh"},
		{AssetID: "music_2"}, // duplicate across BGM/SFX
		{AssetID: "sfx_boom"},
	}
	assets, err := resolver.Resolve(context.Background(), bgm, sfx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"music_1", "music_2", "sfx_whoosh", "sfx_boom"}
	if len(assets) != len(wantIDs) {
		t.Fatalf("assets = %+v, want %v", assets, wantIDs)
	}
	for i, id := range wantIDs {
		if assets[i].AssetID != id {
			t.Fatalf("assets[%d].AssetID = %q, want %q (order must follow intent order)", i, assets[i].AssetID, id)
		}
		if assets[i].Path != source.assets[id].Path {
			t.Fatalf("assets[%d].Path = %q, want %q", i, assets[i].Path, source.assets[id].Path)
		}
	}
	if len(source.requests) != len(wantIDs) {
		t.Fatalf("source called %d times, want %d (dedup must happen before resolution)", len(source.requests), len(wantIDs))
	}
}

func TestAudioAssetResolver_EmptyIntentsYieldEmptyAssets(t *testing.T) {
	resolver, err := NewAudioAssetResolver(newFakeAudioAssetSource(nil))
	if err != nil {
		t.Fatal(err)
	}
	assets, err := resolver.Resolve(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 0 {
		t.Fatalf("assets = %+v, want none", assets)
	}
}

// TestAudioAssetResolver_BlankAssetIDFailsClosed certifies that an intent
// without an asset_id fails the whole resolution (never a silent skip).
func TestAudioAssetResolver_BlankAssetIDFailsClosed(t *testing.T) {
	resolver, err := NewAudioAssetResolver(newFakeAudioAssetSource(map[string]string{"music_1": "/p/m1.m4a"}))
	if err != nil {
		t.Fatal(err)
	}
	bgm := []scriptpkg.BackgroundMusicIntent{{AssetID: "music_1"}, {AssetID: "  "}}
	if _, err := resolver.Resolve(context.Background(), bgm, nil); err == nil {
		t.Fatal("expected error for blank asset_id")
	}
	sfx := []scriptpkg.SoundEffectIntent{{AssetID: ""}}
	if _, err := resolver.Resolve(context.Background(), nil, sfx); err == nil {
		t.Fatal("expected error for blank sfx asset_id")
	}
}

func TestAudioAssetResolver_UnknownAssetFailsClosed(t *testing.T) {
	resolver, err := NewAudioAssetResolver(newFakeAudioAssetSource(map[string]string{"music_1": "/p/m1.m4a"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(),
		[]scriptpkg.BackgroundMusicIntent{{AssetID: "missing_music"}}, nil,
	); err == nil {
		t.Fatal("expected error for unknown asset_id")
	}
}

func TestAudioAssetResolver_EmptyResolvedPathFailsClosed(t *testing.T) {
	source := newFakeAudioAssetSource(map[string]string{"music_1": ""})
	resolver, err := NewAudioAssetResolver(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(),
		[]scriptpkg.BackgroundMusicIntent{{AssetID: "music_1"}}, nil,
	); err == nil || !strings.Contains(err.Error(), "empty path") {
		t.Fatalf("expected empty-path error, got %v", err)
	}
}

// TestAudioAssetResolver_PropagatesCertifiedDuration certifies that the
// certified DurationUS from the source flows through Resolve untouched —
// the loop expander consumes exactly this value, so it must never be
// stripped or re-probed at the resolver boundary.
func TestAudioAssetResolver_PropagatesCertifiedDuration(t *testing.T) {
	source := newFakeAudioAssetSource(map[string]string{"music_abc": "/m/music.m4a"})
	source.assets["music_abc"] = audio.ResolvedAudioAsset{AssetID: "music_abc", Path: "/m/music.m4a", DurationUS: 20_000_000}

	resolver, err := NewAudioAssetResolver(source)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := resolver.Resolve(context.Background(),
		[]scriptpkg.BackgroundMusicIntent{{AssetID: "music_abc"}}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].DurationUS != 20_000_000 {
		t.Fatalf("resolved asset must carry the certified duration, got %+v", assets)
	}
}

func TestAudioAssetResolver_NilSourceFailsClosed(t *testing.T) {
	if _, err := NewAudioAssetResolver(nil); err == nil {
		t.Fatal("NewAudioAssetResolver(nil) must fail closed")
	}
	var unwired *AudioAssetResolver
	if _, err := unwired.Resolve(context.Background(), nil, nil); err == nil {
		t.Fatal("Resolve on an unwired resolver must fail closed")
	}
}
