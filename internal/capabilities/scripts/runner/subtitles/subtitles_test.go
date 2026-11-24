package subtitles

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type fakePort struct {
	certified bool
}

func (fakePort) FixedMediaNeedsRender(contract.GenerateRequest, contract.Scene) bool { return true }
func (fakePort) CaptionText(contract.GenerateRequest, contract.Scene) string         { return "caption" }
func (f fakePort) CertifiedLocalRender(string, string, string) bool                  { return f.certified }

func subtitleRequest() contract.GenerateRequest {
	return contract.GenerateRequest{
		SourceLanguage: "en",
		Render:         scriptpkg.VideoRenderSpec{Subtitles: &scriptpkg.VideoSubtitlesSpec{Enabled: true}},
	}
}

func subtitleScene() contract.Scene {
	return contract.Scene{
		ID:    "scene-1",
		Index: 0,
		Clip:  &contract.ClipReference{ID: "clip-1", DurationUS: 2_000_000},
		Text:  map[contract.Language]string{"en": "hello"},
	}
}

func TestBuildPlanDisabledIsNoOp(t *testing.T) {
	jobs, err := BuildPlan(subtitleRequest(), Source{Scenes: []contract.Scene{subtitleScene()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("disabled subtitles produced %d jobs", len(jobs))
	}
}

func TestBuildPlanEmitsUncertifiedFixedMediaUnit(t *testing.T) {
	req := subtitleRequest()
	jobs, err := BuildPlan(req, Source{
		Scenes:           []contract.Scene{subtitleScene()},
		LocalizedRenders: nil,
		Subtitles:        req.Render.Subtitles,
	}, fakePort{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if jobs[0].ClipID != "clip-1" || jobs[0].Text != "hello" || jobs[0].Language != "en" {
		t.Fatalf("unexpected job: %+v", jobs[0])
	}
}

func TestBuildPlanSkipsCertifiedUnit(t *testing.T) {
	req := subtitleRequest()
	jobs, err := BuildPlan(req, Source{
		Scenes:    []contract.Scene{subtitleScene()},
		Subtitles: req.Render.Subtitles,
	}, fakePort{certified: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("certified unit produced %d jobs", len(jobs))
	}
}
