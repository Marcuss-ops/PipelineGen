package overlays

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
)

func testMapCandidate(t *testing.T, id, label string, lat, lon float64, startUS, durationUS int64) MapCandidate {
	t.Helper()
	candidate, ok := NewMapCandidate(id, label, lat, lon, startUS, durationUS, 1, "city")
	if !ok {
		t.Fatalf("invalid test candidate %s", id)
	}
	return candidate
}

func TestCoalesceMapPlansForNarrativeGroupingUsesOneCoveringPlate(t *testing.T) {
	center := capmodel.MapCenter{Latitude: 43.3, Longitude: 11.2}
	plate := MapPlate{
		ID: "toscana-plate", Zoom: 6, Width: 5760, Height: 3240, Center: center,
		Window: geodesy.CenteredOn(center.Latitude, center.Longitude, 6, 5760, 3240),
	}
	other := plate
	other.ID = "siena-plate"
	candidates := []MapCandidate{
		testMapCandidate(t, "siena", "Siena", 43.3185, 11.3316, 9_132_000, 3_295_100),
		testMapCandidate(t, "campo", "Campo", 43.7109, 10.4787, 12_427_100, 942_115),
	}
	plans := []MapPlan{
		{Plate: plate, Place: candidates[0]},
		{Plate: other, Place: candidates[1]},
	}
	got := coalesceMapPlansForNarrativeGrouping(plans, MapGroupConfig{MaxPinsPerGroup: 3})
	if got[0].Plate.ID != "toscana-plate" || got[1].Plate.ID != "toscana-plate" {
		t.Fatalf("plates = %q, %q; want both on the covering plate", got[0].Plate.ID, got[1].Plate.ID)
	}
}

func TestCoalesceMapPlansDoesNotOverrideExplicitNoGrouping(t *testing.T) {
	center := capmodel.MapCenter{Latitude: 43.3, Longitude: 11.2}
	plate := MapPlate{
		ID: "toscana-plate", Zoom: 6, Width: 5760, Height: 3240, Center: center,
		Window: geodesy.CenteredOn(center.Latitude, center.Longitude, 6, 5760, 3240),
	}
	other := plate
	other.ID = "siena-plate"
	plans := []MapPlan{
		{Plate: plate, Place: testMapCandidate(t, "siena", "Siena", 43.3185, 11.3316, 9_132_000, 3_295_100)},
		{Plate: other, Place: testMapCandidate(t, "campo", "Campo", 43.7109, 10.4787, 12_427_100, 942_115)},
	}
	got := coalesceMapPlansForNarrativeGrouping(plans, MapGroupConfig{NoNarrativeGrouping: true})
	if got[1].Plate.ID != "siena-plate" {
		t.Fatalf("explicit no-grouping must keep the resolved plate, got %q", got[1].Plate.ID)
	}
}

func TestMergeShortFinalMapChunkMergesReadableTail(t *testing.T) {
	siena := testMapCandidate(t, "siena", "Siena", 43.3185, 11.3316, 9_132_000, 3_295_100)
	campo := testMapCandidate(t, "campo", "Campo", 43.7109, 10.4787, 12_427_100, 942_115)
	chunks := [][]MapCandidate{{siena}, {campo}}
	got := mergeShortFinalMapChunk(chunks, 3, mapMaxPresentationUS)
	if len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("chunks = %#v, want one readable two-pin chunk", got)
	}
}

func TestMergeShortFinalMapChunkRespectsPinCap(t *testing.T) {
	siena := testMapCandidate(t, "siena", "Siena", 43.3185, 11.3316, 9_132_000, 3_295_100)
	campo := testMapCandidate(t, "campo", "Campo", 43.7109, 10.4787, 12_427_100, 942_115)
	got := mergeShortFinalMapChunk([][]MapCandidate{{siena}, {campo}}, 1, mapMaxPresentationUS)
	if len(got) != 2 {
		t.Fatalf("chunks = %#v, want the tail kept when the pin cap is 1", got)
	}
}

func TestMergeShortFinalMapChunkRebalancesAtPinCap(t *testing.T) {
	toscana := testMapCandidate(t, "toscana", "Toscana", 43.4587, 11.1389, 219_000, 675_000)
	firenze := testMapCandidate(t, "firenze", "Firenze", 43.7698, 11.2556, 4_970_000, 657_540)
	siena := testMapCandidate(t, "siena", "Siena", 43.3185, 11.3316, 9_132_000, 551_000)
	campo := testMapCandidate(t, "campo", "Campo", 43.7109, 10.4787, 12_427_100, 892_115)
	got := mergeShortFinalMapChunk([][]MapCandidate{{toscana, firenze, siena}, {campo}}, 3, mapMaxPresentationUS)
	if len(got) != 2 || len(got[0]) != 2 || len(got[1]) != 2 {
		t.Fatalf("chunks = %#v, want two readable two-pin chunks", got)
	}
	if span := got[1][1].StartUS + got[1][1].DurationUS - got[1][0].StartUS; span < MapMinimumReadableUS {
		t.Fatalf("tail span = %d, want at least %d", span, MapMinimumReadableUS)
	}
}
