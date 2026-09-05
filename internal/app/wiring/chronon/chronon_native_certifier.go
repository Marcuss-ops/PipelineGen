package chronon

// chronon_native_certifier.go — the ChrononNativeCertified capability.
//
// The mere presence of a Chronon render binary (ChrononVulkan) says nothing
// about whether the native CUDA/Vulkan handoff actually works on this host:
// an RTX A4000 with NVDEC/NVENC/Vulkan can still fault the custom
// CUDA↔Vulkan surface exchange with CUDA_ERROR_ILLEGAL_ADDRESS on every job.
//
// This certifier therefore runs a REAL certification: a tiny ~1-second job
// at the canonical assembly contract FPS (NVDEC → CUDA/Vulkan surface →
// NVENC, the exact handoff that broke) and only then reports
// ChrononNativeCertified=true. The result is cached per
// environment fingerprint (chronon binary SHA256 + GPU driver/model/compute
// capability) and re-certified automatically when any of those change — so a
// broken handoff adds ZERO latency to real renders (the resolver never even
// attempts chronon_vulkan), and a fixed or changed binary/driver/GPU is
// picked up without operator intervention.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"go.uber.org/zap"
)

// certCommandRunner runs a command and returns its combined output. It is a
// seam so the certifier is unit-testable without real binaries; the default
// implementation shells out via exec.CommandContext.
type certCommandRunner func(ctx context.Context, name string, args ...string) (string, error)

