// cmd/admin/qdrant_maintenance_scanner.go — qdrant scanner adapter.
//
// Translates internal/infrastructure/qdrant.ScrollResult into
// []legacyaudit.ScrollPoint so the application-layer audit package does
// not import the infra layer (godlike/06 SSOT: one owner per fact;
// legacyaudit owns the type, qdrant owns the wire).
//
// The adapter is single-goroutine (legacyaudit.Classify drives both
// ScrollPoints and NextOffset from one goroutine), so lastNextOffset
// is a plain field — no sync/atomic needed.
package main

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/legacyaudit"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// qdrantScannerAdapter is the single-goroutine adapter wiring
// qdrant.Client.ScrollPoints into the legacyaudit scanner contract.
type qdrantScannerAdapter struct {
	client         *qdrant.Client
	activeCol      string
	pageSize       int
	lastNextOffset string
}

// newQdrantScannerAdapter constructs the adapter wired against the
// canonical qdrant.Client. pageSize is hard-clamped to <= 1000 —
// Qdrant REST cannot return more than 1000 points per scroll page.
// 0 (zero) and negative values default to 500 (the canonical default).
// This clamping is the single source of truth for the scan-limit
// invariants; the orchestrator passes deps.Limit directly without
// pre-clamping.
func newQdrantScannerAdapter(client *qdrant.Client, activeCol string, pageSize int) *qdrantScannerAdapter {
	if pageSize <= 0 {
		pageSize = 500
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	return &qdrantScannerAdapter{client: client, activeCol: activeCol, pageSize: pageSize}
}

// ScrollPoints returns up to limit points from the next page and
// stashes the NextOffset in lastNextOffset so the classify loop's
// NextOffsetExtractor call can drive the cursor.
func (a *qdrantScannerAdapter) ScrollPoints(ctx context.Context, _ string, offset string, _ int) ([]legacyaudit.ScrollPoint, error) {
	if a.client == nil {
		return nil, errors.New("qdrantScannerAdapter: client is nil")
	}
	res, err := a.client.ScrollPoints(ctx, a.activeCol, offset, a.pageSize, nil)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	out := make([]legacyaudit.ScrollPoint, 0, len(res.Points))
	for _, p := range res.Points {
		out = append(out, legacyaudit.ScrollPoint{
			ID:      p.ID,
			Payload: p.Payload,
		})
	}
	a.lastNextOffset = res.NextOffset
	return out, nil
}

// NextOffset implements legacyaudit.NextOffsetExtractor.
func (a *qdrantScannerAdapter) NextOffset(_ []legacyaudit.ScrollPoint) string {
	return a.lastNextOffset
}

// Compile-time guards (godlike/06 audit-pin discipline: pins live next to
// the type they assert). The pre-existing `_ = sql.ErrNoRows` dead-code
// assertion that was at the bottom of the original monolithic file has
// been REMOVED per PR-QDRANT-MAINT-PER-MODE (godlike/07 minimum-blast-radius:
// the import is no longer used in any per-mode file, the speculative
// "future expansion of the apply loop" comment is not a real dependency,
// the assertion was guarding a hypothetical surface that never landed).
var (
	_ legacyaudit.QdrantScanner       = (*qdrantScannerAdapter)(nil)
	_ legacyaudit.NextOffsetExtractor = (*qdrantScannerAdapter)(nil)
)
