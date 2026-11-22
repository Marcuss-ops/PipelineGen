package cliprender

import (
	"context"
	"testing"
)

func TestCopyModeDoesNotPinAudioBitrateAndTranscodeIsRejected(t *testing.T) {
	req := &RenderRequest{
		Output: &OutputSpec{
			Contract: OutputContractVeloxAssemblyReadyV1,
			Width:    1920,
			Height:   1080,
			FPSNum:   24,
			FPSDen:   1,
		},
		Audio: &AudioSpec{Mode: AudioModeCopyIfCompatible},
	}
	resolved, err := (defaultContractResolver{}).Resolve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.AudioBitrate != "" {
		t.Fatalf("copy mode pinned audio bitrate %q, want unpinned", resolved.AudioBitrate)
	}

	req.Audio.Mode = AudioModeTranscode
	if _, err := (defaultContractResolver{}).Resolve(context.Background(), req); err == nil {
		t.Fatal("transcode must be rejected: the native mux copies the source stream")
	}
}
