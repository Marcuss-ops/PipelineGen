package scriptgeneration

// overlay_render_cache_epoch_test.go pins the reuse contract the cache exists
// for: two builds of identical source must name the same epoch. make/build.mk
// stamps BuildTime with `date -u`, so the previous commit@BuildTime epoch
// changed on every recompile and invalidated every stored clip.

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/buildinfo"
)

func TestOverlayRenderCacheEpochIgnoresBuildClock(t *testing.T) {
	first := overlayRenderCacheEpochFromIdentity(buildinfo.Identity{
		GitCommit: "abc1234", GitCommitFull: "abc1234567890abcdef",
		BuildTime: "2026-10-30T08:00:00Z", BinarySHA256: strings.Repeat("1", 64),
	})
	second := overlayRenderCacheEpochFromIdentity(buildinfo.Identity{
		GitCommit: "abc1234", GitCommitFull: "abc1234567890abcdef",
		BuildTime: "2026-10-30T09:30:00Z", BinarySHA256: strings.Repeat("2", 64),
	})
	if first != second {
		t.Fatalf("same source produced different epochs: %q vs %q; a rebuild must not invalidate the cache", first, second)
	}
	if first != "abc1234567890abcdef" {
		t.Fatalf("clean tree epoch = %q, want the full commit", first)
	}
}

func TestOverlayRenderCacheEpochDirtyTreeUsesExecutableDigest(t *testing.T) {
	epoch := overlayRenderCacheEpochFromIdentity(buildinfo.Identity{
		GitCommitFull: "abc1234567890abcdef", GitDirty: true,
		BinarySHA256: strings.Repeat("3", 64),
	})
	if !strings.HasPrefix(epoch, "binary:") {
		t.Fatalf("dirty tree epoch = %q, want the executable digest: different bytes must not share rows", epoch)
	}
	if epoch == "abc1234567890abcdef" {
		t.Fatal("dirty tree must never key on the commit alone")
	}
}

func TestOverlayRenderCacheEpochFallsBackHonestly(t *testing.T) {
	if got := overlayRenderCacheEpochFromIdentity(buildinfo.Identity{}); got != "v1" {
		t.Fatalf("empty identity epoch = %q, want v1", got)
	}
	if got := overlayRenderCacheEpochFromIdentity(buildinfo.Identity{GitDirty: true, GitCommitFull: "abc"}); got != "abc+dirty" {
		t.Fatalf("dirty identity without executable digest = %q, want abc+dirty", got)
	}
}

func TestOverlayRenderCacheEpochEnvOverrideWins(t *testing.T) {
	t.Setenv(OverlayRenderCacheEpochEnv, "epoch-42")
	if got := OverlayRenderCacheEpoch(); got != "epoch-42" {
		t.Fatalf("env override epoch = %q, want epoch-42", got)
	}
}
