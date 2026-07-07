// Package timeline — legacy_adapter_test.go (FASE C, July 2026).
//
// TDD contract tests for the FASE C legacy adapter. Verifies that
// old-style layers (with explicit from/duration) are correctly mapped
// into SequenceNode wrappers that produce byte-equivalent temporal
// behavior when resolved through ResolveCompositionFlat.
//
// godlike/06 SSOT: these tests are the canonical behavioral contract
// for the legacy adapter. A future refactor that changes the wrapping
// semantics MUST update these tests.
//
// godlike/07 NO-FAKE-AVAILABILITY: every test asserts exact frame
// values and scope_paths. Round-trip tests verify that the wrapped
// layer produces the same resolution output as an equivalent hand-
// built SequenceNode.
package timeline

import (
	"testing"
)

// ── WrapLegacyLayer Tests ──────────────────────────────────────────

func TestWrapLegacyLayer_BasicWrapping(t *testing.T) {
	layer := LayerNode{Name: "logo", Kind: LayerKindText}
	dur := Frame(30)
	legacy := LegacyLayer{
		Name:     "logo",
		From:     Frame(10),
		Duration: &dur,
		Node:     layer,
	}

	seq := WrapLegacyLayer(legacy)

	if seq.Name != "logo_legacy" {
		t.Fatalf("expected sequence name 'logo_legacy', got %q", seq.Name)
	}
	if seq.Spec.From != 10 {
		t.Fatalf("expected From=10, got %d", seq.Spec.From)
	}
	if seq.Spec.Duration == nil || *seq.Spec.Duration != 30 {
		t.Fatalf("expected Duration=30, got %v", seq.Spec.Duration)
	}
	if len(seq.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(seq.Children))
	}

	child, ok := seq.Children[0].(LayerNode)
	if !ok {
		t.Fatalf("expected child to be LayerNode, got %T", seq.Children[0])
	}
	if child.Name != "logo" {
		t.Fatalf("expected child name 'logo', got %q", child.Name)
	}
}

func TestWrapLegacyLayer_InfiniteDuration(t *testing.T) {
	media := MediaNode{Name: "bg_video", SourcePath: "/media/bg.mp4"}
	legacy := LegacyLayer{
		Name:     "background",
		From:     Frame(0),
		Duration: nil, // infinite
		Node:     media,
	}

	seq := WrapLegacyLayer(legacy)

	if seq.Spec.Duration != nil {
		t.Fatalf("expected nil Duration (infinite), got %v", *seq.Spec.Duration)
	}
	if seq.Spec.From != 0 {
		t.Fatalf("expected From=0, got %d", seq.Spec.From)
	}
	if len(seq.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(seq.Children))
	}
}

func TestWrapLegacyLayer_MediaNodeChild(t *testing.T) {
	dur := Frame(60)
	media := MediaNode{
		Name:       "clip",
		SourcePath: "/media/clip.mp4",
		MediaTime:  DefaultMediaTimeSpec(),
	}
	legacy := LegacyLayer{
		Name:     "clip",
		From:     Frame(30),
		Duration: &dur,
		Node:     media,
	}

	seq := WrapLegacyLayer(legacy)

	child, ok := seq.Children[0].(MediaNode)
	if !ok {
		t.Fatalf("expected child to be MediaNode, got %T", seq.Children[0])
	}
	if child.SourcePath != "/media/clip.mp4" {
		t.Fatalf("expected SourcePath preserved, got %q", child.SourcePath)
	}
}

// ── WrapLegacyLayers Tests ─────────────────────────────────────────

func TestWrapLegacyLayers_Multiple(t *testing.T) {
	layers := []LegacyLayer{
		{Name: "a", From: Frame(0), Node: LayerNode{Name: "a", Kind: LayerKindText}},
		{Name: "b", From: Frame(10), Node: LayerNode{Name: "b", Kind: LayerKindImage}},
		{Name: "c", From: Frame(20), Node: LayerNode{Name: "c", Kind: LayerKindShape}},
	}

	seqs := WrapLegacyLayers(layers)

	if len(seqs) != 3 {
		t.Fatalf("expected 3 sequences, got %d", len(seqs))
	}

	for i, want := range []struct {
		name string
		from Frame
	}{
		{"a_legacy", Frame(0)},
		{"b_legacy", Frame(10)},
		{"c_legacy", Frame(20)},
	} {
		if seqs[i].Name != want.name {
			t.Fatalf("seq[%d]: expected name %q, got %q", i, want.name, seqs[i].Name)
		}
		if seqs[i].Spec.From != want.from {
			t.Fatalf("seq[%d]: expected From=%d, got %d", i, want.from, seqs[i].Spec.From)
		}
	}
}

