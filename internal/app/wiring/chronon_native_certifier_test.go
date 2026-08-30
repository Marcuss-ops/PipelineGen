package wiring

import (
	"context"

	"errors"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

// fakeCertRunner stands in for every command the certifier shells out to.
// It records invocations, synthesizes the clip file, answers nvidia-smi and
// ffprobe, and can fail or block the chronon render on demand.
type fakeCertRunner struct {
	mu         sync.Mutex
	calls      []string // command names in invocation order
	renderErr  error
	renderWait <-chan struct{} // when non-nil, render blocks until it closes
	nvidiaSmi  string
	ffprobeOut string
}

func (f *fakeCertRunner) run(ctx context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
	switch {
	case name == "nvidia-smi":
		if f.nvidiaSmi == "" {
			return "550.54.15, NVIDIA GeForce RTX A4000, 8.6\n", nil
		}
		return f.nvidiaSmi, nil
	case name == "ffprobe" || strings.HasSuffix(name, "/ffprobe"):
		if f.ffprobeOut != "" {
			return f.ffprobeOut, nil
		}
		return `{"streams":[{"nb_frames":"24","duration":"1.0"}]}`, nil
	case strings.HasSuffix(name, "ffmpeg") || name == "ffmpeg":
		// The clip path is the last argument (-t 1 <clipPath>).
		clip := args[len(args)-1]
		_ = os.WriteFile(clip, []byte("synthetic-h264"), 0o644)
		return "", nil
	default:
		// The chronon render invocation.
		if f.renderWait != nil {
			select {
			case <-f.renderWait:
			case <-ctx.Done():
				return "cancelled", ctx.Err()
			}
		}
		if f.renderErr != nil {
			return "chronon stderr: CUDA_ERROR_ILLEGAL_ADDRESS", f.renderErr
		}
		// A successful render produces the output MP4 (arg after --output).
		for i, arg := range args {
			if arg == "--output" && i+1 < len(args) {
				_ = os.WriteFile(args[i+1], []byte("certified-mp4"), 0o644)
				break
			}
		}
		return "render report ok", nil
	}
}

func (f *fakeCertRunner) renderCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, name := range f.calls {
		if name == "nvidia-smi" || name == "ffmpeg" || name == "ffprobe" {
			continue
		}
		if strings.HasSuffix(name, "ffmpeg") || strings.HasSuffix(name, "ffprobe") {
			continue
		}
		count++
	}
	return count
}

// newTestCertifier builds a certifier over a real temp binary file so the
// SHA256 fingerprint component is deterministic and mutable.
func newTestCertifier(t *testing.T, runner *fakeCertRunner) *chrononNativeCertifier {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "chronon3d_cli")
	if err := os.WriteFile(bin, []byte("chronon-binary-v1"), 0o755); err != nil {
		t.Fatalf("write fake chronon binary: %v", err)
	}
	cert := NewChrononNativeCertifier(bin, "/usr/bin/ffmpeg", "libx264", nil)
	cert.tmpBase = t.TempDir()
	cert.WithRunner(runner.run)
	return cert
}

func TestChrononNativeCertifier_UsesAssemblyV2Profile(t *testing.T) {
	contract := kernelmedia.DefaultAssemblyMediaContractV2()
	if certCanvasWidth() != contract.Width || certCanvasHeight() != contract.Height || certFPSNum() != contract.FPS.Num || certFPSDen() != contract.FPS.Den || certFrames() != 24 {
		t.Fatalf("certifier profile drifted from assembly V2: %dx%d @ %d/%d, frames=%d", certCanvasWidth(), certCanvasHeight(), certFPSNum(), certFPSDen(), certFrames())
	}
}