func runCombined(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

// chrononCertResult is the cached certification outcome for one environment
// fingerprint. A failed certification keeps its reason so operators see WHY
// the Chronon backend is not selected (e.g. CUDA_ERROR_ILLEGAL_ADDRESS).
type chrononCertResult struct {
	Fingerprint string `json:"fingerprint"`
	Certified   bool   `json:"certified"`
	Reason      string `json:"reason,omitempty"`
	DurationMS  int64  `json:"duration_ms"`
	Frames      int    `json:"frames"`
}

// Certification dimensions (mirrors the review: re-certify on chronon SHA,
// driver version, GPU model, CUDA runtime). The CUDA runtime ships inside the
// chronon binary, so its change is captured by the binary SHA; the driver is
// what gates which CUDA runtimes work, so driver+GPU identity completes the
// environment key.
const certTimeout = 90 * time.Second

var chrononCertificationContract = kernelmedia.DefaultAssemblyMediaContractV2()

func certCanvasWidth() int  { return chrononCertificationContract.Width }
func certCanvasHeight() int { return chrononCertificationContract.Height }
func certFPSNum() int       { return chrononCertificationContract.FPS.Num }
func certFPSDen() int       { return chrononCertificationContract.FPS.Den }
func certFrames() int       { return certFPSNum() * 1 / certFPSDen() }

// chrononNativeCertifier owns the ChrononNativeCertified probe: a real
// short render run, cached per environment fingerprint, re-certified on any
// fingerprint change. The probe path never blocks on an in-flight
// certification — the first probe may pay the certification cost (or a
// boot-time warm-up goroutine absorbs it), every later probe is a cached
// read.
// gpuIdentityCache memoizes the nvidia-smi identity so per-render probes do
// not pay a GPU query on every render (the review's zero-added-latency rule).
// The short TTL still picks up driver/GPU changes without a restart; binary
// SHA changes force re-certification immediately.
type gpuIdentityCache struct {
	at                time.Time
	driver, model, cc string
}

type chrononNativeCertifier struct {
	bin     string // chronon render binary (empty → never certified)
	ffmpeg  string // ffmpeg binary used to synthesize the certification clip
	log     *zap.Logger
	run     certCommandRunner
	tmpBase string // scratch root for certification artifacts
	gpuTTL  time.Duration
	encoder string // resolved video codec from the canonical encoder policy

	mu      sync.Mutex
	result  *chrononCertResult
	running bool
	gpuID   *gpuIdentityCache
}

// NewChrononNativeCertifier constructs the certifier for a configured Chronon
// binary. A nil logger is replaced with a no-op logger; an empty binary still
// produces a certifier that simply never certifies (fail-closed), so the
// wiring can always install the decorator.
// NewChrononNativeCertifier exposes the StockRust native capability
// certifier to the composition root.
func NewChrononNativeCertifier(bin, ffmpeg, encoder string, log *zap.Logger) *chrononNativeCertifier {
	if log == nil {
		log = zap.NewNop()
	}
	tmpBase := filepath.Join(os.TempDir(), "pipelinegen", "chronon-native-cert")
	return &chrononNativeCertifier{
		bin:     bin,
		ffmpeg:  ffmpeg,
		log:     log,
		run:     runCombined,
		tmpBase: tmpBase,
		gpuTTL:  30 * time.Second,
		encoder: encoder,
	}
}

// WithRunner overrides the command runner (tests inject fakes here).
func (c *chrononNativeCertifier) WithRunner(run certCommandRunner) *chrononNativeCertifier {
	if run != nil {
		c.run = run
	}
	return c
}

// Certified returns the cached certification state, certifying lazily on the
// first call when no cached result matches the current fingerprint. While a
// certification is in flight it returns false WITHOUT waiting — fail-closed
// and zero added latency for concurrent renders; the completed result serves
// the next probe. An uncertified/unconfigured binary is always false.
func (c *chrononNativeCertifier) Certified(ctx context.Context) bool {
	if c == nil || strings.TrimSpace(c.bin) == "" {
		return false
	}
	fingerprint := c.fingerprint(ctx)

	c.mu.Lock()
	if c.result != nil && c.result.Fingerprint == fingerprint {
		certified := c.result.Certified
		c.mu.Unlock()
		return certified
	}
	if c.running {
		// A certification is already in flight: fail closed for THIS probe
		// without blocking (the boot warm-up usually absorbs the cost).
		c.mu.Unlock()
		return false
	}
	c.running = true
	c.mu.Unlock()

	result := c.runCertification(ctx, fingerprint)

	c.mu.Lock()
	c.result = &result
	c.running = false
	c.mu.Unlock()

	if result.Certified {
		c.log.Info("clip.render.chronon.certified",
			zap.String("fingerprint", fingerprint),
			zap.Int("frames", result.Frames),
			zap.Int64("duration_ms", result.DurationMS),
		)
	} else {
		c.log.Warn("clip.render.chronon.not_certified",
			zap.String("fingerprint", fingerprint),
			zap.String("reason", result.Reason),
			zap.Int64("duration_ms", result.DurationMS),
		)
	}
	return result.Certified
}

// Certify runs the certification now and caches the outcome. It is used for
// the boot-time warm-up (called in a goroutine so the first render never pays
// the certification cost); it is safe to call concurrently with Certified —
// the in-flight guard makes the extra call a no-op wait-free.
func (c *chrononNativeCertifier) Certify(ctx context.Context) {
	if c == nil {
		return
	}
	_ = c.Certified(ctx)
}

// fingerprint derives the environment key that decides whether the cached
// certification is still valid: chronon binary SHA256 + GPU driver version,
// model and compute capability. Any change re-certifies automatically.
func (c *chrononNativeCertifier) fingerprint(ctx context.Context) string {
	sha := "unknown"
	if s, _, err := digest.SHA256File(c.bin); err == nil {
		sha = s
	}
	driver, model, computeCap := c.gpuIdentity(ctx)
	return fmt.Sprintf("chronon=%s|driver=%s|gpu=%s|cc=%s", sha, driver, model, computeCap)
}

// gpuIdentity returns the nvidia-smi identity, memoized for gpuTTL so the
// per-render probe path stays cheap (the review's zero-added-latency rule).
// A missing/failing nvidia-smi degrades every field to "unknown" — the SHA
// component still forces re-certification on binary change, and "unknown"
// identity is stable across calls so it does not re-certify on every probe.
func (c *chrononNativeCertifier) gpuIdentity(ctx context.Context) (driver, model, computeCap string) {
	now := time.Now()
	if c.gpuID != nil && now.Sub(c.gpuID.at) < c.gpuTTL {
		return c.gpuID.driver, c.gpuID.model, c.gpuID.cc
	}
	driver, model, computeCap = c.queryGPUIdentity(ctx)
	c.gpuID = &gpuIdentityCache{at: now, driver: driver, model: model, cc: computeCap}
	return driver, model, computeCap
}

func (c *chrononNativeCertifier) queryGPUIdentity(ctx context.Context) (driver, model, computeCap string) {
	out, err := c.run(ctx, "nvidia-smi",
		"--query-gpu=driver_version,name,compute_cap", "--format=csv,noheader")
	if err != nil {
		return "unknown", "unknown", "unknown"
	}
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	fields := strings.Split(first, ", ")
	if len(fields) >= 1 {
		driver = strings.TrimSpace(fields[0])
	}
	if len(fields) >= 2 {
		model = strings.TrimSpace(fields[1])
	}
	if len(fields) >= 3 {
		computeCap = strings.TrimSpace(fields[2])
	}
	if driver == "" {
		driver = "unknown"
	}
	if model == "" {
		model = "unknown"
	}
	if computeCap == "" {
		computeCap = "unknown"
	}
	return driver, model, computeCap
}

// runCertification executes the real ~1-second certification job (canvas,
// FPS and frame count come from the canonical assembly contract via
// certCanvas*/certFPS*/certFrames): synthesize a tiny H264 clip, project a
// minimal video-only plan, invoke the Chronon binary with the SAME flags as
// production (vulkan + nvenc + pipe encoder backend) and verify the output MP4. The critical section is serialized against real
// renders via chrononRenderMu (one Chronon process on the device at a time).
func (c *chrononNativeCertifier) runCertification(ctx context.Context, fingerprint string) chrononCertResult {
	started := time.Now()
	fail := func(reason string) chrononCertResult {
		return chrononCertResult{Fingerprint: fingerprint, Certified: false, Reason: reason, DurationMS: time.Since(started).Milliseconds()}
	}

	runCtx, cancel := context.WithTimeout(ctx, certTimeout)
	defer cancel()

	if err := os.MkdirAll(c.tmpBase, 0o755); err != nil {
		return fail(fmt.Sprintf("cert scratch root: %v", err))
	}
	dir, err := os.MkdirTemp(c.tmpBase, "cert-*")
	if err != nil {
		return fail(fmt.Sprintf("cert scratch dir: %v", err))
	}
	defer os.RemoveAll(dir)
	assets := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		return fail(fmt.Sprintf("cert assets dir: %v", err))
	}

	// 1. Synthesize a tiny real H264 clip (the source NVDEC must decode).
	clipPath := filepath.Join(assets, "clip.mp4")
	if err := c.encodeCertClip(runCtx, clipPath); err != nil {
		return fail(fmt.Sprintf("cert clip encode: %v", err))
	}

	// 2. Minimal video-only plan: NVDEC → CUDA/Vulkan surface → composite →
	//    NVENC — the exact handoff that faulted with CUDA_ERROR_ILLEGAL_ADDRESS.
	plan := chrononRenderPlan{
		Schema:  chrononSchema,
		Version: chrononVersion,
		JobID:   "chronon-native-cert",
		Canvas: chrononCanvas{
			Width:          certCanvasWidth(),
			Height:         certCanvasHeight(),
			FPSNum:         certFPSNum(),
			FPSDen:         certFPSDen(),
			DurationFrames: certFrames(),
		},
		Layers: []chrononLayer{{
			ID:             "video",
			Type:           "video",
			Source:         "clip.mp4",
			Fit:            "stretch",
			StartFrame:     0,
			DurationFrames: certFrames(),
		}},
		Output: chrononOutput{Path: "cert.mp4", Format: "mp4", Codec: "h264"},
	}
	planBytes, err := json.Marshal(plan)
	if err != nil {
		return fail(fmt.Sprintf("cert plan marshal: %v", err))
	}
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
		return fail(fmt.Sprintf("cert plan write: %v", err))
	}

	// 3. Invoke the Chronon binary exactly like production (same flags as the
	//    executor). Share the bounded GPU admission policy with real renders.
	_, release, acquireErr := acquireChrononGPU(runCtx)
	if acquireErr != nil {
		return fail(fmt.Sprintf("chronon GPU admission: %v", acquireErr))
	}
	out, renderErr := c.run(runCtx, c.bin,
		"render",
		"--plan", planPath,
		"--output", filepath.Join(dir, "cert.mp4"),
		"--assets-root", assets,
		"--backend", "vulkan",
		"--hardware", "nvenc",
		"--ffmpeg-mode", "pipe",
		"--encoder-backend", "native",
		"--report",
	)
	release()
	if renderErr != nil {
		preview := strings.TrimSpace(out)
		if len(preview) > 2000 {
			preview = preview[len(preview)-2000:]
		}
		return fail(fmt.Sprintf("chronon render failed: %v: %s", renderErr, preview))
	}

	// 4. Verify the output is a real video of the expected length.
	frames, verifyErr := c.verifyCertOutput(runCtx, filepath.Join(dir, "cert.mp4"))
	if verifyErr != nil {
		return fail(verifyErr.Error())
	}
	return chrononCertResult{
		Fingerprint: fingerprint,
		Certified:   true,
		Frames:      frames,
		DurationMS:  time.Since(started).Milliseconds(),
	}
}

