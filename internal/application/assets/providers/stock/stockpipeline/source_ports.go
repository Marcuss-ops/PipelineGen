// Package stockpipeline — source_ports.go (PR-REFACTOR-P0-IO-BINDER, July 2026).
//
// Owns the source-discovery port surface (Pattern 0 narrow interface for
// YouTube channel listing). Extracted from ports.go per godlike/06 SSOT
// one-canonical-owner-per-fact.
//
// Wiring happens at the composition root (internal/app/wire_*.go); the
// infrastructure adapter lives in internal/infrastructure/downloader/stock_adapter.go.
package stockpipeline

import "context"

// VideoInfo is the application-layer DTO for a YouTube channel
// listing result. Contains only the fields the stock pipeline
// consumes (godlike/07 minimum-blast-radius).
type VideoInfo struct {
	ID       string
	Title    string
	Duration float64
}

// ChannelLister is the narrow port for YouTube channel listing
// (P4, July 2026). The infrastructure adapter satisfies this
// interface; wiring happens at the composition root.
type ChannelLister interface {
	ListChannel(ctx context.Context, channelURL string, limit int) ([]VideoInfo, error)
}
