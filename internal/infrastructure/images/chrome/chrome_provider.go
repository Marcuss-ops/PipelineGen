// Package images — chrome_provider.go (PR-CHROME-PROVIDER-SPLIT, 2026-07 + commit 5):
// PUBLIC SURFACE for the ChromeImageProvider.
//
// After the commit 5 split, this file contains ONLY:
//
//   - ChromeImageProvider struct (the persistent worker handle).
//   - NewChromeImageProvider (2-arg constructor; pre-extraction 3-arg
//     signature was retired per godlike/07 no-fake-availability — the
//     legacy numProfiles parameter was silently ignored, single-profile
//     is canonical).
//   - Compile-time appimages.ImageGenerator assertion.
//   - Generate (THIN orchestrator: ctx cancel check → lock → delegate
//     to chrome_provider_retry.go::retryGenerationOnce).
//   - generateOnce (per-attempt body that delegates to the six
//     single-purpose helper files below).
//
// Concern-route map (godlike/06 SSOT — each file owns ONE concern):
//
//   - chrome_provider_ids.go        — correlation-token generators
//     (generateUUIDv4 + generateRequestID).
//   - chrome_provider_retry.go      — retry-once seam
//     (shouldRetryWorkerFailure + retryGenerationOnce).
//   - chrome_provider_request.go    — payload composition + path resolution
//     (buildWorkerGenerateRequest + resolveOutputPath).
//   - chrome_provider_response.go   — worker-error handling
//     (handleWorkerError + readGeneratedOutput + cleanupFailedOutput).
//   - chrome_provider_validation.go — post-success observability + content validation
//     (validateGeneratedOutput + decodeGeneratedDimensions +
//     buildGeneratedImage + logGenerationDiagnostics +
//     computeGenerationLogContext + GenerationLogContext +
//     ComposedPrompt + isWellFormedPhashHex).
//   - slide_worker_process.go       — subprocess lifecycle happy path
//     (ensureStarted + Stop).
//   - slide_worker_recovery.go      — dead-process detection + state reset
//     (isDeadWorkerError + resetWorker).
//   - slide_worker_protocol.go      — JSONL wire layer
//     (writeJSON + readResponse + readRawResponse +
//     workerResponse + mapToStruct; UNCHANGED).
//
// godlike/07 no-fake-availability invariants (preserved byte-byte from
// pre-extraction):
//
//   - numProfiles constructor arg REMOVED (single-profile is canonical,
//     the legacy 3-arg constructor silently ignored it).
//   - cooldowns map[int]int64 field REMOVED (single-profile has no
//     per-profile routing to spread load onto; the bookkeeping was
//     fake-availability).
//   - On worker-failure (resp.Status != "ok" OR visual_validate failure),
//     the file is REMOVED before the typed error propagates so the
//     next retry lands on a clean output path.
//
// Architecture (preserved from FASE 7, June 2026):
//
//	ChromeImageProvider.Generate()
//	  ├── ensureStarted()           [slide_worker_process.go]
//	  ├── stdin  ← JSON requests
//	  └── stdout → JSON responses
//	        └── os.ReadFile(output) → appimages.GeneratedImage{Data, Format, ...}
//
// ONE browser, ONE page, reused across ALL requests in the process lifetime.
package chrome

import (
	"bufio"
	"context"
	"fmt"
	appimages "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	"go.uber.org/zap"
)

// Compile-time assertion: ChromeImageProvider implements appimages.ImageGenerator.
var _ appimages.ImageGenerator = (*ChromeImageProvider)(nil)

// ChromeImageProvider implements appimages.ImageGenerator by delegating to the
// persistent Playwright-based Python worker that automates Google
// Slides AI (Nano Banana Pro).
//
// Thread safety: a single mutex serialises stdin/stdout access.
//
// PR-CHROME-PROVIDER-SPLIT (2026-07, commit 5): the legacy
// `cooldowns map[int]int64` field is RETIRED — single-profile has
// no per-profile routing; the tracking was fake-availability.
type ChromeImageProvider struct {
	scriptsDir string
	profileID  int
	log        *zap.Logger

	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Scanner
	stdoutPipe io.ReadCloser
	started    bool
}

// NewChromeImageProvider creates a new ChromeImageProvider.
// The worker is NOT started until the first Generate() call (lazy init).
//
// PR-CHROME-PROVIDER-SPLIT (2026-07): the legacy `numProfiles int` arg
// is REMOVED. The pre-split constructor silently ignored it; single-
// profile is the canonical policy. godlike/07 no-fake-availability
// demands the constructor not advertise a parameter it doesn't honor.
func NewChromeImageProvider(scriptsDir string, profileID int, log *zap.Logger) *ChromeImageProvider {
	return &ChromeImageProvider{
		scriptsDir: scriptsDir,
		profileID:  profileID,
		log:        log,
	}
}

