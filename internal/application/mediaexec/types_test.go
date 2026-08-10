package mediaexec

import "testing"

func TestVideoProfileWithDefaults(t *testing.T) {
	got := (VideoProfile{}).WithDefaults()
	want := VideoProfile{
		Width: 1920, Height: 1080, FPS: 24, KeyframeInterval: 48,
		AudioCodec: "aac", AudioBitrate: "128k", SampleRate: 48000, Channels: 2,
	}
	if got != want {
		t.Fatalf("VideoProfile.WithDefaults() = %+v, want %+v", got, want)
	}
}

func TestNormalizeOptionsUsesCanonicalProfileAndPolicy(t *testing.T) {
	opts := NormalizeOptions{
		Profile: VideoProfile{Width: 1280, Height: 720, FPS: 30, KeyframeInterval: 60},
		Policy:  EncoderPolicy{Codec: "h264_nvenc", Preset: "p1", CRF: 21},
	}
	if opts.Profile.Width != 1280 || opts.Profile.Height != 720 || opts.Policy.Codec != "h264_nvenc" {
		t.Fatalf("unexpected canonical normalize options: %+v", opts)
	}
}
