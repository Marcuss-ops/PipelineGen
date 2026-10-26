package overlay

import (
	"math"
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

// testGlobalPx mirrors dynamic_tile_pyramid.latlon_to_global_px (and the C++
// map_camera.cpp port): the independent projection reference used to verify
// the framing keeps every place inside the native frame.
func testGlobalPx(lat, lon float64, zoom int) (float64, float64) {
	clamp := func(v, lo, hi float64) float64 {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	lat = clamp(lat, -85.05112878, 85.05112878)
	lon = clamp(lon, -180, 180)
	scale := 256.0 * float64(int64(1)<<zoom)
	x := (lon + 180) / 360 * scale
	rad := lat * math.Pi / 180
	y := (1 - math.Log(math.Tan(rad)+1/math.Cos(rad))/math.Pi) * 0.5 * scale
	return x, y
}

// TestMapNativeCameraFramingSinglePlaceKeepsTheProductDive pins the
// single-place contract: the anchor is the place itself, and with no grounded
// camera move the zoom keys are OMITTED so the renderer runs its certified
// product dive — the pipeline never invents zooms.
func TestMapNativeCameraFramingSinglePlaceKeepsTheProductDive(t *testing.T) {
	pins := []map[string]any{{"id": "a", "label": "A", "latitude": 41.9, "longitude": 12.5}}
	lat, lon, _, _, send := MapNativeCameraFraming(pins, 1920, 1080, false, 0, 0)
	if lat != 41.9 || lon != 12.5 {
		t.Fatalf("anchor = (%v,%v), want the place itself", lat, lon)
	}
	if send {
		t.Fatal("a single place with no grounded move must omit the zoom keys (certified product dive)")
	}
	lat, lon, start, end, send := MapNativeCameraFraming(pins, 1920, 1080, true, 5.5, 8.25)
	if !send || start != 5.5 || end != 8.25 {
		t.Fatalf("a grounded camera move must travel verbatim, got send=%t %.2f->%.2f", send, start, end)
	}
	if lat != 41.9 || lon != 12.5 {
		t.Fatalf("anchor = (%v,%v), want the place itself", lat, lon)
	}
	// Two entries on the SAME coordinate are one renderer place.
	twin := append(pins, map[string]any{"id": "b", "label": "B", "latitude": 41.9, "longitude": 12.5})
	if _, _, _, _, send := MapNativeCameraFraming(twin, 1920, 1080, false, 0, 0); send {
		t.Fatal("duplicate coordinates must collapse to the single-place contract")
	}
}

// TestMapNativeCameraFramingMultiPlaceKeepsEveryPlaceInFrame pins the
// multi-place contract: the anchor is the mean of the renderer-drawn places
// and the END zoom is the tightest framing whose native frame still holds the
// whole pin span inside the margin — nothing is dropped off frame.
func TestMapNativeCameraFramingMultiPlaceKeepsEveryPlaceInFrame(t *testing.T) {
	pins := []map[string]any{
		{"id": "a", "label": "A", "latitude": 43.7687, "longitude": 11.2569},
		{"id": "b", "label": "B", "latitude": 43.3188, "longitude": 11.3308},
		{"id": "c", "label": "C", "latitude": 42.8512, "longitude": 12.5021},
	}
	const width, height = 1920, 1080
	lat, lon, start, end, send := MapNativeCameraFraming(pins, width, height, false, 0, 0)
	if !send {
		t.Fatal("a multi-place map must travel explicit zooms")
	}
	wantLat, wantLon := (43.7687+43.3188+42.8512)/3, (11.2569+11.3308+12.5021)/3
	if math.Abs(lat-wantLat) > 1e-9 || math.Abs(lon-wantLon) > 1e-9 {
		t.Fatalf("anchor = (%v,%v), want the mean of the drawn places (%v,%v)", lat, lon, wantLat, wantLon)
	}
	if !(start < end && end <= 17.8 && start >= 2) {
		t.Fatalf("zooms %.2f->%.2f must dive and stay inside the certified range", start, end)
	}
	// At the END zoom every place must project inside the frame margin.
	zf := int(math.Floor(end))
	scale := math.Pow(2, end-float64(zf))
	ax, ay := testGlobalPx(lat, lon, zf)
	margin := 0.12 * float64(width)
	marginY := 0.12 * float64(height)
	for _, pin := range pins {
		gx, gy := testGlobalPx(pin["latitude"].(float64), pin["longitude"].(float64), zf)
		x := float64(width)/2 + (gx-ax)*scale
		y := float64(height)/2 + (gy-ay)*scale
		if x < margin || x > float64(width)-margin || y < marginY || y > float64(height)-marginY {
			t.Fatalf("pin %v projects to (%.0f,%.0f) at end zoom %.2f, outside the %.0f%% frame margin", pin["id"], x, y, end, 100*0.12)
		}
	}
	// A wider span must yield a LOWER end zoom (tighter framing has limits).
	wide := append([]map[string]any{}, pins...)
	wide[2] = map[string]any{"id": "c", "label": "C", "latitude": 36.0, "longitude": 18.0}
	_, _, _, wideEnd, _ := MapNativeCameraFraming(wide, width, height, false, 0, 0)
	if wideEnd >= end {
		t.Fatalf("wider span must fit at a lower zoom: %.2f >= %.2f", wideEnd, end)
	}
}

// TestMapNativeCameraFramingDegenerateInputs pins the closed edges: no
// coordinates, no zooms sent; a grounded move on a multi-place map cannot
// override the fit — route zooms frame the route endpoints, so the fit keeps
// every place in frame and the two framings agree.
func TestMapNativeCameraFramingDegenerateInputs(t *testing.T) {
	if _, _, _, _, send := MapNativeCameraFraming(nil, 1920, 1080, false, 0, 0); send {
		t.Fatal("an empty pin set must not invent camera zooms")
	}
	pins := []map[string]any{
		{"id": "a", "latitude": 43.7687, "longitude": 11.2569},
		{"id": "b", "latitude": 43.3188, "longitude": 11.3308},
	}
	_, _, _, fitEnd, _ := MapNativeCameraFraming(pins, 1920, 1080, false, 0, 0)
	_, _, start, end, send := MapNativeCameraFraming(pins, 1920, 1080, true, 4.0, 6.0)
	if !send || end != fitEnd || start >= end {
		t.Fatalf("multi-place framing must keep the fit (got %.2f, fit %.2f) with a grounded move present: %.2f->%.2f send=%t", end, fitEnd, start, end, send)
	}
}
