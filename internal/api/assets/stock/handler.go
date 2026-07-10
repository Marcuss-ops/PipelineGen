package stock

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// StockAssetLookup is the narrow port for looking up a single media
// asset by ID. Satisfied by *assets.ClipsRepository.Get (returns
// *asset.Asset). Pattern 0 typed port — the stock package stays
// free of infrastructure imports.
type StockAssetLookup interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
}

// StockDriveReader is the narrow port for streaming files from Google
// Drive. Satisfied by drive.Reader (DownloadFile + GetFileMeta).
// Pattern 0 typed port.
type StockDriveReader interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
	GetFileMeta(ctx context.Context, fileID string) (*DriveFileMeta, error)
}

// DriveFileMeta is a minimal mirror of drive.FileMeta so the stock
// package stays free of infrastructure/drive imports. Exported so the
// composition-root adapter can construct it.
//
// godlike/06 SSOT (one canonical owner per fact): the `Size int64`
// field is populated by the composition-root adapter from
// `drive.FileMeta.Size`, which the underlying *Uploader.GetFileMeta
// fetches in the SAME Drive API call (single files.get?fields=
// "...,size,..." round-trip — zero additional network cost). The
// canonical 2GB cap (`MaxStockDownloadSize`) gates downstream
// DownloadFile streaming — see `ErrStockDownloadOversized` below
// (PR-STOCK-OVERSIZED-DOWNLOAD-GUARD, 2026-07-08).
type DriveFileMeta struct {
	MimeType string
	Size     int64
}

// MaxStockDownloadSize is the canonical upper bound on stock clip
// downloads. 2 binary GB (= 2 << 30 bytes) covers any legitimate
// stock video clip (5-second stock snippets at typical bitrates are
// <500MB) while rejecting pathological inputs (oversized garbage
// flagged as video/mp4 via MIME bypass attempts).
//
// godlike/06 SSOT: this constant is the SOLE owner of the cap value;
// tests + production code MUST reference it by symbol (NOT by hard-
// coded 2GB literal) so future cap changes propagate uniformly.
//
// godlike/07 NO-FAKE-AVAILABILITY: the cap is fail-closed at the file
// size guard boundary (returns 413 BEFORE the DownloadFile streaming
// call opens — operators never see a wasted Drive bandwidth charge).
const MaxStockDownloadSize int64 = 2 << 30 // 2 GiB

// ErrStockDownloadOversized is the canonical typed sentinel for the
// size-guard gate in DownloadStockClip.
//
// godlike/07 typed-error contract: callers probe via
// `errors.Is(err, stock.ErrStockDownloadOversized)` — the typed
// sentinel is the SOLE contract surface (NO string-match fallback).
//
// godlike/06 SSOT: this sentinel lives ONLY in handler.go (the
// canonical SOLE owner of the size-gate contract). Composition-root
// adapters + downstream consumers MUST import this package to use
// errors.Is — NO re-declaration in adapters or wrappers.
//
// The byte-count suffix is computed via fmt.Errorf so future changes
// to MaxStockDownloadSize propagate naturally (a hardcoded literal
// "2 GiB" in the message string would lie if the constant were
// bumped; the runtime apiutil.Error response surfaces the actual
// size-vs-cap diagnostic separately).
var ErrStockDownloadOversized = fmt.Errorf(
	"stock download: drive file exceeds MaxStockDownloadSize (%d bytes); refusing to proxy",
	MaxStockDownloadSize)

// Handler is the api-layer adapter for the stock pipeline endpoints.
// After S2b it holds the use case + logger + optional download deps.
// All dispatch logic lives in stockpipeline.StockUseCase.
type Handler struct {
	useCase   *stockpipeline.StockUseCase
	log       *zap.Logger
	assetRepo StockAssetLookup // optional (nil → 503 on /clips/:id/download)
	driveRead StockDriveReader // optional (nil → 503 on /clips/:id/download)
}

// NewHandler constructs the api handler. Production wire-up builds a
// *stockpipeline.StockUseCase first (composition root, module_sources.go)
// and passes it in; test fixtures may pass nil for either dependency.
func NewHandler(useCase *stockpipeline.StockUseCase, log *zap.Logger, assetRepo StockAssetLookup, driveRead StockDriveReader) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		useCase:   useCase,
		log:       log,
		assetRepo: assetRepo,
		driveRead: driveRead,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Stock Pipeline routes")

	r.POST("/run", h.RunStockPipeline)
	r.POST("/search-and-run", h.SearchAndRun)
	r.POST("/clips/:id/download", h.DownloadStockClip)
}

