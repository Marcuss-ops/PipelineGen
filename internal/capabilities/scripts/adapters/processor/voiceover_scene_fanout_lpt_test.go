package processor

import (
	"context"
	"strings"
	"testing"
)

// TestSceneFanout_LaunchesLongestFirst pins the P7a anti-muda contract: with
// concurrency 1 the execution order equals the launch order, and the longest
// text must go first (TTS cost scales with text length). The returned
// outcomes stay in INPUT order regardless of launch order.
func TestSceneFanout_LaunchesLongestFirst(t *testing.T) {
	exec := &fakeItemExecutor{}
	items := []VoiceoverSceneInput{
		{SceneIndex: 0, Text: "short", Filename: "s0.mp3"},
		{SceneIndex: 1, Text: strings.Repeat("long text ", 50), Filename: "s1.mp3"},
		{SceneIndex: 2, Text: "medium text here", Filename: "s2.mp3"},
	}
	got := RunVoiceoverSceneFanout(context.Background(), exec, "en", items, 1)
	if len(got) != 3 {
		t.Fatalf("outcomes = %d, want 3", len(got))
	}
	// Launch order: longest first.
	if len(exec.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(exec.calls))
	}
	if exec.calls[0].text != items[1].Text || exec.calls[1].text != items[2].Text || exec.calls[2].text != items[0].Text {
		t.Fatalf("launch order is not longest-first: %q, %q, %q",
			truncateForLog(exec.calls[0].text), truncateForLog(exec.calls[1].text), truncateForLog(exec.calls[2].text))
	}
	// Output order: input order preserved.
	for i, outcome := range got {
		if outcome.SceneIndex != i {
			t.Fatalf("outcome %d has SceneIndex %d, want input order", i, outcome.SceneIndex)
		}
		if outcome.Status != "completed" {
			t.Fatalf("outcome %d status = %q, want completed", i, outcome.Status)
		}
	}
}

// TestVoiceoverLaunchOrder pins the permutation: descending length, stable
// on ties (uniform batches dispatch exactly as before).
func TestVoiceoverLaunchOrder(t *testing.T) {
	items := []VoiceoverSceneInput{
		{Text: "b"}, {Text: "aaa"}, {Text: "cc"}, {Text: "d"},
	}
	got := voiceoverLaunchOrder(items)
	want := []int{1, 2, 0, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func truncateForLog(s string) string {
	if len(s) > 20 {
		return s[:20] + "..."
	}
	return s
}
