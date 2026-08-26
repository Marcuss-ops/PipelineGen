// Package stockpipeline — ports.go slim orchestrator (PR-SPLIT-STOCK-PORTS, July 2026).
//
// Per PR6 spec (Pattern 0 + Pattern 8): the application layer decides WHICH
// clips receive transitions/effects and WHAT the encoding policy is.
// It does NOT know how FFmpeg builds the filter_complex, runs the binary, or
// assembles the codec args — all that lives in the infrastructure layer
// behind the canonical typed ports declared in this directory.
//
// Import-boundary invariant (verified by `go vet`):
//
//	go vet ./internal/capabilities/assets/providers/stock/...
//
// must NOT import `internal/infrastructure/media/ffmpeg` OR
// `internal/platform/process`. Both are infra concerns; the app layer
// only depends on the typed ports declared in the companion files below.
//
// Port surface split (per godlike/06 SSOT one-canonical-owner-per-fact):
//
//   - render_ports.go — application-layer render + cutter + transition
//   - clip surface (StockRenderer + VideoCutter + their
//     DTOs + the TransitionRegistry catalog + the Clip
//     DTO + the noOp test fixtures + the 2 var _ pins
//     for the no-op concretes).
//   - source_ports.go — source-discovery narrow port (ChannelLister for
//     YouTube channel listing + the var _ pin locking
//     *downloader.YTDLPDownloader to the port).
//   - job_ports.go    — job-side narrow infra ports (3 narrow Pattern 0
//     interfaces scoped to the methods the stock
//     pipeline actually invokes: stockAssetIndexUpserter
//   - stockClipsSearchTermUpdater + stockChunkDispatcher).
package stockpipeline

import ()
