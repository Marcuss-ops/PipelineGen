// Package images — chrome_provider.go (PR-CHROME-PROVIDER-SPLIT, 2026-07-04):
// public surface for the ChromeImageProvider (ImageGenerator backed by
// the persistent Playwright slide_worker.py process).
//
// PR-CHROME-PROVIDER-SPLIT (2026-07-04, godlike/06 + AGENTS.md Pattern 5):
// the pre-split ~260-LoC god file is decomposed per capability into 4
// single-purpose files in the same package (chrome_provider.go +
// slide_worker_process.go + slide_worker_protocol.go + slide_worker_health.go).
// This file owns the public surface only:
//   - ChromeImageProvider struct
//   - NewChromeImageProvider (no numProfiles arg)
//   - Generate (no cooldown tracking)
//   - compile-time ImageGenerator assertion
//
// BRUTAL DECISION (godlike/07 no-fake-availability):
//   - numProfiles constructor arg REMOVED. The pre-split
//     NewChromeImageProvider(scriptsDir, numProfiles, log) silently
//     ignored numProfiles (the worker was always launched with
//     --profiles 1 per ensureStarted). Single-profile is the
//     canonical policy: the argument was fake-availability
//     (param accepted but never honored). Retired.
//   - cooldowns map[int]int64 field REMOVED from the struct.
//     The pre-split code tracked per-profile cooldowns (60s
//     after quota/auth errors) but the policy never fanned out
//     beyond profile 0 (single-profile = no per-profile routing
//     to spread the load). Retired. The diagnostics field
//     ImageGenCooldownProfiles is preserved on DiagnosticsReport
//     and always reports 0 (the truthful state for single-profile
//     policy; godlike/07 no-fake-availability demands the field
//     report the truth, not a tracked-but-never-actionable counter).
//
// Architecture (FASE 7, June 2026; preserved verbatim):
//
//	ChromeImageProvider.Generate()
//	  ├── ensureStarted()           [slide_worker_process.go]
//	  │     └── os/exec → python3 scripts/bridges/slide_worker.py
//	  │           └── stdin  ← JSON requests (warmup, generate, health, quit)
//	  │           └── stdout → JSON responses
//	  │           └── stderr → human-readable logs
//	  ├── stdin  ← {"action":"generate","id":"req-1","prompt":"...","output":"/tmp/..."}
//	  └── stdout → {"id":"req-1","status":"ok","output":"...","elapsed_ms":22000,"bytes":123456}
//	        └── os.ReadFile(output) → GeneratedImage{Data, Format, ...}
//
// ONE browser, ONE page, reused across ALL requests in the process lifetime.
// No browser restart between generations. The Python worker handles:
//   - Persistent Chromium launch_persistent_context (cookies / auth survive)
//   - Single page that navigates to slides.new once on warmup
//   - Per-request: click insert-generated-image → fill prompt → 16:9 → submit
//     → wait 22s → extract googleusercontent image → save to output path
//   - Periodic page recycle (every 20 generations) to prevent DOM bloat
//   - Transparent recovery: fresh page on timeout
//
// Single-profile (the canonical policy, no longer a flag):
//
// The provider launches slide_worker.py in enforced single-profile mode
// (`--profiles 1` hard-coded in slide_worker_process.go::ensureStarted).
// The Go-side mutex serialises stdin/stdout and the Python worker keeps one
// persistent Slides session alive for the whole process.
package images

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/visual_validate"
	"go.uber.org/zap"
)

// generateRequestID returns a 16-byte hex string usable as the worker
// generation_id correlation field. crypto/rand gives genuine entropy
// without pulling in google/uuid.
func generateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback: nanosecond timestamp + PID — always non-empty
		// even on a /dev/urandom failure (extremely rare).
		return fmt.Sprintf("ts-%d-%d", time.Now().UnixNano(), os.Getpid())
	}
	return hex.EncodeToString(b[:])
} // Compile-time assertion: ChromeImageProvider implements ImageGenerator.
var _ ImageGenerator = (*ChromeImageProvider)(nil)

