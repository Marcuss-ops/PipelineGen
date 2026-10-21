package scriptgeneration

import (
	"fmt"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func chapterBulletTestItems(bullets, longPhrases int) []capabilityoverlay.OverlayItem {
	var items []capabilityoverlay.OverlayItem
	for i := 0; i < bullets; i++ {
		items = append(items, capabilityoverlay.OverlayItem{
			ID: fmt.Sprintf("bullet-%02d", i), Kind: "text_phrase",
			SceneID: fmt.Sprintf("scene-%02d", i), StartUS: int64(i) * 10_000_000, DurationUS: 4_000_000,
			Text:   fmt.Sprintf("punto saliente numero %02d della scena", i),
			Params: map[string]any{"editorial_role": capabilityoverlay.ChapterBulletRole},
		})
	}
	for i := 0; i < longPhrases; i++ {
		items = append(items, capabilityoverlay.OverlayItem{
			ID: fmt.Sprintf("long-%02d", i), Kind: "text_phrase",
			SceneID: fmt.Sprintf("scene-%02d", i), StartUS: int64(i)*10_000_000 + 5_000_000, DurationUS: 4_000_000,
			Text: fmt.Sprintf("questa e la frase lunga numero %02d con tutte le parole necessarie", i),
		})
	}
	return items
}

// TestOverlayGroupOfSeparatesBullets proves a chapter card is classified as a
// bullet, never as a long phrase. Without this split the long_phrases cap
// silently spent itself on chapter cards.
func TestOverlayGroupOfSeparatesBullets(t *testing.T) {
	items := chapterBulletTestItems(1, 0)
	if got := overlayGroupOf(items[0]); got != overlayGroupBullets {
		t.Fatalf("chapter bullet group = %q, want %q", got, overlayGroupBullets)
	}
	sceneBullet := capabilityoverlay.OverlayItem{
		Kind: "text_phrase", Text: "un punto saliente", SceneID: "s",
		Params: map[string]any{"editorial_role": "scene_bullet"},
	}
	if got := overlayGroupOf(sceneBullet); got != overlayGroupBullets {
		t.Fatalf("scene bullet group = %q, want %q", got, overlayGroupBullets)
	}
}

// TestBulletsGroupLimitIsIndependent proves the bullets quota does not consume
// the long-phrase allowance: 4 bullets and 10 long phrases all survive.
func TestBulletsGroupLimitIsIndependent(t *testing.T) {
	items := chapterBulletTestItems(12, 12)
	got := applyOverlayGroupLimits(items, map[string]int{
		"long_phrases": 10,
		"bullets":      4,
	})
	bullets, long := 0, 0
	for _, item := range got {
		if overlayGroupOf(item) == overlayGroupBullets {
			bullets++
			continue
		}
		long++
	}
	if bullets != 4 || long != 10 {
		t.Fatalf("group limits: bullets=%d long=%d, want 4+10 (items=%d)", bullets, long, len(got))
	}
}

// TestBulletsGroupLimitZeroDisablesTheLane pins the explicit opt-out.
func TestBulletsGroupLimitZeroDisablesTheLane(t *testing.T) {
	items := chapterBulletTestItems(3, 10)
	got := applyOverlayGroupLimits(items, map[string]int{"bullets": 0})
	for _, item := range got {
		if overlayGroupOf(item) == overlayGroupBullets {
			t.Fatalf("bullets:0 kept %q", item.ID)
		}
	}
	if len(got) != 10 {
		t.Fatalf("bullets:0 kept %d items, want the 10 phrases", len(got))
	}
}

// TestOverlayGroupLimitsAcceptsBulletsKey guards the payload decoder: the
// audit payload sends overlay_group_limits.bullets, which must be a known key.
func TestOverlayGroupLimitsAcceptsBulletsKey(t *testing.T) {
	if err := validateOverlayGroupLimits(map[string]int{"bullets": 4}); err != nil {
		t.Fatalf("bullets group rejected: %v", err)
	}
}

// TestApplyPhraseLaneCapsOpensBulletLane proves the payload switch reaches the
// planner: overlay_group_limits.bullets sets the independent lane, absent keys
// keep the legacy shared lane.
func TestApplyPhraseLaneCapsOpensBulletLane(t *testing.T) {
	config := capabilityoverlay.PlannerConfig{}
	applyPhraseLaneCaps(&config, map[string]int{"short_phrases": 10, "long_phrases": 10, "bullets": 4})
	if config.RunLevelBulletOverlayLimit != 4 {
		t.Fatalf("bullet lane = %d, want 4", config.RunLevelBulletOverlayLimit)
	}
	legacy := capabilityoverlay.PlannerConfig{}
	applyPhraseLaneCaps(&legacy, map[string]int{"short_phrases": 10, "long_phrases": 10})
	if legacy.RunLevelBulletOverlayLimit != 0 {
		t.Fatalf("legacy bullet lane = %d, want 0", legacy.RunLevelBulletOverlayLimit)
	}
}
