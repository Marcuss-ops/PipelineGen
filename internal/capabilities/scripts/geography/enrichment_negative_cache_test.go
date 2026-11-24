package geography

import (
	"context"
	"testing"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// noResultRecordingGeocoder never resolves: every query is a certified
// no-result, so the only thing under test is how many times the run asks.
type noResultRecordingGeocoder struct {
	calls []capabilitygeocoding.Request
}

func (g *noResultRecordingGeocoder) Geocode(_ context.Context, req capabilitygeocoding.Request) (capabilitygeocoding.Result, error) {
	g.calls = append(g.calls, req)
	return capabilitygeocoding.Result{}, capabilitygeocoding.ErrNoResult
}

// Before the run-scoped refusal, the same unresolved name cost one lookup per
// scene PLUS the unconditional retry pass in each scene (six calls for three
// scenes). The key is the normalized name and its grounded context, so the
// first no-result now answers every later scene and retry frame.
func TestEnrichAsksOnceForTheSameNoResult(t *testing.T) {
	geocoder := &noResultRecordingGeocoder{}
	annotations := make(map[int]*scriptpkg.SceneAnnotations, 3)
	texts := make(map[int]string, 3)
	for scene := 0; scene < 3; scene++ {
		annotations[scene] = &scriptpkg.SceneAnnotations{
			PrimaryEntities: []scriptpkg.AnnotatedEntity{place("Nowhere")},
		}
		texts[scene] = "A trip to Nowhere."
	}
	if err := Enrich(context.Background(), geocoder, annotations, "it", Options{SceneTexts: texts}); err != nil {
		t.Fatal(err)
	}
	if got := len(geocoder.calls); got != 1 {
		t.Fatalf("provider calls = %d, want 1 (same name+context must be refused once per run)", got)
	}
	if annotations[0].PrimaryEntities[0].Geo != nil {
		t.Fatal("an unresolvable place must not receive coordinates")
	}
}