// encodeCertClip synthesizes a 1-second H264 test clip with the configured
// ffmpeg. libx264 is preferred; h264_nvenc is the fallback for ffmpeg builds
// without a CPU encoder.
func (c *chrononNativeCertifier) encodeCertClip(ctx context.Context, clipPath string) error {
	bin := c.ffmpeg
	if strings.TrimSpace(bin) == "" {
		bin = "ffmpeg"
	}
	base := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi",
		"-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=%d/%d:duration=1", certCanvasWidth(), certCanvasHeight(), certFPSNum(), certFPSDen()),
		"-pix_fmt", "yuv420p",
	}
	// Use the resolved encoder from the canonical policy. The governance
	// gate prohibits hardcoded encoder literals outside the resolver;
	// certification must use the same codec as production renders.
	encoder := c.encoder
	if strings.TrimSpace(encoder) == "" {
		return fmt.Errorf("no encoder configured: certification requires a resolved codec from the canonical encoder policy")
	}
	// FFmpeg's software preset names (for example "ultrafast" and
	// "veryfast") are not accepted by the NVENC wrapper in the installed
	// builds.  The certification clip must therefore use an encoder-specific
	// probe preset; this is only a tiny compatibility probe and does not alter
	// the production encoder policy.  p1 is the fastest valid NVENC preset.
	preset := "ultrafast"
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(encoder)), "_nvenc") {
		preset = "p1"
	}
	args := append(append([]string(nil), base...), "-c:v", encoder, "-preset", preset, "-t", "1", clipPath)
	if out, err := c.run(ctx, bin, args...); err != nil {
		return fmt.Errorf("encoder %s: %w: %s", encoder, err, strings.TrimSpace(out))
	}
	if st, statErr := os.Stat(clipPath); statErr != nil || st.Size() == 0 {
		return fmt.Errorf("encoder %s produced empty output", encoder)
	}
	return nil
}

