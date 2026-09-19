package scriptgeneration

import (
	"context"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// runExecutionPhases owns the ordered business pipeline. Keeping the phase
// sequence separate from Runner wiring makes the resume/stop contract visible
// and keeps ExecuteWithContext small.
func (r *Runner) runExecutionPhases(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext) {
	e := &executionRun{
		r:     r,
		ctx:   ctx,
		runID: runID,
		req:   req,
		exec:  exec,
	}

	if !e.start() {
		return
	}
	stockPrefetchDone := r.startStockPrefetch(ctx, req.StockBindings)
	defer func() { r.waitStockPrefetch(stockPrefetchDone) }()
	if !e.normalize() {
		return
	}
	if !e.mediaPreflightPhase() {
		return
	}
	if !e.beginVidRushPhase() {
		return
	}
	// The VidRush coordinator wiring lives for the whole run: release it only
	// after every phase that consumes the fan-out completes (or fails).
	if e.coordinator != nil {
		defer r.endVidRush(runID)
	}
	if !e.generate() {
		return
	}
	r.waitStockPrefetch(stockPrefetchDone)
	stockPrefetchDone = nil
	e.ensureResult()
	if !e.translate() {
		return
	}
	if !e.sceneTextReady() {
		return
	}
	if !e.audioCompile() {
		return
	}
	if !e.persist() {
		return
	}
	if !e.documents() {
		return
	}
	e.complete()
}

// SetStockPrefetcher wires the best-effort acquisition hook for the stock
// bindings already present in the payload.
func (r *Runner) SetStockPrefetcher(prefetcher scriptports.StockPrefetcher) {
	if r != nil {
		r.stockPrefetcher = prefetcher
	}
}

func (r *Runner) startStockPrefetch(ctx context.Context, bindings []scriptpkg.StockBindingInput) chan scriptports.StockPrefetchReport {
	if r == nil || r.stockPrefetcher == nil || len(bindings) == 0 {
		return nil
	}
	copyBindings := append([]scriptpkg.StockBindingInput(nil), bindings...)
	done := make(chan scriptports.StockPrefetchReport, 1)
	go func() {
		done <- r.stockPrefetcher.Prefetch(ctx, copyBindings)
	}()
	return done
}

func (r *Runner) waitStockPrefetch(done chan scriptports.StockPrefetchReport) {
	if done == nil {
		return
	}
	report := <-done
	if r == nil || r.log == nil {
		return
	}
	r.log.Info("script.generate: stock prefetch completed",
		zap.Int("requested", report.Requested),
		zap.Int("warmed", report.Warmed),
		zap.Int("cached", report.Cached),
		zap.Int("failed", report.Failed),
		zap.Int("skipped", report.Skipped))
}