// Generate produces an AI-generated image by delegating to the
// persistent Playwright worker. Blocks until the image is generated
// and saved to disk, then reads the file and returns the bytes.
//
// PR-CHROME-PROVIDER-SPLIT (commit 5, 2026-07): the pre-extraction
// inline 4-condition retry-once block (~40 LOC) is delegated to
// chrome_provider_retry.go::retryGenerationOnce. This method is now
// a thin orchestrator: context cancel check → lock → delegate.
//
// godlike/06 SSOT: the public Generate surface is the SINGLE
// entry point callers use. All retry / fail-closed / classification
// logic lives one level deeper in retryGenerationOnce.
func (p *ChromeImageProvider) Generate(ctx context.Context, req appimages.GenerateImageRequest) (*appimages.GeneratedImage, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("chrome provider: context cancelled before generation: %w", ctx.Err())
	default:
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.retryGenerationOnce(ctx, req)
}

// generateOnce executes a single Generate attempt: ensure worker is
// started, send the generate request, read the response, validate the
// bytes, and return the image. Returns a typed error if the subprocess
// dies or the response is unfavourable; the broader retry-once seam
// (chrome_provider_retry.go) decides whether to recover via
// resetWorker + re-attempt.
//
// PR-CHROME-PROVIDER-SPLIT (commit 5, 2026-07): the long inline block
// is delegated to single-purpose helpers per concern:
//
//   - identity (request_id, generation_id) → generateUUIDv4 +
//     resolveOutputPath + buildWorkerGenerateRequest
//   - request send / response read → p.writeJSON + p.readResponse
//     (slide_worker_protocol.go; unchanged)
//   - worker-error path → p.handleWorkerError
//   - validation → p.validateGeneratedOutput + decodeGeneratedDimensions
//   - observability → computeGenerationLogContext + p.logGenerationDiagnostics
//
// Must be called while p.mu is held (caller locks).
func (p *ChromeImageProvider) generateOnce(ctx context.Context, req appimages.GenerateImageRequest) (*appimages.GeneratedImage, error) {
	if err := p.ensureStarted(ctx); err != nil {
		return nil, fmt.Errorf("chrome provider: %w", err)
	}

	requestID := fmt.Sprintf("gen-%d", time.Now().UnixNano()%1_000_000_000)
	outputPath := resolveOutputPath(req, requestID)
	generationID := generateUUIDv4()
	composed := appimages.ComposePrompt(req.Prompt, req.Style, req.NegativePrompt)
	workerReq := buildWorkerGenerateRequest(requestID, generationID, req, composed.Composed, outputPath)

	// Send.
	if err := p.writeJSON(workerReq); err != nil {
		if p.isDeadWorkerError(err) {
			p.log.Warn("ChromeImageProvider: broken pipe on write, resetting worker", zap.Error(err))
			p.resetWorker()
			return nil, fmt.Errorf("chrome provider: worker died (broken pipe): %w", err)
		}
		return nil, fmt.Errorf("chrome provider: failed to send generate request: %w", err)
	}

	// Read response.
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
		return nil, p.handleWorkerError(resp, outputPath, requestID)
	}

	// Read the generated PNG.
	data, err := readGeneratedOutput(outputPath)
	if err != nil {
		return nil, fmt.Errorf("chrome provider: failed to read generated image at %s: %w", outputPath, err)
	}

	// P0.2 visual_validate pass — FAIL-CLOSED on blank/placeholder.
	if valErr := p.validateGeneratedOutput(outputPath, req.Style, requestID); valErr != nil {
		return nil, valErr
	}

	// Decode real dimensions from the PNG header (cheap). Real
	// dims become the appimages.GeneratedImage.Width/Height; the requested
	// w/h are preserved in the post-success observability log so
	// operators can audit requested-vs-actual ratio drift without
	// code changes.
	realW, realH, ratioMatch := decodeGeneratedDimensions(data, req.Width, req.Height)

	// P2 (July 2026): replicate the worker's diagnostic stats in the
	// structured Zap log. The pre-extraction inline block was 16+ lines
	// of zap field binding; post-extraction, the typed
	// GenerationLogContext + logGenerationDiagnostics split handles
	// the same observation surface via SSOT field shapes.
	composedTyped := ComposedPrompt{
		Composed:      composed.Composed,
		ComposedLen:   composed.ComposedLen,
		StyleAffix:    composed.StyleAffix,
		NegativeAffix: composed.NegativeAffix,
		WasCompressed: composed.WasCompressed,
	}
	logCtx, _ := computeGenerationLogContext(
		requestID, generationID, req, composedTyped,
		resp, data, outputPath, realW, realH, ratioMatch,
	)
	p.logGenerationDiagnostics(logCtx)
	if !logCtx.ComputeStatsOK {
		p.log.Warn("ChromeImageProvider: visual_validate.ComputeStats recompute failed (worker stats remain canonical)",
			zap.String("output_path", outputPath))
	}

	// SourceHash uses REAL dims so a 1920x1080 request that comes back
	// 1280x720 reuses the same hash as a direct 1280x720 request — the
	// downstream ingestion path is dim-correct.
	format := "png"
	sourceHash := appimages.ComputeSourceHash("google-slides", req.Prompt, req.Style, realW, realH, generated.CanonicalGoogleSlidesModel)
	return buildGeneratedImage(data, outputPath, req, realW, realH, sourceHash, format), nil
}
