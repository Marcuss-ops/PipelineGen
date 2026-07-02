// Package images — chrome_provider.go implements ImageGenerator via a
// persistent Playwright worker process (slide_worker.py).
//
// Architecture (FASE 7, June 2026):
//
//	ChromeImageProvider.Generate()
//	  ├── ensureStarted()
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
// Single profile:
//
// The provider launches slide_worker.py in enforced single-profile mode.
// The Go-side mutex serialises stdin/stdout and the Python worker keeps one
// persistent Slides session alive for the whole process.
package images

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
type ChromeImageProvider struct {
	scriptsDir string
	log        *zap.Logger

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	started bool

	// FASE 10 (June 2026): per-profile cooldown tracking.
	// Map of profile ID → cooldown expiry (Unix nano). When a profile
	// hits a quota/auth error, it's cooled down for 60s before reuse.
	cooldowns map[int]int64
}

// NewChromeImageProvider creates a new ChromeImageProvider.
// The worker is NOT started until the first Generate() call (lazy init).
func NewChromeImageProvider(scriptsDir string, numProfiles int, log *zap.Logger) *ChromeImageProvider {
	_ = numProfiles
	return &ChromeImageProvider{
		scriptsDir: scriptsDir,
		log:        log,
		cooldowns:  make(map[int]int64),
	}
}

// Generate produces an AI-generated image by delegating to the persistent
// Playwright worker. Blocks until the image is generated and saved to disk,
// then reads the file and returns the bytes.
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
	// Only clean up temp files; canonical workspace files are managed by the caller.
	if req.OutputPath == "" {
		defer os.Remove(outputPath)
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
		classified := ClassifyError(errMsg)
		// Track cooldown for the profile that returned the error (if known).
		if resp.Profile != nil {
			if errors.Is(classified, ErrImageGenQuota) || errors.Is(classified, ErrImageGenAuth) {
				p.cooldowns[*resp.Profile] = time.Now().UnixNano() + 60*int64(time.Second)
				p.log.Warn("ChromeImageProvider: profile in cooldown",
					zap.Int("profile", *resp.Profile),
					zap.String("error", errMsg),
				)
			}
		}
		return nil, classified
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
		OutputPath: req.OutputPath,
	}, nil
}

// ── Subprocess lifecycle ──────────────────────────────────────────────────

// ensureStarted launches the persistent worker if not already running.
// Must be called while p.mu is held.
func (p *ChromeImageProvider) ensureStarted(ctx context.Context) error {
	if p.started {
		return p.healthCheck()
	}

	scriptPath := filepath.Join(p.scriptsDir, "bridges", "slide_worker.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("slide_worker.py not found at %s", scriptPath)
	}

	p.log.Info("ChromeImageProvider: launching persistent worker",
		zap.String("script", scriptPath))

	// Create the subprocess in single-profile mode.
	// We use exec.Command (not CommandContext) because the worker outlives
	// individual request contexts.
	p.cmd = exec.Command("python3", scriptPath, "--profiles", "1")

	var err error
	p.stdin, err = p.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	p.stdout = bufio.NewScanner(stdoutPipe)
	// The worker writes one JSON line per response; the default 64 KB
	// bufio buffer is ample — a response is at most ~500 bytes.

	// Route stderr to a file for post-mortem debugging.
	stderrPath := filepath.Join(os.TempDir(), "slide_worker_stderr.log")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		p.log.Warn("ChromeImageProvider: failed to create stderr log", zap.Error(err))
		p.cmd.Stderr = os.Stderr
	} else {
		p.cmd.Stderr = stderrFile
		// The child process holds a reference to the file descriptor.
		// Once cmd.Start() succeeds, the OS will close it when the child exits.
		// If cmd.Start() fails, we close it here to avoid a leak.
		defer stderrFile.Close()
	}

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start worker: %w", err)
	}

	p.started = true
	p.log.Info("ChromeImageProvider: worker process started",
		zap.Int("pid", p.cmd.Process.Pid))

	// Send warmup command and wait for "ready".
	if err := p.writeJSON(map[string]any{"action": "warmup"}); err != nil {
		return fmt.Errorf("warmup request failed: %w", err)
	}

	resp, err := p.readRawResponse()
	if err != nil {
		return fmt.Errorf("warmup response failed: %w", err)
	}
	if resp["status"] != "ready" {
		return fmt.Errorf("warmup: expected status=ready, got %v", resp["status"])
	}

	p.log.Info("ChromeImageProvider: worker warmup complete, ready for generation")
	return nil
}

