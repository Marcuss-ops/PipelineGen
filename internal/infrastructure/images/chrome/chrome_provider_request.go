// Package images — chrome_provider_request.go (commit 5, 2026-07):
// request composition + output-path resolution for the worker
// JSONL payload.
//
// PR-CHROME-PROVIDER-SPLIT (commit 5, July 2026): per godlike/06 SSOT,
// chrome_provider_request.go is the SINGLE canonical owner of "the
// stdin-side payload shape" sent to slide_worker.py. The previous
// inline workerReq map literal in chrome_provider.go::generateOnce
// grew to ~30 fields across 4 sections (identity, prompt
// composition, dimensions, output path) — extracting it to a
// function-level operation makes each tag-band testable in
// isolation and prevents silent drift between Go-side
// construction + Python-side consumption.
//
// Invariant (godlike/06 SSOT): the JSON keys emitted here are the
// canonical set consumed by scripts/bridges/slide_worker.py
// (and the wave refactor's runtime/protocol.py model). Adding a
// new key here MUST be paired with a worker-side update; removing
// a key is a wire-protocol break and is forbidden outside the
// protocol-versioning flow.
package images

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolveOutputPath returns the canonical output path for the
// worker's saved PNG. The contract is a 2-tier preference:
//
//  1. Caller-supplied OutputPath (workspace-based ingest path).
//     Operators use this when the downstream image-ingestion
//     already expects the bytes at a specific location (e.g. the
//     Qdrant projection's docroot).
//  2. TempDir fallback (for callers that don't pre-allocate a
//     path — backward compat with the sync endpoints).
//
// requestID is plumbed through so the tempDir fallback names the
// file deterministically without colliding with concurrent calls.
func resolveOutputPath(req GenerateImageRequest, requestID string) string {
	if req.OutputPath != "" {
		return req.OutputPath
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("slide_gen_%s.png", requestID))
}

// buildWorkerGenerateRequest composes the map[string]any payload
// sent to slide_worker.py over stdin. The map shape MUST match the
// Python-side parsing in scripts/bridges/slide_worker.py (see the
// protocol spec in scripts/bridges/runtime/protocol.py::parse_request).
//
// Field bands (preserved byte-byte from the pre-extraction inline):
//
//   - Identity: action="generate", id=<request_id>, generation_id=<UUID v4>,
//     request_id=<same as id> alias per user spec (P1.1
//     July-29 review-feedback; spec-compliance emission).
//   - Prompt composition: prompt=<ComposedPrompt.Composed>,
//     prompt_original=<raw req.Prompt>. The worker fills the DOM
//     textarea with Composed and emits prompt_original in the JSONL
//     audit. See internal/application/images/prompt_composer.go
//     for the ComposePrompt rulebook (P1.2 retire-the-150-char-truncation).
//   - Style directives: style_id, optional prompt_suffix (forward-pointer
//     for callers that want a custom worker-side composition
//     format). prompt_suffix is plumb-through only — the
//     ComposePrompt tool handles the canonical [style: X] [negative:
//     do not include ...] format; prompt_suffix is for escape-hatch
//     cases.
//   - Dimensions: width / height (P1.1 wire-up). The worker uses
//     these for the 16:9 selection verification.
//   - Ratio override: optional ratio (P1.1). Empty defaults the
//     worker to 16:9.
//   - Output: output=<outputPath>, consumed by readGeneratedOutput.
func buildWorkerGenerateRequest(requestID, generationID string, req GenerateImageRequest, composedPrompt string, outputPath string) map[string]any {
	workerReq := map[string]any{
		"action":          "generate",
		"id":              requestID, // request_id correlation token (Go ↔ worker stdin/stdout)
		"generation_id":   generationID,
		"prompt":          composedPrompt,
		"prompt_original": req.Prompt, // raw user prompt for worker-side JSONL audit
		"negative_prompt": req.NegativePrompt,
		"style_id":        req.Style,
		"width":           req.Width,
		"height":          req.Height,
		"output":          outputPath,
		// P1.1 (July 2026, July-29 review-feedback): the user spec
		// explicitly asks for a `request_id` correlation field. The
		// canonical chrome_provider.go internal token is `id`; for
		// spec-compliance we ALSO emit `request_id` as an alias pointing
		// at the same string. The response reader consumes `id`; consumers
		// that pivot on `request_id` per the user spec see the same
		// value.
		"request_id": requestID,
	}
	// P1.1 (July 2026): forward prompt_suffix + ratio when non-empty.
	if req.PromptSuffix != "" {
		workerReq["prompt_suffix"] = req.PromptSuffix
	}
	if req.Ratio != "" {
		workerReq["ratio"] = req.Ratio
	}
	return workerReq
}
