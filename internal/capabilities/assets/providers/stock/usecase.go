// Package stock is the parent application-level orchestration layer for
// the stock video pipeline. It hosts the canonical Submit use case
// (SubmitStockPipelineUseCase) and the API→domain converter with
// input validation (FromAPIRequest). The runner / port types live in
// the stockpipeline child package.
//
// S2a (June 2026): re-exposes the canonical StockCommand and
// StockSearchAndRunRequest from stockpipeline through type aliases so
// the api layer binds JSON onto these types without a local mirror.
// The use case here is the rich-return coordinator that the api
// handler delegates to; pre-S2a the api handler drove dispatch
// inline (with a stray ListClipsPaged(10000) sync fallback that
// was removed in S2b).
package stock

import (
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
)

// ── Type aliases (SSOT preservation across the parent/child split) ──
//
// StockCommand is canonical in stockpipeline (the package that owns
// the runner + RunInput + ChunkMetadataInput shape). Re-exposed via
// type alias here so api-layer code can import `StockCommand`
// without duplicating the struct or risking drift on a future
// field add. The two paths resolve to the same underlying type at
// compile time — no wrapping, no projection, no copy.
type (
	StockCommand             = stockpipeline.StockCommand
	StockSearchAndRunRequest = stockpipeline.StockSearchAndRunRequest
	ChunkMetadataInput       = stockpipeline.ChunkMetadataInput
)

// ── SubmitResult ────────────────────────────────────────────────────────
//
// SubmitResult is the rich return shape spec'd in S2a. It carries the
// dispatch receipt (JobID, Status) on async paths and the runtime
// summary (TotalClips, TotalChunks, Chunks) on sync paths. Error is
// populated whenever the submit ends in a non-success terminal state;
// the returned error mirrors Error for errors.Is classification.
type SubmitResult struct {
	JobID       string                      `json:"job_id,omitempty"`
	Status      string                      `json:"status"`
	TotalClips  int                         `json:"total_clips,omitempty"`
	TotalChunks int                         `json:"total_chunks,omitempty"`
	Chunks      []stockpipeline.ChunkResult `json:"chunks,omitempty"`
	Error       string                      `json:"error,omitempty"`
}

// ── FromAPIRequest (api → StockCommand with validation) ──────────────
//
// FromAPIRequest converts the search-and-run request body into a
// StockCommand AND applies the canonical domain validation rules
// that previously lived inline in the api handler:
//
//   - ClipDuration: 0 means "no clip-duration override"; otherwise
//     must be in [3, 40] seconds. The pipeline rejects values outside
//     this range as unrunnable (clips < 3s are too short for
//     transitions / effects; clips > 40s exceed the stock contract).
//   - TotalMinutes: 0 (or negative) means "use default 5 minutes".
//
// Validation is co-located with the domain constructor so the api
// handler can pass raw user input through and rely on the result.
// Errors are surfaced to the api boundary as 400 Bad Request.
func FromAPIRequest(req *StockSearchAndRunRequest) (*StockCommand, error) {
	if req == nil {
		return nil, errors.New("stock: FromAPIRequest: nil *StockSearchAndRunRequest")
	}
	if req.ClipDuration != 0 && (req.ClipDuration < 3 || req.ClipDuration > 40) {
		return nil, fmt.Errorf("stock: clip_duration must be between 3 and 40 seconds (got %d)", req.ClipDuration)
	}
	if err := stockpipeline.ValidateDurationContract(req.TargetTotalDurationSeconds, req.TargetDurationPerSourceSeconds, req.ClipsPerSource, req.ClipDurationSeconds, req.DownloadMode); err != nil {
		return nil, err
	}
	// Mutate-in-place via a local copy so we don't surprise callers
	// who hold the request pointer post-binding.
	cloned := *req
	if cloned.TotalMinutes <= 0 {
		cloned.TotalMinutes = 5
	}
	cmd, err := stockpipeline.FromSearchAndRunRequest(&cloned)
	if err != nil {
		return nil, fmt.Errorf("stock: FromAPIRequest: %w", err)
	}
	return cmd, nil
}
