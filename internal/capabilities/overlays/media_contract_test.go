package overlays

import (
	"testing"
)

func TestOverlayRender_ValidatesMediaContract(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err != nil {
		t.Errorf("valid probe should pass: %v", err)
	}
}

func TestOverlayRender_RejectsInvalidMedia(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1280, // wrong width
		Height:       720,  // wrong height
		DurationUS:   5000000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("wrong resolution should fail validation")
	}
}

func TestOverlayRender_HasZeroAudioStreams(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 1, // overlay must have 0 audio streams
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("audio streams != 0 should fail validation")
	}
}

func TestOverlayRender_RejectsWrongCodec(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "h264", // wrong codec
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("wrong codec should fail validation")
	}
}

func TestOverlayRender_RejectsWrongPixelFormat(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuv420p", // wrong pixel format
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("wrong pixel format should fail validation")
	}
}

func TestOverlayRender_RejectsAlphaRequiredButMissing(t *testing.T) {
	contract := OverlayMediaContract{
		ID:            "test-alpha",
		Version:       1,
		RequiresAlpha: true,
		Width:         1920,
		Height:        1080,
		FPSNum:        30,
		FPSDen:        1,
		AudioStreams:  0,
		PixelFormat:   "yuv420p", // no alpha
	}
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuv420p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("alpha required but missing should fail validation")
	}
}

func TestOverlayRender_AcceptsAlphaInPixelFormat(t *testing.T) {
	contract := OverlayMediaContract{
		ID:            "test-alpha",
		Version:       1,
		RequiresAlpha: true,
		Width:         1920,
		Height:        1080,
		FPSNum:        30,
		FPSDen:        1,
		AudioStreams:  0,
		PixelFormat:   "yuva444p",
	}
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err != nil {
		t.Errorf("valid alpha contract should pass: %v", err)
	}
}

func TestOverlayRender_RejectsZeroDuration(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   0, // zero duration
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("zero duration should fail validation")
	}
}

func TestOverlayRender_RejectsZeroFileSize(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    0, // 262-byte MP4 class of bug
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("zero file size should fail validation")
	}
}

func TestOverlayContractForCanvas_Alpha(t *testing.T) {
	c := OverlayContractForCanvas(1280, 720, 30, 1, true)
	if c.Width != 1280 || c.Height != 720 {
		t.Errorf("canvas = %dx%d, want 1280x720", c.Width, c.Height)
	}
	if c.FPSNum != 30 || c.FPSDen != 1 {
		t.Errorf("fps = %d/%d, want 30/1", c.FPSNum, c.FPSDen)
	}
	if !c.RequiresAlpha {
		t.Error("requires_alpha should be true")
	}
	if c.AudioStreams != 0 {
		t.Errorf("audio_streams = %d, want 0", c.AudioStreams)
	}
}

func TestOverlayContractForCanvas_NoAlpha(t *testing.T) {
	c := OverlayContractForCanvas(1920, 1080, 24, 1, false)
	if c.RequiresAlpha {
		t.Error("requires_alpha should be false")
	}
	if c.Codec != "h264" {
		t.Errorf("codec = %q, want h264", c.Codec)
	}
}

func TestResolveMediaContract_KnownIDs(t *testing.T) {
	for _, id := range []string{"", "overlay-v1", DefaultOverlayContractV1.ID} {
		c, err := ResolveMediaContract(id)
		if err != nil {
			t.Fatalf("ResolveMediaContract(%q): %v", id, err)
		}
		if c.ID != DefaultOverlayContractV1.ID {
			t.Fatalf("ResolveMediaContract(%q).ID = %q, want %q", id, c.ID, DefaultOverlayContractV1.ID)
		}
	}
	c, err := ResolveMediaContract(DefaultOverlayContractNoAlpha.ID)
	if err != nil {
		t.Fatalf("ResolveMediaContract(no-alpha): %v", err)
	}
	if c.Codec != "h264" {
		t.Fatalf("no-alpha codec = %q, want h264", c.Codec)
	}
}

func TestResolveMediaContract_UnknownFailsClosed(t *testing.T) {
	if _, err := ResolveMediaContract("overlay-v99"); err == nil {
		t.Fatal("unknown contract id must fail closed")
	}
}

func TestContractIDForCanvas(t *testing.T) {
	if id := ContractIDForCanvas(1280, 720, 30, 1, true); id != DefaultOverlayContractV1.ID {
		t.Fatalf("alpha contract id = %q, want %q", id, DefaultOverlayContractV1.ID)
	}
	if id := ContractIDForCanvas(1280, 720, 30, 1, false); id != DefaultOverlayContractNoAlpha.ID {
		t.Fatalf("no-alpha contract id = %q, want %q", id, DefaultOverlayContractNoAlpha.ID)
	}
}

func TestOverlayMediaContract_FPSComparisonRational(t *testing.T) {
	contract := OverlayMediaContract{
		ID:      "test-fps",
		Version: 1,
		Width:   1920, Height: 1080,
		FPSNum: 24000, FPSDen: 1001, // 23.976
	}
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       24000,
		FPSDen:       1001,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err != nil {
		t.Errorf("23.976 fps rational comparison should pass: %v", err)
	}
}

func TestOverlayMediaContract_ContainerCommaTokenAware(t *testing.T) {
	contract := DefaultOverlayContractV1
	// ffprobe's format_name is a comma-joined container family list; the
	// first token is the canonical container identity.
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       30,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov,mp4,m4a,3gp,3g2,mj2",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err != nil {
		t.Errorf("comma-token container should match: %v", err)
	}
}

func TestOverlayMediaContract_RejectsZeroFPS(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       0, // no usable frame rate reported
		FPSDen:       0,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("a 0/0 fps probe must fail closed (not trivially cross-multiply to zero)")
	}
}
