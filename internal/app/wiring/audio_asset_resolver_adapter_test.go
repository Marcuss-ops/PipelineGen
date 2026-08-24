// Package capabilities — audio_asset_resolver_adapter_test.go: regression
// guards for the AudioAssetSource adapter, specifically the certified
// duration handoff. The loop expander can only be deterministic when the
// resolved asset carries DurationUS from the canonical asset registry, so
// the adapter must project Asset.Duration (registry `duration_ms` column)
// into ResolvedAudioAsset.DurationUS — never probe the file, never guess.
package wiring

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// fakeAssetStore is a minimal in-memory asset.Store for the adapter tests.
// It returns the seeded Details for a requested id and is otherwise inert
// (the adapter only ever calls Get).
type fakeAssetStore struct {
	byID map[string]*asset.Details
}

func (f *fakeAssetStore) Get(_ context.Context, id string) (*asset.Details, error) {
	if details, ok := f.byID[id]; ok {
		return details, nil
	}
	return nil, os.ErrNotExist
}

func (f *fakeAssetStore) List(context.Context, asset.Filter) ([]*asset.Summary, error) {
	return nil, nil
}
func (f *fakeAssetStore) Save(context.Context, *asset.Details) error { return nil }
func (f *fakeAssetStore) Delete(context.Context, string) error       { return nil }

// newTestAudioAdapter builds an adapter wired to a no-op materializer.
// The materializer only passes through registered local paths; it fails
// closed when no local path exists and no Drive source is available.
func newTestAudioAdapter(t *testing.T, byID map[string]*asset.Details) *audioAssetSourceAdapter {
	canonical, err := drive.NewCanonicalAssetMaterializer(&testDriveReader{t: t}, t.TempDir(), nil)
	require.NoError(t, err)
	return &audioAssetSourceAdapter{
		assets:    asset.NewService(&fakeAssetStore{byID: byID}, nil),
		canonical: canonical,
	}
}

// testDriveReader is a drive.Reader stub that works for test-only
// registered-local-path usage — the DownloadFile branch is never hit in
// these tests.
type testDriveReader struct{ t *testing.T }

func (r *testDriveReader) DownloadFile(_ context.Context, _ string) (io.ReadCloser, string, error) {
	r.t.Fatal("unexpected DownloadFile call in audio adapter test")
	panic("unreachable")
}
func (r *testDriveReader) GetFileMD5(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (r *testDriveReader) GetFileMeta(_ context.Context, _ string) (*drive.FileMeta, error) {
	return nil, nil
}
func (r *testDriveReader) ListFiles(_ context.Context, _ string) ([]drive.DriveFileInfo, error) {
	return nil, nil
}
func (r *testDriveReader) FindFileByName(_ context.Context, _, _ string) (drive.ExistingFileLookup, error) {
	return drive.ExistingFileLookup{}, nil
}
func (r *testDriveReader) FileIsNotTrashed(_ context.Context, _ string) (bool, error) {
	return true, nil
}
func (r *testDriveReader) FileExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (r *testDriveReader) SearchFiles(_ context.Context, _ string) ([]drive.DriveFileInfo, error) {
	return nil, nil
}

var _ drive.Reader = (*testDriveReader)(nil)

// TestAudioAssetSourceAdapterCarriesCertifiedDuration certifies the
// registry's certified duration (Asset.Duration, loaded from duration_ms)
// reaches ResolvedAudioAsset.DurationUS, alongside the verified local path.
func TestAudioAssetSourceAdapterCarriesCertifiedDuration(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "bgm.m4a")
	require.NoError(t, os.WriteFile(local, []byte("audio"), 0o600))

	audioAsset := &asset.Asset{ID: "bgm_20s", MediaType: asset.MediaTypeAudio, Duration: 20 * time.Second}
	audioAsset.SetLocalPath(local)

	adapter := newTestAudioAdapter(t, map[string]*asset.Details{
		"bgm_20s": {Asset: audioAsset},
	})

	resolved, err := adapter.ResolveAudioAsset(context.Background(), "bgm_20s")
	require.NoError(t, err)
	require.Equal(t, "bgm_20s", resolved.AssetID)
	require.Equal(t, local, resolved.Path)
	require.Equal(t, int64(20_000_000), resolved.DurationUS)
}

// TestAudioAssetSourceAdapter_SoundEffectCarriesCertifiedDuration mirrors
// the BGM case for sound_effect assets: the gate accepts the media type and
// the duration is still projected from the registry.
func TestAudioAssetSourceAdapter_SoundEffectCarriesCertifiedDuration(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "whoosh.m4a")
	require.NoError(t, os.WriteFile(local, []byte("audio"), 0o600))

	sfxAsset := &asset.Asset{ID: "sfx_whoosh", MediaType: asset.MediaTypeSoundEffect, Duration: 2 * time.Second}
	sfxAsset.SetLocalPath(local)

	adapter := newTestAudioAdapter(t, map[string]*asset.Details{
		"sfx_whoosh": {Asset: sfxAsset},
	})

	resolved, err := adapter.ResolveAudioAsset(context.Background(), "sfx_whoosh")
	require.NoError(t, err)
	require.Equal(t, "sfx_whoosh", resolved.AssetID)
	require.Equal(t, int64(2_000_000), resolved.DurationUS)
}

// TestAudioAssetSourceAdapter_ZeroDurationIsPreserved certifies that a
// registry asset without a recorded duration yields DurationUS == 0 (not a
// guessed probe value) — the loop expander fails closed on it downstream.
func TestAudioAssetSourceAdapter_ZeroDurationIsPreserved(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "bgm.m4a")
	require.NoError(t, os.WriteFile(local, []byte("audio"), 0o600))

	audioAsset := &asset.Asset{ID: "bgm_unknown", MediaType: asset.MediaTypeAudio}
	audioAsset.SetLocalPath(local)

	adapter := newTestAudioAdapter(t, map[string]*asset.Details{
		"bgm_unknown": {Asset: audioAsset},
	})

	resolved, err := adapter.ResolveAudioAsset(context.Background(), "bgm_unknown")
	require.NoError(t, err)
	require.Equal(t, int64(0), resolved.DurationUS)
}

// TestAudioAssetSourceAdapter_RejectsNonAudioMediaType certifies the media
// type gate runs before any path resolution: a video clip asset never
// reaches the mixer even if it has a local path.
func TestAudioAssetSourceAdapter_RejectsNonAudioMediaType(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(local, []byte("video"), 0o600))

	clipAsset := &asset.Asset{ID: "clip_1", MediaType: asset.MediaTypeClip, Duration: 10 * time.Second}
	clipAsset.SetLocalPath(local)

	adapter := newTestAudioAdapter(t, map[string]*asset.Details{
		"clip_1": {Asset: clipAsset},
	})

	_, err := adapter.ResolveAudioAsset(context.Background(), "clip_1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "media type")
}

// TestAudioAssetSourceAdapter_UnwiredRegistryFailsClosed certifies the
// nil-registry guard: the adapter must never fabricate a path when the
// registry is not wired.
func TestAudioAssetSourceAdapter_UnwiredRegistryFailsClosed(t *testing.T) {
	adapter := &audioAssetSourceAdapter{assets: nil}
	_, err := adapter.ResolveAudioAsset(context.Background(), "bgm_1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "registry not wired")
}
