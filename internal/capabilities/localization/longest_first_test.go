package localization

import "testing"

func TestLongestFirstLaunchOrderSortsByRenderWindowDescending(t *testing.T) {
	plans := []LocalizedClipPlan{
		{SceneID: "short", DurationMS: 10_000},
		{SceneID: "longest", DurationMS: 60_000},
		{SceneID: "medium", DurationMS: 30_000},
	}
	got := longestFirstLaunchOrder(plans)
	want := []int{1, 2, 0}
	for i, idx := range got {
		if idx != want[i] {
			t.Fatalf("order[%d] = %d (%s), want %d (%s)", i, idx, plans[idx].SceneID, want[i], plans[want[i]].SceneID)
		}
	}
}

func TestLongestFirstLaunchOrderIsStableOnTies(t *testing.T) {
	plans := []LocalizedClipPlan{
		{SceneID: "first", DurationMS: 30_000},
		{SceneID: "second", DurationMS: 30_000},
		{SceneID: "short", DurationMS: 10_000},
	}
	got := longestFirstLaunchOrder(plans)
	want := []int{0, 1, 2}
	for i, idx := range got {
		if idx != want[i] {
			t.Fatalf("order[%d] = %d (%s), want %d (%s) — ties must keep editorial order", i, idx, plans[idx].SceneID, want[i], plans[want[i]].SceneID)
		}
	}
}

func TestLongestFirstLaunchOrderEmptyAndSingle(t *testing.T) {
	if got := longestFirstLaunchOrder(nil); len(got) != 0 {
		t.Fatalf("empty plans: got %v, want empty", got)
	}
	single := longestFirstLaunchOrder([]LocalizedClipPlan{{SceneID: "only", DurationMS: 1}})
	if len(single) != 1 || single[0] != 0 {
		t.Fatalf("single plan: got %v, want [0]", single)
	}
}
