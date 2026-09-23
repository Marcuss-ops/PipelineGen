package videocreate

import "testing"

// TestChildKeys pins the §8 canonical derivation exactly as the plan
// documents it: one root key, deterministic suffixes, 1-based
// zero-padded scene indexes. A silent change here would orphan every
// in-flight child key and break replay convergence.
func TestChildKeys(t *testing.T) {
	root := "calendar:item-123:2026-09-24"
	cases := []struct {
		suffix, scene string
		index         int
		want          string
	}{
		{suffix: "script", want: "calendar:item-123:2026-09-24:script"},
		{suffix: "voiceover", want: "calendar:item-123:2026-09-24:voiceover"},
		{suffix: "assemble", want: "calendar:item-123:2026-09-24:assemble"},
	}
	for _, c := range cases {
		if got := ChildKey(root, c.suffix); got != c.want {
			t.Errorf("ChildKey(%q, %q) = %q, want %q", root, c.suffix, got, c.want)
		}
	}
	scenes := []struct {
		family string
		index  int
		want   string
	}{
		{family: "youtube", index: 1, want: "calendar:item-123:2026-09-24:youtube:scene:001"},
		{family: "youtube", index: 2, want: "calendar:item-123:2026-09-24:youtube:scene:002"},
		{family: "stock", index: 3, want: "calendar:item-123:2026-09-24:stock:scene:003"},
		{family: "render", index: 1, want: "calendar:item-123:2026-09-24:render:scene:001"},
		{family: "render", index: 12, want: "calendar:item-123:2026-09-24:render:scene:012"},
	}
	for _, c := range scenes {
		if got := SceneChildKey(root, c.family, c.index); got != c.want {
			t.Errorf("SceneChildKey(%q, %q, %d) = %q, want %q", root, c.family, c.index, got, c.want)
		}
	}
}

// TestRootKey pins the root derivation: the caller idempotency key IS
// the root (the calendar item identity); an internal submit without one
// falls back to the job id.
func TestRootKey(t *testing.T) {
	if got := RootKey("calendar:item-123:2026-09-24", "job_1"); got != "calendar:item-123:2026-09-24" {
		t.Errorf("RootKey with caller key = %q", got)
	}
	if got := RootKey("  ", "job_1"); got != "job_1" {
		t.Errorf("RootKey fallback = %q, want job_1", got)
	}
}

// TestChildCorrelationID pins the correlation scoping: a child must not
// inherit the parent's running correlation id (the clip.render
// submit→settle lesson) and the scope must be deterministic.
func TestChildCorrelationID(t *testing.T) {
	got := ChildCorrelationID("corr-parent", "script.generate", "root:script")
	want := "corr-parent:vc:root:script"
	if got != want {
		t.Errorf("ChildCorrelationID = %q, want %q", got, want)
	}
	if again := ChildCorrelationID("corr-parent", "script.generate", "root:script"); again != got {
		t.Errorf("ChildCorrelationID not deterministic: %q vs %q", got, again)
	}
}