// verifyCertOutput checks the certified MP4 via ffprobe: it must be a video
// stream at least ~0.9s long (one second at the canonical contract FPS,
// tolerating container rounding) with the expected frame count when reported.
func (c *chrononNativeCertifier) verifyCertOutput(ctx context.Context, path string) (int, error) {
	if st, err := os.Stat(path); err != nil || st.Size() == 0 {
		return 0, fmt.Errorf("cert output missing or empty: %v", err)
	}
	ffprobe := "ffprobe"
	if strings.TrimSpace(c.ffmpeg) != "" {
		ffprobe = filepath.Join(filepath.Dir(c.ffmpeg), "ffprobe")
	}
	out, err := c.run(ctx, ffprobe,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=nb_frames,duration",
		"-of", "json", path)
	if err != nil {
		return 0, fmt.Errorf("cert ffprobe: %v", err)
	}
	var parsed struct {
		Streams []struct {
			NbFrames string `json:"nb_frames"`
			Duration string `json:"duration"`
		} `json:"streams"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil || len(parsed.Streams) == 0 {
		return 0, fmt.Errorf("cert ffprobe parse: %v (out=%q)", err, strings.TrimSpace(out))
	}
	stream := parsed.Streams[0]
	frames := 0
	if stream.NbFrames != "" && stream.NbFrames != "N/A" {
		fmt.Sscanf(stream.NbFrames, "%d", &frames)
	}
	var duration float64
	if stream.Duration != "" {
		fmt.Sscanf(stream.Duration, "%f", &duration)
	}
	// Container rounding can shave a few hundredths; require at least 90% of
	// the 1s certification window.
	if duration > 0 && duration < 0.9 {
		return frames, fmt.Errorf("cert output duration %.2fs too short (want ~1s)", duration)
	}
	if duration == 0 && frames < certFrames()-2 {
		return frames, fmt.Errorf("cert output frames %d too few (want ~%d)", frames, certFrames())
	}
	return frames, nil
}

// chrononCertifiedCapabilityProbe decorates the base probe (which already
// reports ChrononVulkan binary presence) with the certified flag. The
// registry gates chronon_vulkan on ChrononNativeCertified, so a configured
// but uncertified binary is never selected — zero wasted GPU attempts.
type chrononCertifiedCapabilityProbe struct {
	base cliprender.BackendCapabilityProbe
	cert *chrononNativeCertifier
}

func (p chrononCertifiedCapabilityProbe) ProbeCapabilities(ctx context.Context) (cliprender.RendererCapabilities, error) {
	caps, err := p.base.ProbeCapabilities(ctx)
	if err != nil {
		return caps, err
	}
	caps.ChrononNativeCertified = p.cert != nil && p.cert.Certified(ctx)
	return caps, nil
}

var _ cliprender.BackendCapabilityProbe = (*chrononCertifiedCapabilityProbe)(nil)
