// Package scriptgeneration coordinates stock acquisition with durable script generation.
package scriptgeneration

import (
	"context"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

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
