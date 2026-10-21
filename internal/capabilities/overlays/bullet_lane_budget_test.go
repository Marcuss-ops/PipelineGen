package overlays

import (
	"fmt"
	"strings"
	"testing"
)

// chapterBulletLaneItems builds 10 short + 10 long narration phrases plus one
// chapter bullet per scene, the exact shape a 16-scene Garlasco-like run has
// when bullets are enabled next to the 10+10 phrase contract.
func chapterBulletLaneItems(scenes int) []OverlayItem {
	var items []OverlayItem
	for scene := 0; scene < scenes; scene++ {
		sceneID := fmt.Sprintf("scene-%02d", scene)
		base := int64(scene) * 20_000_000
		short := fmt.Sprintf("frase breve %02d", scene)
		long := fmt.Sprintf("questa e la frase lunga numero %02d con tutte le parole necessarie", scene)
		bullet := fmt.Sprintf("punto saliente numero %02d estratto dalla narrazione della scena", scene)
		items = append(items,
			OverlayItem{Kind: "text_phrase", Text: short, SceneID: sceneID, StartUS: base, DurationUS: 2_000_000},
			OverlayItem{Kind: "text_phrase", Text: long, SceneID: sceneID, StartUS: base + 3_000_000, DurationUS: 4_000_000},
			OverlayItem{
				Kind: "text_phrase", Text: bullet, SceneID: sceneID,
				StartUS: base + 8_000_000, DurationUS: 4_000_000,
				Params: map[string]any{"editorial_role": ChapterBulletRole},
			},
		)
	}
	return items
}

// TestBulletLaneKeepsShortLongAndBullets proves the three lanes coexist: the
// bullet quota does not spend a long-phrase slot, and the 10+10 phrase
// contract is still fully filled.
func TestBulletLaneKeepsShortLongAndBullets(t *testing.T) {
	items := chapterBulletLaneItems(12)

	out, _ := ApplyEditorialOverlayBudgetWithBulletLane(items, 20, 0, 0, false, 10, 10, 4)

	short, long, bullets := 0, 0, 0
	for _, item := range out {
		role, _ := item.Params["editorial_role"].(string)
		switch {
		case role == ChapterBulletRole:
			bullets++
		case len(strings.Fields(item.Text)) <= 5:
			short++
		default:
			long++
		}
	}
	if short != 10 || long != 10 || bullets != 4 {
		t.Fatalf("lanes: short=%d long=%d bullets=%d, want 10+10+4 (items=%d)", short, long, bullets, len(out))
	}
}

// TestBulletLaneRespectsQuotaAndLegacyPath pins the opt-in boundary: without a
// bullet quota the legacy shared-lane behavior is unchanged, and the lane is
// capped by scene diversity and the explicit ceiling.
func TestBulletLaneRespectsQuotaAndLegacyPath(t *testing.T) {
	items := chapterBulletLaneItems(12)

	legacy, _ := ApplyEditorialOverlayBudgetWithPhraseLanes(items, 20, 0, 0, false, 10, 10)
	legacyBullets := 0
	for _, item := range legacy {
		if role, _ := item.Params["editorial_role"].(string); role == ChapterBulletRole {
			legacyBullets++
		}
	}
	// Legacy: chapter cards compete with long phrases, so the 20-slot budget
	// cannot carry 10 short + 10 long + 12 bullets.
	if legacyBullets == 12 {
		t.Fatalf("legacy path unexpectedly admitted all %d bullets", legacyBullets)
	}

	capped, _ := ApplyEditorialOverlayBudgetWithBulletLane(items, 20, 0, 0, false, 10, 10, 2)
	bullets, scenes := 0, map[string]struct{}{}
	for _, item := range capped {
		if role, _ := item.Params["editorial_role"].(string); role == ChapterBulletRole {
			bullets++
			scenes[item.SceneID] = struct{}{}
		}
	}
	if bullets != 2 || len(scenes) != 2 {
		t.Fatalf("capped bullet lane: bullets=%d scenes=%d, want 2 cards in 2 scenes", bullets, len(scenes))
	}
}

// TestBulletLaneDeduplicatesCopy ensures the same bullet text cannot arrive
// twice from the chapter manifest and consume two quota slots.
func TestBulletLaneDeduplicatesCopy(t *testing.T) {
	items := []OverlayItem{
		{Kind: "text_phrase", Text: "stesso punto saliente della narrazione", SceneID: "a", StartUS: 0, DurationUS: 4_000_000, Params: map[string]any{"editorial_role": ChapterBulletRole}},
		{Kind: "text_phrase", Text: "stesso punto saliente della narrazione", SceneID: "b", StartUS: 5_000_000, DurationUS: 4_000_000, Params: map[string]any{"editorial_role": ChapterBulletRole}},
	}
	out, _ := ApplyEditorialOverlayBudgetWithBulletLane(items, 20, 0, 0, false, 10, 10, 4)
	if len(out) != 1 {
		t.Fatalf("deduplicated bullets: got %d items, want 1", len(out))
	}
}

// TestBulletLaneQuotaGovernsSceneBulletsToo pins the single-quota rule: when
// the lane is enabled, a scene_bullet is admitted by the bullet quota exactly
// like a chapter card. Previously the editorial title lane retained every
// scene_bullet outside the quota, so bullets:2 could still emit three cards.
func TestBulletLaneQuotaGovernsSceneBulletsToo(t *testing.T) {
	items := []OverlayItem{
		{Kind: "text_phrase", Text: "primo punto saliente della narrazione", SceneID: "a", StartUS: 0, DurationUS: 4_000_000, Params: map[string]any{"editorial_role": "scene_bullet"}},
		{Kind: "text_phrase", Text: "secondo punto saliente della narrazione", SceneID: "b", StartUS: 5_000_000, DurationUS: 4_000_000, Params: map[string]any{"editorial_role": ChapterBulletRole}},
		{Kind: "text_phrase", Text: "terzo punto saliente della narrazione", SceneID: "c", StartUS: 10_000_000, DurationUS: 4_000_000, Params: map[string]any{"editorial_role": ChapterBulletRole}},
	}
	out, _ := ApplyEditorialOverlayBudgetWithBulletLane(items, 20, 0, 0, false, 10, 10, 2)
	bullets := 0
	for _, item := range out {
		if role, _ := item.Params["editorial_role"].(string); role == "scene_bullet" || role == ChapterBulletRole {
			bullets++
		}
	}
	if bullets != 2 {
		t.Fatalf("bullet quota=2 admitted %d cards, want exactly 2", bullets)
	}
}
