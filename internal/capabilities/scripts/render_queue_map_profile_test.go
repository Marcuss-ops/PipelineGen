package scriptgeneration

import (
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func TestDynamicMapEncodeProfileUsesCanonicalCPUForFinalJobs(t *testing.T) {
	profile, err := dynamicMapEncodeProfile(capmodel.OverlayPlan{
		OutputProfileID: kernelmedia.OverlayVideoOnlyProfileID,
	}, capoverlay.MapProductionCodec)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Codec != capoverlay.MapCPUCodec || profile.ExpectGPU {
		t.Fatalf("final-job map profile = %+v, want certified CPU encoder", profile)
	}
}

func TestDynamicMapEncodeProfileKeepsPreviewDefault(t *testing.T) {
	profile, err := dynamicMapEncodeProfile(capmodel.OverlayPlan{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Codec != capoverlay.MapProductionCodec || !profile.ExpectGPU {
		t.Fatalf("preview map profile = %+v, want production NVENC profile", profile)
	}
}
