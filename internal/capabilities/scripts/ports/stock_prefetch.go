package ports

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// StockPrefetcher starts acquisition for the stock bindings already present
// in a script.generate payload. The implementation owns cache and Drive
// details; the script capability only coordinates its lifetime.
//
// Prefetch is deliberately best-effort. A failed source is reported by the
// implementation and the script binding remains the source of truth for the
// downstream availability gate.
type StockPrefetcher interface {
	Prefetch(context.Context, []scriptpkg.StockBindingInput) StockPrefetchReport
}

type StockPrefetchReport struct {
	Requested int
	Warmed    int
	Cached    int
	Failed    int
	Skipped   int
}
