package scriptgeneration

import (
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func TestFinalJobOverlayStreamWireSeparatesCanonicalFromLegacy(t *testing.T) {
	canonical := finalJobOverlayStreamWire(nil, &RenderArtifact{ProfileID: kernelmedia.OverlayVideoOnlyAssemblyProfileID})
	if canonical == nil || canonical["copy_ready"] != true ||
		canonical["extradata_sha256"] != kernelmedia.CanonicalVideoExtradataSHA256 ||
		canonical["time_base_den"] != 90000 {
		t.Fatalf("canonical V3 stream = %+v", canonical)
	}
	legacy := finalJobOverlayStreamWire(nil, &RenderArtifact{ProfileID: kernelmedia.OverlayVideoOnlyPreviousID})
	if legacy == nil || legacy["copy_ready"] != false {
		t.Fatalf("legacy V2 stream = %+v", legacy)
	}
	if kernelmedia.OverlayVideoOnlyProfileID != kernelmedia.OverlayVideoOnlyAssemblyProfileID {
		t.Fatal("the final-job default must be the canonical V3 profile")
	}
	if kernelmedia.OverlayVideoOnlyPreviousID == kernelmedia.OverlayVideoOnlyAssemblyProfileID {
		t.Fatal("the V2 previous lane must remain distinct from V3")
	}
	if _, claimed := legacy["extradata_sha256"]; claimed {
		t.Fatalf("legacy V2 must not claim canonical avcC: %+v", legacy)
	}
	if got := finalJobOverlayStreamWire(nil, &RenderArtifact{}); got != nil {
		t.Fatalf("unprofiled artifact must not emit a stream claim: %+v", got)
	}
}

func TestFinalJobOverlayStreamWireRefusesUnprovenCanonicalClaims(t *testing.T) {
	canonicalStream := &capmodel.OverlayManifestStream{
		ProfileID: kernelmedia.OverlayVideoOnlyAssemblyProfileID, ExtradataSHA256: kernelmedia.CanonicalVideoExtradataSHA256,
		TimeBaseNum: 1, TimeBaseDen: 90000, EncoderRevision: kernelmedia.CanonicalVideoProfileRevision,
	}
	ok := finalJobOverlayStreamWire(&capmodel.OverlayManifest{Stream: canonicalStream}, &RenderArtifact{ProfileID: kernelmedia.OverlayVideoOnlyAssemblyProfileID})
	if ok == nil || ok["copy_ready"] != true || ok["extradata_sha256"] != kernelmedia.CanonicalVideoExtradataSHA256 {
		t.Fatalf("canonical declared stream = %+v", ok)
	}

	badAVCC := *canonicalStream
	badAVCC.ExtradataSHA256 = strings.Repeat("a", 64)
	for name, tc := range map[string]struct {
		stream   *capmodel.OverlayManifestStream
		artifact string
	}{
		"wrong avcC":               {stream: &badAVCC, artifact: kernelmedia.OverlayVideoOnlyAssemblyProfileID},
		"wrong timebase":           {stream: &capmodel.OverlayManifestStream{ProfileID: canonicalStream.ProfileID, ExtradataSHA256: canonicalStream.ExtradataSHA256, TimeBaseNum: 1, TimeBaseDen: 12288, EncoderRevision: canonicalStream.EncoderRevision}, artifact: kernelmedia.OverlayVideoOnlyAssemblyProfileID},
		"stream/artifact mismatch": {stream: canonicalStream, artifact: kernelmedia.OverlayVideoOnlyPreviousID},
	} {
		got := finalJobOverlayStreamWire(&capmodel.OverlayManifest{Stream: tc.stream}, &RenderArtifact{ProfileID: tc.artifact})
		if got == nil || got["copy_ready"] != false {
			t.Fatalf("%s: got %+v, want copy_ready=false", name, got)
		}
		if _, claimed := got["extradata_sha256"]; claimed {
			t.Fatalf("%s: must not transport an unproven extradata: %+v", name, got)
		}
	}
}
