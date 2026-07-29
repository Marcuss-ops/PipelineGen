// Package stockpipeline — source_ports.go (PR-SPLIT-STOCK-PORTS, July 2026).
//
// Owns the source-discovery port surface (Pattern 0 narrow interface for
// YouTube channel listing). Extracted from ports.go per godlike/06 SSOT
// one-canonical-owner-per-fact: this file is the SOLE canonical owner of
// the ChannelLister interface + the var _ compile-time pin that locks
// the *downloader.YTDLPDownloader concrete to the port.
//
// Wiring happens at the composition root (internal/app/wire_*.go); the
// old `s.ytdlp.ListChannel` direct call in query.go is RETIRED.
package types

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

// ChannelLister is the narrow port for YouTube channel listing
// (P4, July 2026). The concrete `*downloader.YTDLPDownloader` satisfies
// this interface structurally; wiring happens at the composition root.
// The old `s.ytdlp.ListChannel` direct call in query.go is RETIRED.
type ChannelLister interface {
	ListChannel(ctx context.Context, channelURL string, limit int) ([]downloader.VideoInfo, error)
}

// Compile-time assertion: *downloader.YTDLPDownloader satisfies
// ChannelLister. Signature drift on ListChannel is a build
// failure rather than a runtime panic (godlike/06 SSOT).
var _ ChannelLister = (*downloader.YTDLPDownloader)(nil)
