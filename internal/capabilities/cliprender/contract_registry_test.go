package cliprender

import (
	"errors"
	"strings"
	"testing"
)

func TestContractTablesAreValidAtBoot(t *testing.T) {
	if err := validateContractTables(); err != nil {
		t.Fatalf("contract tables must be valid: %v", err)
	}
}

func TestValidateContractTablesRejectsDuplicateDimensions(t *testing.T) {
	checks := append([]contractCheck(nil), contractChecks...)
	checks = append(checks, contractChecks[0])
	if err := validateContractCheckTable(checks); err == nil {
		t.Fatal("duplicate contract dimensions must be rejected")
	}
}

func TestV2ResolverRejectsNonCanonicalFPS(t *testing.T) {
	req := &RenderRequest{Output: &OutputSpec{
		Contract: OutputContractVeloxAssemblyReadyV2,
		Width:    1920, Height: 1080, FPSNum: 30000, FPSDen: 1001,
	}}
	if _, err := NewContractResolver().Resolve(nil, req); err == nil || !errors.Is(err, ErrOutputContractMismatch) {
		t.Fatalf("expected OUTPUT_CONTRACT_MISMATCH, got %v", err)
	}
}

func TestV2ResolverAcceptsCanonicalFPS(t *testing.T) {
	req := &RenderRequest{Output: &OutputSpec{
		Contract: OutputContractVeloxAssemblyReadyV2,
		Width:    1920, Height: 1080, FPSNum: 24, FPSDen: 1,
	}}
	resolved, err := NewContractResolver().Resolve(nil, req)
	if err != nil {
		t.Fatalf("canonical FPS must resolve: %v", err)
	}
	if resolved.FPSNum != 24 || resolved.FPSDen != 1 {
		t.Fatalf("unexpected resolved FPS: %d/%d", resolved.FPSNum, resolved.FPSDen)
	}
}

// TestAudioTimebaseRejectsNonContractSampleRate pins the fail-closed audio
// gate that the 2026-09-15 full-GPU sweep exercised against real media.
//
// Chronon's native A/V mux copies the source audio stream verbatim
// (RenderingGen audioModeCopyOnly / audioSourcePathFromPlan), so a 44.1 kHz
// source produces a 44.1 kHz output and the sealed plan's requested
// {"mode":"transcode"} is inert. The contract gate is therefore the ONLY
// thing standing between a non-conforming artifact and publication, and it
// must report the exact dimension that failed instead of degrading.
//
// The audio timebase dimension deliberately has NO tolerance whitelist (unlike
// video-timebase, which accepts 1/12288 and friends as legitimate 24 fps
// alternatives): an audio timebase of 1/<rate> IS the sample rate, so a
// mismatch is a real, audible contract violation.
func TestAudioTimebaseRejectsNonContractSampleRate(t *testing.T) {
	contract := &ResolvedContract{
		AudioCodec: "aac", AudioProfile: "LC", SampleRate: 48000,
		Channels: 2, AudioChannelLayout: "stereo", AudioStreams: 1,
		AudioTimeBaseNum: 1, AudioTimeBaseDen: 48000,
	}
	conforming := &OutputProbe{
		HasVideo: true, HasAudio: true, AudioCodec: "aac", AudioProfile: "LC",
		SampleRate: 48000, Channels: 2, ChannelLayout: "stereo", AudioStreams: 1,
		AudioTimeBaseNum: 1, AudioTimeBaseDen: 48000,
	}
	if err := ValidateContract(contract, conforming); err != nil {
		t.Fatalf("a 48 kHz output must validate against a 48 kHz contract: %v", err)
	}

	// The observed failure: a 44.1 kHz source copied through the mux.
	copyof := *conforming
	copyof.SampleRate = 44100
	copyof.AudioTimeBaseDen = 44100
	err := ValidateContract(contract, &copyof)
	if !errors.Is(err, ErrContractMismatch) {
		t.Fatalf("a 44.1 kHz copy must fail the contract gate, got %v", err)
	}
	if want := "audio timebase 1/44100 != 1/48000"; !strings.Contains(err.Error(), want) {
		t.Fatalf("typed detail must name the audio timebase, want %q in %q", want, err.Error())
	}
}

func TestValidateContractTablesRejectsInvalidRegistryEntry(t *testing.T) {
	builders := map[string]func(*RenderRequest) (*ResolvedContract, error){
		"": nil,
	}
	if err := validateContractBuilderRegistry(builders); err == nil {
		t.Fatal("empty ID and nil builder must be rejected")
	}
}
