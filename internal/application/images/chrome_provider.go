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
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	"go.uber.org/zap"
)

// Compile-time assertion: ChromeImageProvider implements ImageGenerator.
var _ ImageGenerator = (*ChromeImageProvider)(nil)

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

	// Send the generate request over stdin.
	workerReq := map[string]any{
		"action": "generate",
		"id":     requestID,
		"prompt": req.Prompt,
		"output": outputPath,
	}
	if err := p.writeJSON(workerReq); err != nil {
		return nil, fmt.Errorf("chrome provider: failed to send generate request: %w", err)
	}

	// Read and parse the response.
	resp, err := p.readResponse(requestID)
	if err != nil {
		return nil, fmt.Errorf("chrome provider: failed to read worker response: %w", err)
	}

	if resp.Status != "ok" {
		errMsg := resp.Error
		if errMsg == "" {
			errMsg = "unknown worker error"
		}
		// Classify the error for typed retry decisions (FASE 10).
		// PR-CHROME-PROVIDER-SPLIT (2026-07-04): the pre-split
		// `if resp.Profile != nil { p.cooldowns[*resp.Profile] = ... }` block
		// is REMOVED. The classified error is propagated to the caller as
		// before; only the per-profile cooldown bookkeeping is retired
		// (godlike/07 no-fake-availability — single-profile policy has no
		// per-profile routing to spread the load onto).
		return nil, ClassifyError(errMsg)
	}

	// Read the generated image file.
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("chrome provider: failed to read generated image at %s: %w", outputPath, err)
	}

	format := "png"
	p.log.Info("ChromeImageProvider: generated image",
		zap.String("request_id", requestID),
		zap.String("prompt", req.Prompt),
		zap.Int("bytes", len(data)),
		zap.Int64("elapsed_ms", resp.ElapsedMS),
	)

	// ── Compute idempotency SourceHash (FASE 10) ────────────────────
	sourceHash := ComputeSourceHash("google-slides", req.Prompt, req.Style, req.Width, req.Height, generated.CanonicalGoogleSlidesModel)

	return &GeneratedImage{
		Data:       data,
		Format:     format,
		Width:      req.Width,
		Height:     req.Height,
		PromptUsed: req.Prompt,
		Provider:   "google-slides",
		SourceHash: sourceHash,
		OutputPath: outputPath,
	}, nil
}
