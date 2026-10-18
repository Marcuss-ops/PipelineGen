package overlay

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// TestMapPinTimingPayloadProjectsSpokenTiming is the wire contract: one entry
// per pin that has planner timing, in PIN order (not map order), with
// clip-relative seconds — the renderer never invents timing.
func TestMapPinTimingPayloadProjectsSpokenTiming(t *testing.T) {
	pins := []map[string]any{
		{"id": "p1"},
		{"id": "p2"},
		{"id": "p3"},
	}
	params := map[string]any{
		"map_pin_timing": map[string]any{
			// Deliberately out of pin order: output must follow pin order.
			"p3": map[string]any{"start_us": int64(2_500_000), "duration_us": float64(1_250_000)},
			"p1": map[string]any{"start_us": 1_000_000, "duration_us": int64(500_000)},
			// p2 has no timing entry → skipped, never zero-filled.
		},
	}
	out := MapPinTimingPayload(params, pins)
	if len(out) != 2 {
		t.Fatalf("expected 2 timed pins, got %d: %v", len(out), out)
	}
	if out[0]["id"] != "p1" || out[1]["id"] != "p3" {
		t.Fatalf("output must follow pin order, got %v", out)
	}
	if out[0]["start_s"] != 1.0 || out[0]["duration_s"] != 0.5 {
		t.Fatalf("p1 seconds projection wrong: %v", out[0])
	}
	if out[1]["start_s"] != 2.5 || out[1]["duration_s"] != 1.25 {
		t.Fatalf("p3 seconds projection wrong (float64/int64 coercion): %v", out[1])
	}
}

// TestMapPinTimingPayloadSkipsIncompleteEntries pins fail-closed projection:
// a pin whose timing lacks either bound is absent from the wire, never
// rendered with invented timing.
func TestMapPinTimingPayloadSkipsIncompleteEntries(t *testing.T) {
	pins := []map[string]any{{"id": "p1"}, {"id": "p2"}, {"id": "p3"}}
	params := map[string]any{
		"map_pin_timing": map[string]any{
			"p1": map[string]any{"start_us": int64(1)},                       // no duration
			"p2": map[string]any{"duration_us": int64(1)},                    // no start
			"p3": map[string]any{"start_us": "bad", "duration_us": int64(1)}, // non-numeric
		},
	}
	if out := MapPinTimingPayload(params, pins); len(out) != 0 {
		t.Fatalf("incomplete timing must project nothing, got %v", out)
	}
	if out := MapPinTimingPayload(nil, pins); out != nil {
		t.Fatalf("nil params must project nothing, got %v", out)
	}
}

// TestMapPinTimingPayloadAcceptsTypedTimingMap covers the decoded
// map[string]map[string]any form alongside the generic map[string]any form.
func TestMapPinTimingPayloadAcceptsTypedTimingMap(t *testing.T) {
	params := map[string]any{
		"map_pin_timing": map[string]map[string]any{
			"p1": {"start_us": int64(1_000_000), "duration_us": int64(1_000_000)},
		},
	}
	out := MapPinTimingPayload(params, []map[string]any{{"id": "p1"}})
	if len(out) != 1 || out[0]["start_s"] != 1.0 {
		t.Fatalf("typed timing map not projected: %v", out)
	}
}

func TestMapTimingCoercions(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  float64
	}{
		{float64(1.5), 1.5},
		{"not a number", 0},
		{nil, 0},
	} {
		if got := MustFloat(tc.value); got != tc.want {
			t.Fatalf("MustFloat(%v) = %v, want %v", tc.value, got, tc.want)
		}
	}
	if _, ok := mapTimingInt64(map[string]any{"k": 7}, "k"); !ok {
		t.Fatal("int must decode as timing int64")
	}
	if v, ok := mapTimingInt64(map[string]any{"k": float64(7)}, "k"); !ok || v != 7 {
		t.Fatalf("float64 must decode as timing int64, got %v %v", v, ok)
	}
	if v, ok := mapTimingInt64(map[string]any{"k": int64(7)}, "k"); !ok || v != 7 {
		t.Fatalf("int64 must decode as timing int64, got %v %v", v, ok)
	}
	if _, ok := mapTimingInt64(map[string]any{"k": "x"}, "k"); ok {
		t.Fatal("non-numeric must not decode as timing")
	}
	if _, ok := mapTimingInt64(map[string]any{}, "k"); ok {
		t.Fatal("absent key must not decode as timing")
	}
}

func TestMapGroupPayloadProjection(t *testing.T) {
	group := map[string]any{"index": 1.0, "count": 2.0, "size": 3.0}
	got := MapGroupPayload(map[string]any{"map_group": group})
	if got == nil || got["index"] != 1.0 {
		t.Fatalf("map_group identity not projected: %v", got)
	}
	if MapGroupPayload(map[string]any{}) != nil {
		t.Fatal("absent map_group must project nil")
	}
	if MapGroupPayload(nil) != nil {
		t.Fatal("nil params must project nil")
	}
	if got := MapGroupStrategyParam(map[string]any{"map_group_strategy": "tour"}); got != "tour" {
		t.Fatalf("strategy projection wrong: %q", got)
	}
	if got := MapGroupStrategyParam(nil); got != "" {
		t.Fatalf("absent strategy must project empty, got %q", got)
	}
}

// TestMapItemDurationMSUsesPlanBoundsThenFallback pins the projection order:
// the item's own window wins; only a bound-less item falls back to the plan
// duration.
func TestMapItemDurationMSUsesPlanBoundsThenFallback(t *testing.T) {
	item := capabilityoverlay.OverlayItem{StartMs: 1000, EndMs: 3500}
	if got := MapItemDurationMS(item, 9999); got != 2500 {
		t.Fatalf("plan bounds must win, got %d", got)
	}
	boundless := capabilityoverlay.OverlayItem{StartMs: -1}
	if got := MapItemDurationMS(boundless, 9999); got != 9999 {
		t.Fatalf("fallback must apply without bounds, got %d", got)
	}
	inverted := capabilityoverlay.OverlayItem{StartMs: 5, EndMs: 5}
	if got := MapItemDurationMS(inverted, 9999); got != 9999 {
		t.Fatalf("empty window must fall back, got %d", got)
	}
}
