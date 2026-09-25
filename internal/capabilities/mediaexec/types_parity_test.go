package mediaexec

import (
	"reflect"
	"testing"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// TestContractTypesAreKernelAliases pins the SINGLE-OWNER contract for the
// media-execution types: every type this package re-exports MUST be the exact
// same Go type as its kernel/media definition.
//
// Forward-prevention rationale: these types used to exist as two independent
// struct copies (one here, one in kernel/media) and their VideoProfile.FrameRate
// defaults had already diverged (one projected the canonical assembly contract,
// the other returned a hardcoded 24/1). A reintroduced struct copy — or a new
// method defined on an imported type — fails to compile or fails here, instead
// of drifting silently.
func TestContractTypesAreKernelAliases(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"VideoProfile", VideoProfile{}, kernelmedia.VideoProfile{}},
		{"EncoderPolicy", EncoderPolicy{}, kernelmedia.EncoderPolicy{}},
		{"ExecutionConfig", ExecutionConfig{}, kernelmedia.ExecutionConfig{}},
		{"NormalizeOptions", NormalizeOptions{}, kernelmedia.NormalizeOptions{}},
		{"CutAndNormalizeOptions", CutAndNormalizeOptions{}, kernelmedia.CutAndNormalizeOptions{}},
		{"WatermarkOptions", WatermarkOptions{}, kernelmedia.WatermarkOptions{}},
		{"MediaInfo", MediaInfo{}, kernelmedia.MediaInfo{}},
	}
	for _, tc := range cases {
		got, want := reflect.TypeOf(tc.got), reflect.TypeOf(tc.want)
		if got != want {
			t.Errorf("%s: mediaexec type is %v but the kernel/media owner is %v (a duplicate definition was reintroduced)", tc.name, got, want)
		}
	}
}

// TestVideoProfileFrameRateProjectsCanonicalContract pins the rational FPS a
// profile with no explicit FPS resolves to: it must be the canonical assembly
// contract's FPS, never a restated literal. This is the guard that keeps the
// projection and assembly_contract.go in step when the contract moves.
func TestVideoProfileFrameRateProjectsCanonicalContract(t *testing.T) {
	num, den := VideoProfile{}.FrameRate()
	want := kernelmedia.DefaultAssemblyMediaContractV2().FPS
	if num != want.Num || den != want.Den {
		t.Fatalf("VideoProfile{}.FrameRate() = %d/%d, want the canonical contract FPS %d/%d", num, den, want.Num, want.Den)
	}
}