func TestChrononNativeCertifier_CertifiesOnceThenServesFromCache(t *testing.T) {
	runner := &fakeCertRunner{}
	cert := newTestCertifier(t, runner)
	ctx := context.Background()

	if !cert.Certified(ctx) {
		t.Fatal("first probe must certify (fake render succeeds)")
	}
	if !cert.Certified(ctx) {
		t.Fatal("second probe must serve the cached certification")
	}
	if !cert.Certified(ctx) {
		t.Fatal("third probe must serve the cached certification")
	}
	if got := runner.renderCalls(); got != 1 {
		t.Fatalf("chronon render invoked %d times, want exactly 1 (cache hit)", got)
	}
}

func TestChrononNativeCertifier_FailedCertificationIsFailClosed(t *testing.T) {
	runner := &fakeCertRunner{renderErr: errors.New("exit status 1")}
	cert := newTestCertifier(t, runner)

	if cert.Certified(context.Background()) {
		t.Fatal("certified = true, want false when the chronon render faults")
	}
	if cert.Certified(context.Background()) {
		t.Fatal("certified = true on cache hit, want the cached failure")
	}
	if got := runner.renderCalls(); got != 1 {
		t.Fatalf("chronon render invoked %d times, want 1 (failed result cached)", got)
	}
}

func TestChrononNativeCertifier_NoBinaryNeverCertifies(t *testing.T) {
	cert := NewChrononNativeCertifier("", "/usr/bin/ffmpeg", "libx264", nil)
	if cert.Certified(context.Background()) {
		t.Fatal("certified = true without a binary, want false")
	}
	// A nil certifier (the wiring helper's fail-closed path) also never
	// certifies and never panics.
	var nilCert *chrononNativeCertifier
	if nilCert.Certified(context.Background()) {
		t.Fatal("nil certifier certified = true, want false")
	}
	nilCert.Certify(context.Background()) // must not panic
}

func TestChrononNativeCertifier_ReCertifiesOnBinarySHAChange(t *testing.T) {
	runner := &fakeCertRunner{}
	cert := newTestCertifier(t, runner)
	ctx := context.Background()

	if !cert.Certified(ctx) {
		t.Fatal("initial certification must succeed")
	}
	if got := runner.renderCalls(); got != 1 {
		t.Fatalf("render calls = %d, want 1", got)
	}

	// Replace the chronon binary: the fingerprint (binary SHA256) changes and
	// the certifier must re-run the real certification automatically.
	if err := os.WriteFile(cert.bin, []byte("chronon-binary-v2-updated"), 0o755); err != nil {
		t.Fatalf("rewrite chronon binary: %v", err)
	}
	if !cert.Certified(ctx) {
		t.Fatal("certified = false after binary change, want re-certification to pass")
	}
	if got := runner.renderCalls(); got != 2 {
		t.Fatalf("render calls = %d, want 2 (re-certified on fingerprint change)", got)
	}
}

func TestChrononNativeCertifier_ReCertifiesOnGPUIdentityChange(t *testing.T) {
	runner := &fakeCertRunner{}
	cert := newTestCertifier(t, runner)
	ctx := context.Background()

	if !cert.Certified(ctx) {
		t.Fatal("initial certification must succeed")
	}
	// The GPU identity is memoized for gpuTTL; shorten it so the change is
	// observed without sleeping.
	cert.mu.Lock()
	cert.gpuTTL = time.Nanosecond
	cert.mu.Unlock()

	runner.mu.Lock()
	runner.nvidiaSmi = "560.00.00, NVIDIA GeForce RTX 5090, 12.0\n"
	runner.mu.Unlock()
	if !cert.Certified(ctx) {
		t.Fatal("certified = false after GPU change, want re-certification to pass")
	}
	if got := runner.renderCalls(); got != 2 {
		t.Fatalf("render calls = %d, want 2 (re-certified on GPU identity change)", got)
	}
}

