package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type clipReaderStub struct {
	clip *asset.Asset
	err  error
}

func (s clipReaderStub) GetClip(context.Context, string) (*asset.Asset, error) {
	return s.clip, s.err
}

type cacheStub struct {
	item   *youtubetypes.ExtractItem
	exists bool
	err    error
}

func (s cacheStub) GetExisting(context.Context, string) (*youtubetypes.ExtractItem, bool, error) {
	return s.item, s.exists, s.err
}

func TestPostgresClipCache_HitHydratesExtractItem(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(local, []byte("video"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	clip := &asset.Asset{
		ID:       "yt_video_10_20_v1",
		Name:     "Galileo",
		Filename: "clip.mp4",
		Duration: 10 * time.Second,
	}
	clip.SetLocalPath(local)
	clip.SetContentHash("abc123")
	clip.SetDriveFileID("drive-1")
	clip.SetDriveLink("https://drive.example/1")

	cache := NewPostgresClipCache(clipReaderStub{clip: clip}, nil)
	item, ok, err := cache.GetExisting(context.Background(), clip.ID)
	if err != nil || !ok || item == nil {
		t.Fatalf("GetExisting = (%v, %v, %v), want hit", item, ok, err)
	}
	if item.LocalPath != local || item.ContentSHA256 != "abc123" || item.DriveFileID != "drive-1" || item.Duration != 10 {
		t.Fatalf("unexpected hydrated item: %+v", item)
	}
	if item.Status != "skipped" {
		t.Fatalf("status = %q, want skipped", item.Status)
	}
}

func TestPostgresClipCache_MissesWhenNoUsableFileReference(t *testing.T) {
	clip := &asset.Asset{ID: "yt_video_10_20_v1"}
	cache := NewPostgresClipCache(clipReaderStub{clip: clip}, nil)
	if _, ok, err := cache.GetExisting(context.Background(), clip.ID); err != nil || ok {
		t.Fatalf("GetExisting = (_, %v, %v), want miss", ok, err)
	}

	clip.SetLocalPath(filepath.Join(t.TempDir(), "missing.mp4"))
	if _, ok, err := cache.GetExisting(context.Background(), clip.ID); err != nil || ok {
		t.Fatalf("missing local file = (_, %v, %v), want miss", ok, err)
	}

	clip.LifecycleState = asset.StateDeleted
	clip.SetDriveFileID("drive-1")
	if _, ok, err := cache.GetExisting(context.Background(), clip.ID); err != nil || ok {
		t.Fatalf("deleted clip = (_, %v, %v), want miss", ok, err)
	}
}

func TestFallbackClipCache_TriesPostgresAfterSQLiteMiss(t *testing.T) {
	secondary := cacheStub{
		item:   &youtubetypes.ExtractItem{ID: "yt_video_10_20_v1", Status: "skipped"},
		exists: true,
	}
	cache := NewFallbackClipCache(cacheStub{}, secondary, nil)
	item, ok, err := cache.GetExisting(context.Background(), "yt_video_10_20_v1")
	if err != nil || !ok || item == nil || item.ID != "yt_video_10_20_v1" {
		t.Fatalf("GetExisting = (%v, %v, %v), want PostgreSQL fallback hit", item, ok, err)
	}
}

func TestFallbackClipCache_BothMiss(t *testing.T) {
	cache := NewFallbackClipCache(cacheStub{}, cacheStub{}, nil)
	var _ youtubeports.ClipCachePort = cache
	if _, ok, err := cache.GetExisting(context.Background(), "missing"); err != nil || ok {
		t.Fatalf("GetExisting = (_, %v, %v), want miss", ok, err)
	}
}