// isWellFormedPhashHex is the shape-only check used by the P2
// phash_parity_ok field. Returns true when s is exactly 16 chars of
// lowercase hex (the canonical encoding emitted by
// fmt.Sprintf("%016x", uint64) on both the worker side and the Go
// ComputeStats side). An empty string, a string with the wrong
// length, or any non-lowercase-hex character fails the shape check.
//
// godlike/07 observability (P2 review, July 2026): this is NOT a
// bit-equality check. The worker and Go use different sampling
// strides, so the two pHash uint64 values rarely coincide. The parity
// field is a cheap "is the diagnostic shape sane?" smoke detector;
// bit-level tampering detection is left to operators inspecting the
// two hex strings in the audit log.
func isWellFormedPhashHex(s string) bool {
	if len(s) != 16 {
		return false
	}
	for i := 0; i < 16; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// ChromeImageProvider implements ImageGenerator by delegating to the
// persistent Playwright-based Python worker that automates Google Slides AI
// (Nano Banana Pro).
//
// The provider launches slide_worker.py as a long-running subprocess with a
// single persistent browser context. Communication is newline-delimited
// JSON over stdin/stdout.
//
// Thread safety: a single mutex serialises stdin/stdout access.
//
// PR-CHROME-PROVIDER-SPLIT (2026-07-04): the legacy `cooldowns map[int]int64`
// field is REMOVED. Single-profile is the canonical policy (no per-profile
// routing); the per-profile cooldown tracker was fake-availability per
// godlike/07 (param accepted, never honored in production dispatch).
type ChromeImageProvider struct {
	scriptsDir string
	log        *zap.Logger

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	started bool
}

// NewChromeImageProvider creates a new ChromeImageProvider.
// The worker is NOT started until the first Generate() call (lazy init).
//
// PR-CHROME-PROVIDER-SPLIT (2026-07-04): the legacy `numProfiles int` arg
// is REMOVED. The pre-split constructor silently ignored it (the worker
// was always launched with `--profiles 1`); single-profile is the
// canonical policy. godlike/07 no-fake-availability: callers no longer
// have the affordance to pass an ignored arg. Composition root
// (`internal/app/build_bundles_core.go::buildImagesService`) updated
// to call the 2-arg signature.
func NewChromeImageProvider(scriptsDir string, log *zap.Logger) *ChromeImageProvider {
	return &ChromeImageProvider{
		scriptsDir: scriptsDir,
		log:        log,
	}
}

// Generate produces an AI-generated image by delegating to the persistent
// Playwright worker. Blocks until the image is generated and saved to disk,
// then reads the file and returns the bytes.
//
// PR-CHROME-PROVIDER-SPLIT (2026-07-04): the per-profile cooldown
// tracking block (the `if errors.Is(classified, ErrImageGenQuota) ||
// errors.Is(classified, ErrImageGenAuth) { p.cooldowns[*resp.Profile] = ... }`)
// is REMOVED. The error is still classified via `ClassifyError(errMsg)`
// (for typed retry decisions) and propagated; the cooldown bookkeeping
// is gone because single-profile policy has no per-profile routing to
// spread the load onto. godlike/07 no-fake-availability: no
// tracked-but-never-actionable counter.
func (p *ChromeImageProvider) Generate(ctx context.Context, req GenerateImageRequest) (*GeneratedImage, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("chrome provider: context cancelled before generation: %w", ctx.Err())
	default:
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Retry-once on dead worker: if the first attempt fails with a
	// broken-pipe / dead-worker error, resetWorker() has already cleared
	// the state. We relaunch and retry the same request exactly once.
	result, err := p.generateOnce(ctx, req)
	if err == nil {
		return result, nil
	}
	if p.isDeadWorkerError(err) {
		p.log.Warn("ChromeImageProvider: dead worker detected, retrying once",
			zap.Error(err))
		return p.generateOnce(ctx, req)
	}
	return nil, err
}

// generateOnce executes a single Generate attempt: ensure worker is
// started, send the generate request, read the response, and return
// the image. Returns a typed dead-worker error if the subprocess dies,
// allowing the caller to retry once.
//
// Must be called while p.mu is held (caller locks).
func (p *ChromeImageProvider) generateOnce(ctx context.Context, req GenerateImageRequest) (*GeneratedImage, error) {
	if err := p.ensureStarted(ctx); err != nil {
		return nil, fmt.Errorf("chrome provider: %w", err)
	}

	// Build a request ID for correlating stdout responses.
	requestID := fmt.Sprintf("gen-%d", time.Now().UnixNano()%1_000_000_000)

	// Use the canonical output path if provided (workspace-based ingest),
	// otherwise fall back to a temp path (backward-compatible for sync endpoints).
	outputPath := req.OutputPath
	if outputPath == "" {
		outputPath = filepath.Join(os.TempDir(), fmt.Sprintf("slide_gen_%s.png", requestID))
	}

	// P1.1 wire-up (July 2026): the worker now expects an extended
	// payload with negative_prompt, style_id, width, height, and a
	// generation_id correlation token. The Python side embeds
	// negative_prompt into the prompt text (Slides UI has one field),
	// uses width/height for the 16:9 selection, and echoes the
	// generation_id + natural dims (resp.NaturalW/H) back in the
	// response so the Go side can correlate logs.
	generationID := requestID + "-" + generateRequestID()[:8]
	workerReq := map[string]any{
		"action":          "generate",
		"id":              requestID,
		"generation_id":   generationID,
		"prompt":          req.Prompt,
		"negative_prompt": req.NegativePrompt,
		"style_id":        req.Style,
		"width":           req.Width,
		"height":          req.Height,
		"output":          outputPath,
	}
	if err := p.writeJSON(workerReq); err != nil {
		if p.isDeadWorkerError(err) {
			p.log.Warn("ChromeImageProvider: broken pipe on write, resetting worker", zap.Error(err))
			p.resetWorker()
			return nil, fmt.Errorf("chrome provider: worker died (broken pipe): %w", err)
		}
		return nil, fmt.Errorf("chrome provider: failed to send generate request: %w", err)
	}

	// Read and parse the response.
	resp, err := p.readResponse(requestID)
	if err != nil {
		if p.isDeadWorkerError(err) {
			p.log.Warn("ChromeImageProvider: dead worker on read, resetting", zap.Error(err))
			p.resetWorker()
			return nil, fmt.Errorf("chrome provider: worker died (read failure): %w", err)
		}
		return nil, fmt.Errorf("chrome provider: failed to read worker response: %w", err)
	}

	if resp.Status != "ok" {
		// godlike/07 FAIL-CLOSED contract (P0.1, July 2026): the Python
		// worker no longer falls back to 'File → Download → PNG' — when
		// extraction fails, it emits a structured error
		// `{status:"error", code:"ErrNoImageCandidate"}` and does NOT
		// write output_path. As a defensive belt-and-suspenders, we
		// remove any file at the canonical output_path before returning
		// the typed error. The caller MUST NOT receive a GeneratedImage
		// when the worker reports an error: the file is removed,
		// the typed sentinel is propagated, and the next retry sees a
		// clean output_path. os.IsNotExist is swallowed (the canonical
		// happy path is 'worker didn't write anything'; logging that as
		// a warning would produce noise). Other remove errors are logged
		// but do NOT block the typed error from propagating.
		if rmErr := os.Remove(outputPath); rmErr != nil && !os.IsNotExist(rmErr) {
			p.log.Warn("ChromeImageProvider: cleanup outputPath after worker error",
				zap.String("output_path", outputPath), zap.Error(rmErr))
		}

		errMsg := resp.Error
		if errMsg == "" {
			errMsg = "unknown worker error"
		}
		// Classify the error for typed retry decisions (FASE 10). The
		// pre-split `cooldowns[*resp.Profile]` block is RETIRED per
		// godlike/07 no-fake-availability (single-profile policy has no
		// per-profile routing to spread the load onto).
		//
		// P0+P1 (July 2026): ClassifyError now matches errblankorplaceholder
		// and errgenerationtimeout as typed sentinels, in addition to
		// errnoimagecandidate (P0.1). The SAME gofail-closed os.Remove
		// applies to all of them — the caller never sees a GeneratedImage
		// and the file is gone.
		return nil, ClassifyError(errMsg)
	}

	// Read the generated image file.
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("chrome provider: failed to read generated image at %s: %w", outputPath, err)
	}

	// P0.2 visual_validate pass — FAIL-CLOSED on blank/placeholder.
	//
	// godlike/07 contract (P0.2, July 2026): the worker claimed
	// status=ok with valid bytes; we cannot trust those bytes without
	// content validation. The worker could have surfaced a slide-export
	// from a stale panel, an old blob: with no real content, or a
	// near-white render the model didn't reject. The content validator
	// decodes the PNG and asserts white_pct, variance, and pHash
	// distance-from-blank invariants.
	//
	// Path: on validation failure, the file is removed AND the typed
	// ERR is wrapped in the canonical images.ErrImageGenBlankOrPlaceholder
	// sentinel so callers (retry policy, audit logs, the smoke test)
	// can errors.Is-probe the package's typed sentinel surface. The
	// visual_validate.ErrBlankOrPlaceholder is preserved in the wrap
	// chain for fine-grained errors.As probes.
	if valErr := visual_validate.Validate(outputPath, req.Style); valErr != nil {
		if rmErr := os.Remove(outputPath); rmErr != nil && !os.IsNotExist(rmErr) {
			p.log.Warn("ChromeImageProvider: cleanup outputPath after visual_validate failure",
				zap.String("output_path", outputPath), zap.Error(rmErr))
		}
		return nil, fmt.Errorf("%w (validator: %v)", ErrImageGenBlankOrPlaceholder, valErr)
	}

	// Decode real dimensions from the PNG header (cheap). ChromeImageProvider
	// previously reported `req.Width/Height` — a lie when the worker
	// produced e.g. a 1280x720 image despite a 1920x1080 request. Real
	// dims are now what `GeneratedImage.Width/Height` carries; the
	// requested w/h are preserved in the log line so operators can
	// audit requested-vs-actual ratio drift without code changes.
	realW, realH := req.Width, req.Height
	if cfg, _, decErr := image.DecodeConfig(bytes.NewReader(data)); decErr == nil {
		realW, realH = cfg.Width, cfg.Height
	}
	ratioMatch := true
	if req.Width > 0 && req.Height > 0 && realW > 0 && realH > 0 {
		reqRatio := float64(req.Width) / float64(req.Height)
		realRatio := float64(realW) / float64(realH)
		ratioMatch = math.Abs(reqRatio-realRatio) < 0.05
	}

	format := "png"

	// P2 (July 2026): replicate the worker's diagnostic stats in the
	// structured Zap log. visual_validate.ComputeStats re-runs the
	// pixel pass on the Go side so the log records both the worker's
	// numbers (CANONICAL, primary source) and the Go-side recompute
	// (defensive cross-validation).
	//
	// godlike/07 observability revision (P2 review, July 2026): the
	// pre-fix strict `workerPhash == goRecomputePhash` parity check
	// was misleading because Go's pHash uses full-iteration
	// step-bounds sampling while the Python worker's _compute_pixel_stats
	// uses a 16-stride pre-sample then 8x8 downsample — the two
	// routines do NOT land on the same physical pixels for non-trivial
	// images, so bit-equality would surface as `phash_parity_ok=false`
	// for every real image even when both sides are canonical-correct.
	// The post-fix contract is a SHAPE check: both hex strings are
	// well-formed (length 16, lowercase hex digits). Operators can
	// still eyeball the two strings in audit logs to detect a tampered
	// extraction (rare; would require a swap between worker save and
	// Go read).
	pixelStats, statsErr := visual_validate.ComputeStats(outputPath)
	workerPhash := resp.PhashHex
	goRecomputePhash := ""
	phashParityOK := false
	if statsErr == nil {
		goRecomputePhash = pixelStats.PHashHex
		phashParityOK = isWellFormedPhashHex(workerPhash) && isWellFormedPhashHex(goRecomputePhash)
	}

	p.log.Info("ChromeImageProvider: generated image",
		zap.String("request_id", requestID),
		zap.String("generation_id", generationID),
		zap.String("method", resp.Method),
		zap.String("style", req.Style),
		zap.String("prompt", req.Prompt),
		zap.Int("bytes", len(data)),
		zap.Int("req_width", req.Width),
		zap.Int("req_height", req.Height),
		zap.Int("real_width", realW),
		zap.Int("real_height", realH),
		zap.Bool("ratio_match", ratioMatch),
		zap.Int("natural_w", resp.NaturalW),
		zap.Int("natural_h", resp.NaturalH),
		zap.Bool("candidate_complete", resp.Complete),
		zap.Int64("elapsed_ms", resp.ElapsedMS),

		// P2 diagnostic replication (worker.primary, Go.recompute).
		zap.Int("candidates_baseline", resp.CandidatesBaseline),
		zap.Int("candidates_after", resp.CandidatesAfter),
		zap.Int("candidates_reported", len(resp.Candidates)),
		zap.Bool("image_mode_active", resp.ImageModeActive),
		zap.String("ratio_selected", resp.RatioSelected),
		zap.String("prompt_original", resp.PromptOriginal),
		zap.String("prompt_dom", resp.PromptDOM),
		zap.String("screenshot_path", resp.ScreenshotPath),

		// Worker-side PIL stats (canonical primary source).
		zap.String("worker_phash_hex", workerPhash),
		zap.Float64("worker_white_pct", resp.WhitePct),
		zap.Float64("worker_variance", resp.Variance),
		zap.Float64("worker_edge_density", resp.EdgeDensity),

		// Go-side recompute (cross-validation).
		zap.String("go_phash_hex", goRecomputePhash),
		zap.Float64("go_white_pct", pixelStats.WhitePct),
		zap.Float64("go_variance", pixelStats.Variance),
		zap.Float64("go_edge_density", pixelStats.EdgeDensity),

		// Shape-parity flag: true iff BOTH sides produced a
		// well-formed 16-char lowercase hex pHash string.
		// Bit-equality is intentionally NOT asserted (worker
		// and Go use different sampling strides).
		zap.Bool("phash_parity_ok", phashParityOK),
		zap.Bool("compute_stats_ok", statsErr == nil),
	)

	// If the Go-side stats fail to compute (e.g. file removed between
	// ext and compute), we still surface the value in the log via
	// compute_stats_ok=false. We do NOT fail the generation — the
	// worker is canonical and Validate already passed.
	if statsErr != nil {
		p.log.Warn("ChromeImageProvider: visual_validate.ComputeStats recompute failed (worker stats remain canonical)",
			zap.String("output_path", outputPath), zap.Error(statsErr))
	}

	// SourceHash uses REAL dims (so a 1920x1080 request that comes back
	// 1280x720 reuses the same hash as a direct 1280x720 request — the
	// downstream ingestion path is dim-correct).
	sourceHash := ComputeSourceHash("google-slides", req.Prompt, req.Style, realW, realH, generated.CanonicalGoogleSlidesModel)

	return &GeneratedImage{
		Data:       data,
		Format:     format,
		Width:      realW,
		Height:     realH,
		PromptUsed: req.Prompt,
		Provider:   "google-slides",
		SourceHash: sourceHash,
		OutputPath: outputPath,
	}, nil
}