// healthCheck sends a health action to the worker.
// Must be called while p.mu is held.
func (p *ChromeImageProvider) healthCheck() error {
	if err := p.writeJSON(map[string]any{"action": "health"}); err != nil {
		return fmt.Errorf("health check: write failed: %w", err)
	}
	resp, err := p.readRawResponse()
	if err != nil {
		return fmt.Errorf("health check: read failed: %w", err)
	}
	if resp["status"] != "ok" {
		return fmt.Errorf("worker unhealthy: %v", resp["error"])
	}
	return nil
}

// Health reports whether the persistent worker and all profiles are alive.
// Returns nil (healthy) or an error describing the problem. Used by
// diagnostics (FASE 10, June 2026).
func (p *ChromeImageProvider) Health() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started {
		return fmt.Errorf("worker not started")
	}
	return p.healthCheck()
}

// ActiveCooldownProfiles returns the count of profiles currently in cooldown.
func (p *ChromeImageProvider) ActiveCooldownProfiles() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UnixNano()
	count := 0
	for _, expiry := range p.cooldowns {
		if expiry > now {
			count++
		}
	}
	return count
}

// ── JSON protocol helpers ─────────────────────────────────────────────────

// writeJSON marshals and writes a JSON object line to the worker's stdin.
// Must be called while p.mu is held.
func (p *ChromeImageProvider) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	_, err = fmt.Fprintf(p.stdin, "%s\n", data)
	return err
}

// readResponse reads a response line from the worker and expects it to
// match the given requestID.
func (p *ChromeImageProvider) readResponse(expectedID string) (*workerResponse, error) {
	raw, err := p.readRawResponse()
	if err != nil {
		return nil, err
	}
	var resp workerResponse
	if err := mapToStruct(raw, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (raw=%v)", err, raw)
	}
	if resp.ID != expectedID {
		return nil, fmt.Errorf("response ID mismatch: expected %s, got %s", expectedID, resp.ID)
	}
	return &resp, nil
}

// readRawResponse reads the next JSON line from the worker's stdout.
func (p *ChromeImageProvider) readRawResponse() (map[string]any, error) {
	if !p.stdout.Scan() {
		err := p.stdout.Err()
		if err == nil {
			err = fmt.Errorf("worker stdout closed unexpectedly (process may have exited)")
		}
		// Try to collect stderr if the worker crashed.
		if p.cmd != nil && p.cmd.ProcessState != nil {
			return nil, fmt.Errorf("%w (exit code: %d)", err, p.cmd.ProcessState.ExitCode())
		}
		return nil, err
	}
	line := p.stdout.Text()
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON from worker: %w (line=%s)", err, line)
	}
	return raw, nil
}

// ── Types ─────────────────────────────────────────────────────────────────

// workerResponse is the JSON shape the Python worker writes to stdout.
type workerResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms,omitempty"`
	Bytes     int    `json:"bytes,omitempty"`
	Profile   *int   `json:"profile,omitempty"`
}

// mapToStruct round-trips a map through JSON to populate a struct.
func mapToStruct(m map[string]any, v any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// Stop gracefully shuts down the persistent worker. Sends the quit command
// over stdin, waits up to 5 seconds for the process to exit, then kills it.
//
// Idempotent: safe to call even if the worker was never started.
func (p *ChromeImageProvider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return nil
	}

	p.log.Info("ChromeImageProvider: stopping worker")
	// Best-effort quit; the worker may already be dead.
	_ = p.writeJSON(map[string]any{"action": "quit"})

	if p.stdin != nil {
		_ = p.stdin.Close()
	}

	if p.cmd != nil && p.cmd.Process != nil {
		// Wait with timeout: if the worker doesn't exit within 5 seconds,
		// send SIGKILL to prevent shutdown from hanging forever.
		done := make(chan error, 1)
		go func() {
			done <- p.cmd.Wait()
		}()
		select {
		case err := <-done:
			if err != nil {
				p.log.Warn("ChromeImageProvider: worker exited with error", zap.Error(err))
			}
		case <-time.After(5 * time.Second):
			p.log.Warn("ChromeImageProvider: worker did not exit within 5s — killing")
			_ = p.cmd.Process.Kill()
			<-done // drain the Wait() goroutine
		}
	}

	p.started = false
	p.log.Info("ChromeImageProvider: worker stopped")
	return nil
}
