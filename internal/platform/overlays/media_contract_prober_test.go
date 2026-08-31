package overlays

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

type fakeMediaProbe struct {
	info *mediaexec.MediaInfo
	err  error
}

func (f *fakeMediaProbe) Probe(_ context.Context, _ string) (*mediaexec.MediaInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.info, nil
}

func TestMediaContractProber_MapsProbeFactsAndHashesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overlay.mov")
	payload := []byte("rendered-overlay-bytes")
	if err := os.WriteFile(path, payload, 0644); err != nil {
		t.Fatal(err)
	}
	proc := &fakeMediaProbe{info: &mediaexec.MediaInfo{
		Duration:         5 * time.Second,
		Width:            1920,
		Height:           1080,
		VideoCodec:       "prores",
		PixelFormat:      "yuva444p",
		FormatName:       "mov,mp4,m4a,3gp,3g2,mj2",
		VideoStreamCount: 1,
		StreamCount:      1,
		AudioStreamCount: 0,
		// Match the canonical assembly V2 contract (24/1) so the probed
		// facts satisfy DefaultOverlayContractV1's exact FPS validation.
		FPSNum: 24,
		FPSDen: 1,
	}}
	p := NewMediaContractProber(proc)
	got, err := p.ProbeOverlay(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])
	if got.SHA256 != wantSHA {
		t.Fatalf("SHA256 = %q, want %q", got.SHA256, wantSHA)
	}
	if got.SizeBytes != int64(len(payload)) {
		t.Fatalf("SizeBytes = %d, want %d", got.SizeBytes, len(payload))
	}
	if got.Width != 1920 || got.Height != 1080 || got.DurationUS != 5_000_000 ||
		got.FPSNum != 24 || got.FPSDen != 1 || got.AudioStreams != 0 ||
		got.Codec != "prores" || got.PixelFormat != "yuva444p" ||
		got.Container != "mov,mp4,m4a,3gp,3g2,mj2" {
		t.Fatalf("unexpected probe result: %+v", got)
	}
	// The full contract must then validate against the probed facts.
	if err := capoverlay.DefaultOverlayContractV1.Validate(got); err != nil {
		t.Fatalf("probed facts should satisfy the contract: %v", err)
	}
}

func TestMediaContractProber_FailsClosedOnProbeError(t *testing.T) {
	p := NewMediaContractProber(&fakeMediaProbe{err: fmt.Errorf("ffprobe unavailable")})
	if _, err := p.ProbeOverlay(context.Background(), "whatever"); err == nil {
		t.Fatal("probe error must fail closed")
	}
}

func TestMediaContractProber_FailsClosedWhenUnconfigured(t *testing.T) {
	p := NewMediaContractProber(nil)
	if _, err := p.ProbeOverlay(context.Background(), "whatever"); err == nil {
		t.Fatal("unconfigured prober must fail closed")
	}
}
