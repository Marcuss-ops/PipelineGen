package overlays

import (
	"fmt"
	"strings"
	"testing"
)

// Split phrase lanes must fill both budgets when enough distinct candidates
// exist across scenes: 10 short (2-5 words) and 10 long (6+ words).
func TestApplyEditorialOverlayBudgetWithPhraseLanesFillsTenPlusTen(t *testing.T) {
	var items []OverlayItem
	for scene := 0; scene < 10; scene++ {
		sceneID := fmt.Sprintf("scene-%02d", scene)
		base := int64(scene) * 10_000_000
		short := fmt.Sprintf("breve frase %02d", scene)
		long := fmt.Sprintf("frase lunga numero %02d con abbastanza parole per la corsia lunga", scene)
		items = append(items,
			OverlayItem{Kind: "text_phrase", Text: short, SceneID: sceneID, StartUS: base, DurationUS: 2_000_000},
			OverlayItem{Kind: "text_phrase", Text: long, SceneID: sceneID, StartUS: base + 3_000_000, DurationUS: 4_000_000},
		)
	}
	out, _ := ApplyEditorialOverlayBudgetWithPhraseLanes(items, 20, 0, 0, false, 10, 10)
	short, long := 0, 0
	for _, item := range out {
		if item.Kind != "text_phrase" {
			continue
		}
		if len(strings.Fields(item.Text)) <= 5 {
			short++
		} else {
			long++
		}
	}
	if short != 10 || long != 10 {
		t.Fatalf("phrase lanes: short=%d long=%d, want 10+10", short, long)
	}
}