func TestChrononNativeCertifier_InFlightCertificationDoesNotBlockProbe(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	runner := &fakeCertRunner{renderWait: release}
	cert := newTestCertifier(t, runner)
	ctx := context.Background()

	// First probe starts the certification and blocks inside the fake render.
	firstDone := make(chan bool, 1)
	go func() {
		firstDone <- cert.Certified(ctx)
	}()
	// Wait until the fake render is actually in flight.
	deadline := time.Now().Add(5 * time.Second)
	for runner.renderCalls() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	_ = started
	if runner.renderCalls() == 0 {
		t.Fatal("certification never started")
	}

	// A concurrent probe while the certification is in flight must return
	// false WITHOUT waiting for the in-flight certification (zero added
	// latency, fail-closed) — it must not deadlock.
	concurrentDone := make(chan bool, 1)
	go func() {
		concurrentDone <- cert.Certified(ctx)
	}()
	select {
	case certified := <-concurrentDone:
		if certified {
			t.Fatal("in-flight probe certified = true, want fail-closed false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("probe blocked on an in-flight certification (violates zero-added-latency)")
	}

	close(release)
	if !<-firstDone {
		t.Fatal("first certification must succeed after release")
	}
	if !cert.Certified(ctx) {
		t.Fatal("post-certification probe must read the cached success")
	}
}

func TestChrononNativeCertifier_FingerprintIncludesEnvironmentComponents(t *testing.T) {
	runner := &fakeCertRunner{}
	cert := newTestCertifier(t, runner)
	ctx := context.Background()

	_ = cert.Certified(ctx)
	fp := cert.fingerprint(ctx)
	if !strings.Contains(fp, "chronon=") {
		t.Errorf("fingerprint %q missing chronon SHA", fp)
	}
	if !strings.Contains(fp, "driver=550.54.15") {
		t.Errorf("fingerprint %q missing driver version", fp)
	}
	if !strings.Contains(fp, "gpu=NVIDIA GeForce RTX A4000") {
		t.Errorf("fingerprint %q missing GPU model", fp)
	}
	if !strings.Contains(fp, "cc=8.6") {
		t.Errorf("fingerprint %q missing compute capability", fp)
	}
}

// TestChrononCertifiedCapabilityProbe_SetsFlagFromCertifier verifies the
// decorator probe maps the certifier outcome onto the capability flag while
// passing the base capabilities (binary presence + CUDA chain) through.
func TestChrononCertifiedCapabilityProbe_SetsFlagFromCertifier(t *testing.T) {
	base := recordingProbe{caps: cliprender.RendererCapabilities{
		NVDEC: true, NVENCH264: true, ChrononVulkan: true,
	}}

	// Certified binary → flag on, base caps pass through.
	runnerOK := &fakeCertRunner{}
	certOK := newTestCertifier(t, runnerOK)
	probeOK := chrononCertifiedCapabilityProbe{base: base, cert: certOK}
	caps, err := probeOK.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("ProbeCapabilities(certified): %v", err)
	}
	if !caps.ChrononVulkan || !caps.ChrononNativeCertified {
		t.Fatalf("caps = %+v, want binary present AND certified", caps)
	}
	if !caps.NVDEC || !caps.NVENCH264 {
		t.Fatalf("base caps must pass through, got %+v", caps)
	}

	// Configured but failed certification → flag stays OFF (the binary is
	// still reported as configured for diagnostics; the resolver gates on
	// the certified flag only).
	runnerBad := &fakeCertRunner{renderErr: errors.New("exit status 1")}
	certBad := newTestCertifier(t, runnerBad)
	probeBad := chrononCertifiedCapabilityProbe{base: base, cert: certBad}
	caps, err = probeBad.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("ProbeCapabilities(uncertified): %v", err)
	}
	if !caps.ChrononVulkan {
		t.Fatal("binary presence must still be reported for diagnostics")
	}
	if caps.ChrononNativeCertified {
		t.Fatal("certified = true after a failed certification, want false")
	}

	// No certifier at all (never wired) → flag OFF, base still passes.
	probeNil := chrononCertifiedCapabilityProbe{base: base}
	caps, err = probeNil.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("ProbeCapabilities(nil cert): %v", err)
	}
	if caps.ChrononNativeCertified {
		t.Fatal("certified = true with a nil certifier, want false")
	}
}