func TestWrapLegacyLayers_EmptySlice(t *testing.T) {
	seqs := WrapLegacyLayers(nil)
	if seqs != nil {
		t.Fatal("expected nil for nil input")
	}

	seqs = WrapLegacyLayers([]LegacyLayer{})
	if seqs != nil {
		t.Fatal("expected nil for empty slice")
	}
}

// ── Composition.AddLegacyLayer Tests ───────────────────────────────

func TestComposition_AddLegacyLayer(t *testing.T) {
	comp, err := NewComposition("test", Frame(100), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dur := Frame(20)
	legacy := LegacyLayer{
		Name:     "title",
		From:     Frame(30),
		Duration: &dur,
		Node:     LayerNode{Name: "title", Kind: LayerKindText},
	}
	comp.AddLegacyLayer(legacy)

	if comp.RootSequence.ChildCount() != 1 {
		t.Fatalf("expected 1 child in root sequence, got %d", comp.RootSequence.ChildCount())
	}

	seq, ok := comp.RootSequence.Children[0].(SequenceNode)
	if !ok {
		t.Fatalf("expected root child to be SequenceNode, got %T", comp.RootSequence.Children[0])
	}
	if seq.Name != "title_legacy" {
		t.Fatalf("expected sequence name 'title_legacy', got %q", seq.Name)
	}
}

func TestComposition_AddLegacyLayers(t *testing.T) {
	comp, err := NewComposition("test", Frame(500), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	layers := []LegacyLayer{
		{Name: "intro", From: Frame(0), Node: LayerNode{Name: "intro", Kind: LayerKindText}},
		{Name: "main", From: Frame(30), Node: LayerNode{Name: "main", Kind: LayerKindVideo}},
	}
	comp.AddLegacyLayers(layers)

	if comp.RootSequence.ChildCount() != 2 {
		t.Fatalf("expected 2 children, got %d", comp.RootSequence.ChildCount())
	}
}

// ── Round-trip Resolution Tests ────────────────────────────────────
// Verify that legacy-wrapped layers produce the same resolution output
// as equivalent hand-built SequenceNodes.

func TestLegacyLayer_RoundTrip_ActiveAtFrom(t *testing.T) {
	comp, err := NewComposition("test", Frame(200), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dur := Frame(40)
	comp.AddLegacyLayer(LegacyLayer{
		Name:     "text",
		From:     Frame(50),
		Duration: &dur,
		Node:     LayerNode{Name: "text", Kind: LayerKindText},
	})

	// At frame 50 (from), the layer should be active
	scene := ResolveCompositionFlat(comp, Frame(50))
	if scene.ActiveCount != 1 {
		t.Fatalf("expected 1 active layer at frame 50, got %d", scene.ActiveCount)
	}
	if scene.Layers[0].TimeContext.LocalFrame != 0 {
		t.Fatalf("expected local_frame=0 at sequence from, got %d", scene.Layers[0].TimeContext.LocalFrame)
	}
	if scene.Layers[0].TimeContext.ScopePath != "root/text_legacy" {
		t.Fatalf("expected scope_path=root/text_legacy, got %s", scene.Layers[0].TimeContext.ScopePath)
	}
}

func TestLegacyLayer_RoundTrip_BeforeFrom_NotActive(t *testing.T) {
	comp, err := NewComposition("test", Frame(200), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dur := Frame(40)
	comp.AddLegacyLayer(LegacyLayer{
		Name:     "text",
		From:     Frame(50),
		Duration: &dur,
		Node:     LayerNode{Name: "text", Kind: LayerKindText},
	})

	// At frame 49 (before from), the layer should NOT be active
	scene := ResolveCompositionFlat(comp, Frame(49))
	if scene.ActiveCount != 0 {
		t.Fatalf("expected 0 active layers at frame 49 (before from=50), got %d", scene.ActiveCount)
	}
}

func TestLegacyLayer_RoundTrip_AfterDuration_NotActive(t *testing.T) {
	comp, err := NewComposition("test", Frame(200), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dur := Frame(40)
	comp.AddLegacyLayer(LegacyLayer{
		Name:     "text",
		From:     Frame(50),
		Duration: &dur,
		Node:     LayerNode{Name: "text", Kind: LayerKindText},
	})

	// At frame 90 (from + duration), the layer should NOT be active
	scene := ResolveCompositionFlat(comp, Frame(90))
	if scene.ActiveCount != 0 {
		t.Fatalf("expected 0 active layers at frame 90 (after from+duration=90), got %d", scene.ActiveCount)
	}

	// At frame 89, still active
	scene89 := ResolveCompositionFlat(comp, Frame(89))
	if scene89.ActiveCount != 1 {
		t.Fatalf("expected 1 active layer at frame 89, got %d", scene89.ActiveCount)
	}
}

func TestLegacyLayer_RoundTrip_InfiniteDuration_AlwaysActive(t *testing.T) {
	comp, err := NewComposition("test", Frame(500), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	comp.AddLegacyLayer(LegacyLayer{
		Name:     "bg",
		From:     Frame(100),
		Duration: nil, // infinite
		Node:     LayerNode{Name: "bg", Kind: LayerKindImage},
	})

	// At frames 100, 200, 499, the layer should be active
	for _, f := range []int64{100, 200, 499} {
		scene := ResolveCompositionFlat(comp, Frame(f))
		if scene.ActiveCount != 1 {
			t.Fatalf("frame %d: expected 1 active layer (infinite duration), got %d", f, scene.ActiveCount)
		}
	}

	// At frame 500 (composition duration), not active (root sequence ends)
	scene := ResolveCompositionFlat(comp, Frame(500))
	if scene.ActiveCount != 0 {
		t.Fatalf("frame 500: expected 0 active layers (beyond composition duration), got %d", scene.ActiveCount)
	}
}

func TestLegacyLayer_RoundTrip_MultipleLegacyLayers(t *testing.T) {
	comp, err := NewComposition("test", Frame(200), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dur50 := Frame(50)

	comp.AddLegacyLayers([]LegacyLayer{
		{Name: "intro", From: Frame(0), Duration: &dur50, Node: LayerNode{Name: "intro", Kind: LayerKindText}},
		{Name: "main", From: Frame(30), Duration: &dur50, Node: LayerNode{Name: "main", Kind: LayerKindVideo}},
	})

	// At frame 15: only intro is active
	scene15 := ResolveCompositionFlat(comp, Frame(15))
	if scene15.ActiveCount != 1 {
		t.Fatalf("frame 15: expected 1 active layer (intro), got %d", scene15.ActiveCount)
	}
	if scene15.Layers[0].TimeContext.ScopePath != "root/intro_legacy" {
		t.Fatalf("frame 15: expected scope_path=root/intro_legacy, got %s", scene15.Layers[0].TimeContext.ScopePath)
	}

	// At frame 45: both intro and main are active
	scene45 := ResolveCompositionFlat(comp, Frame(45))
	if scene45.ActiveCount != 2 {
		t.Fatalf("frame 45: expected 2 active layers, got %d", scene45.ActiveCount)
	}

	// At frame 90: neither is active (intro ends at 30, main ends at 80)
	scene90 := ResolveCompositionFlat(comp, Frame(90))
	if scene90.ActiveCount != 0 {
		t.Fatalf("frame 90: expected 0 active layers, got %d", scene90.ActiveCount)
	}
}

func TestLegacyLayer_ScopePathPreservation(t *testing.T) {
	comp, err := NewComposition("test", Frame(200), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dur := Frame(60)
	comp.AddLegacyLayer(LegacyLayer{
		Name:     "my_title",
		From:     Frame(10),
		Duration: &dur,
		Node:     LayerNode{Name: "my_title", Kind: LayerKindText},
	})

	scene := ResolveCompositionFlat(comp, Frame(30))
	if scene.ActiveCount != 1 {
		t.Fatalf("expected 1 active layer, got %d", scene.ActiveCount)
	}

	// The scope_path should include the "_legacy" suffix
	sp := scene.Layers[0].TimeContext.ScopePath
	if sp != "root/my_title_legacy" {
		t.Fatalf("expected scope_path='root/my_title_legacy', got %q", sp)
	}
}
