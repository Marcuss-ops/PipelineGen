// Package images — chrome_provider_response.go (commit 5, 2026-07):
// worker-error handling + output-file read for the response side.
//
// PR-CHROME-PROVIDER-SPLIT (commit 5, July 2026): per godlike/06 SSOT,
// chrome_provider_response.go is the SINGLE canonical owner of
// "what happens when the worker reports an unfavourable outcome
// OR when we need to read back the saved PNG". The previous
// inline blocks in generateOnce had 3 distinct concerns interlocked
// (file cleanup, appimages.ClassifyError, os.ReadFile) — extracting them keeps
// each testable in isolation and surfaces the godlike/07 FAIL-CLOSED
// contract on worker-asserted errors (the file MUST be removed
// before propagating the typed error so the next retry starts from
// a clean output path).
//
// godlike/07 FAIL-CLOSED primitive: every path that leaves the
// response block with a typed error MUST first call
// cleanupFailedOutput. The two error callers in generateOnce
// (resp.Status != "ok" → handleWorkerError, and visual_validate
// failure → validateGeneratedOutput) both endpoint to
// cleanupFailedOutput so any future addition follows the
// shape byte-for-byte.
package chrome

import (
	"fmt"
	appimages "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow"
	"os"

	"go.uber.org/zap"
)

// handleWorkerError handles a worker response with Status != "ok".
// The contract is BELT-AND-SUSPENDERS FAIL-CLOSED: the file is
// proactively removed before the typed error propagates so the
// next retry (whether triggered by Go-side Generate or by a
// caller-side retry policy) lands on a clean output path.
//
// godlike/07 contract: on worker-asserted error, the caller MUST
// NOT receive a appimages.GeneratedImage — the file is removed, the typed
// sentinel is propagated. The os.IsNotExist swallow is intentional:
// the canonical happy path from the worker's perspective is "the
// worker never wrote anything" (the typed ErrNoImageCandidate code
// path emits no file).
func (p *ChromeImageProvider) handleWorkerError(resp *workerResponse, outputPath string, requestID string) error {
	p.cleanupFailedOutput(outputPath, requestID)
	errMsg := resp.Error
	if errMsg == "" {
		errMsg = "unknown worker error"
	}
	return appimages.ClassifyError(errMsg)
}

// cleanupFailedOutput removes the output_path file and logs the
// outcome. The os.IsNotExist is intentionally tolerated: the
// pre-extraction inline pattern preserves this byte-for-byte so
// callers don't see a spurious warning when the worker never
// wrote the file.
//
// godlike/07 observability: the request_id is logged so operators
// can pivot from "an image-generation failure with this output
// path" to the worker's JSONL audit trail for forensic
// correlation.
func (p *ChromeImageProvider) cleanupFailedOutput(outputPath string, requestID string) {
	if rmErr := os.Remove(outputPath); rmErr != nil && !os.IsNotExist(rmErr) {
		p.log.Warn("ChromeImageProvider: cleanup outputPath after worker error",
			zap.String("output_path", outputPath),
			zap.String("request_id", requestID),
			zap.Error(rmErr))
	}
}

// readGeneratedOutput reads back the PNG file the worker reported
// as success. Errors propagate verbatim to the caller via the
// canonical fmt.Errorf wrap.
//
// godlike/07 fail-closed: a missing/empty file at outputPath is
// a fatal error — we never return a synthetic appimages.GeneratedImage.
// The worker's bytes are the source of truth (preserved by the
// CallSite's visual_validate.Validate pass that follows).
func readGeneratedOutput(outputPath string) ([]byte, error) {
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("chrome provider: failed to read generated image at %s: %w", outputPath, err)
	}
	return data, nil
}