// ── Per-endpoint files (AGENTS.md Pattern 5) ─────────────────────────
//
// Endpoint methods live in dedicated files:
//   handler_run.go            — POST /api/stock-pipeline/run
//   handler_search_and_run.go — POST /api/stock-pipeline/search-and-run
//   handler_download.go       — POST /api/stock-pipeline/clips/:id/download
//
// This file is the slim orchestrator: types, constants, sentinels,
// Handler struct, NewHandler, RegisterRoutes, validation helpers.

// stockValidationInput is the type-erased input to applyStockDefaults.
//
// godlike/06 SSOT: lives ONLY at handler.go (private to package).
// godlike/07 minimum-blast-radius: pure-data plain struct, no methods.
//
// SearchSourceCount is len(req.SearchQueries) for Run OR
// len(req.Queries) for SearchAndRun — both payload types carry a
// wire-shape list of search-source terms; the validator only cares
// about the count (≥1 requirement on any source field), not the
// element type. DirectURLs and DriveURLs use the same field name on
// both payload types. Clips is the shared []stockpipeline.ClipSpec
// slice; the helper inspects only Clip.URL to enforce the has-URL
// rule.
type stockValidationInput struct {
	SearchSourceCount int
	DirectURLsCount   int
	DriveURLsCount    int
	Clips             []stockpipeline.ClipSpec
	TotalMinutes      int
	ClipDuration      int
	Async             bool
}

// stockValidationDefaults is the canonical return shape.
//
// godlike/06 SSOT: lives ONLY at handler.go. The 3 fields populate
// back into the request struct after validation succeeds — the
// caller assigns them back to req before invoking
// FromRunPayload/FromSearchAndRunRequest so the application-layer
// converter sees the defaulted values verbatim.
type stockValidationDefaults struct {
	TotalMinutes int
	ClipDuration int
	Persist      bool
}

// applyStockDefaults enforces the canonical validation contract
// shared by POST /api/stock-pipeline/run AND POST
// /api/stock-pipeline/search-and-run (PR-STOCK-DRY-VALIDATION,
// 2026-07-08).
//
// godlike/06 SSOT: This helper lives ONLY at handler.go — the
// canonical SOLE owner. NO re-declaration in adapters or wrappers.
// The 2 pre-extraction validation blocks were textually identical
// EXCEPT for the "no sources" error message string:
//
//	/run pre-PR:               "search_queries, direct_urls, drive_urls, or clips required"
//	/search-and-run pre-PR:    "queries,         direct_urls, drive_urls, or clips required"
//
// The sourcesEmptyMsg parameter carries the contextual wire-field
// name difference.
//
// godlike/07 minimum-blast-radius: 0 signature drift, 0 new imports,
// 0 new dependencies. Pure refactor — every operator-readable wire
// error message is preserved byte-equivalent so the existing
// handler_test.go assertions on those literal strings continue to
// pass unchanged.
//
// Validation contract (preserves both pre-extraction blocks EXACTLY):
//
//  1. All 4 source fields empty → return sourcesEmptyMsg error.
//  2. Clips non-empty AND no clip with non-empty URL → return
//     "clips require at least one clip with a non-empty url".
//  3. TotalMinutes ≤ 0 → default 5.
//  4. ClipDuration < 0 → "clip_duration must be >= 0".
//  5. ClipDuration == 0 → default 10.
//  6. ClipDuration > 0 AND (ClipDuration < 3 OR > 30) →
//     "clip_duration must be between 3 and 30 seconds".
//  7. Async=false → Persist=true (sync mode enables the resilient
//     path so the runner completes upload + finalization + indexing
//     instead of stopping at the legacy manifest-only flow).
func applyStockDefaults(sourcesEmptyMsg string, in stockValidationInput) (stockValidationDefaults, error) {
	if in.SearchSourceCount == 0 && in.DirectURLsCount == 0 && in.DriveURLsCount == 0 && len(in.Clips) == 0 {
		return stockValidationDefaults{}, errors.New(sourcesEmptyMsg)
	}
	if len(in.Clips) > 0 {
		hasURL := false
		for _, clip := range in.Clips {
			if clip.URL != "" {
				hasURL = true
				break
			}
		}
		if !hasURL {
			return stockValidationDefaults{}, errors.New("clips require at least one clip with a non-empty url")
		}
	}
	out := stockValidationDefaults{TotalMinutes: in.TotalMinutes}
	if out.TotalMinutes <= 0 {
		out.TotalMinutes = 5
	}
	out.ClipDuration = in.ClipDuration
	if out.ClipDuration < 0 {
		return stockValidationDefaults{}, errors.New("clip_duration must be >= 0")
	}
	if out.ClipDuration == 0 {
		out.ClipDuration = 10
	}
	if out.ClipDuration > 0 && (out.ClipDuration < 3 || out.ClipDuration > 30) {
		return stockValidationDefaults{}, errors.New("clip_duration must be between 3 and 30 seconds")
	}
	out.Persist = !in.Async
	return out, nil
}
