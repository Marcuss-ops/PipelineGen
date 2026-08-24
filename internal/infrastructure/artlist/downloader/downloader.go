// Package downloader owns the Artlist HTTP/HLS download pipeline.
//
// STATO ATTUALE (CUTOVER, July 2026): the unified Resolver
// (resolver.go) is the SINGLE canonical owner of Artlist download
// routing. The legacy Provider (dual-path HLS vs HTTP routing) was
// RETIRED in PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY-CUTOVER. The
// composition root at build_bundles_artlist.go wires
// downloader.NewResolver(...) which consolidates all three
// transports (Node scraper / yt-dlp / HTTP) into one resolvePath
// decision per godlike/06 SSOT.
//
// This file retains ONLY the shared types (Config, mapError) that
// the Resolver reuses. Config is embedded by ResolverConfig;
// mapError is the canonical error-classification helper called
// from Resolver.Download.
//
// PROSSIMO STEP (forward-pointer, deadline 2026-08-15): remove
// the duplicated isArtlistURL / isDirectURL / isHLSURL +
// downloadViaScraper logic in processor_download.go — the
// mediaProcessor fallback should route through the Resolver
// instead of duplicating the detection + download logic.
//
// godlike/06 SSOT: this file + resolver.go together are the
// SINGLE canonical owner of "how to download an Artlist asset".
// Every call site routes through this package's Resolver type.
package downloader

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
)

// Config carries the wiring the application passes to New. All fields
// are optional; zero values fall back to the defaults below.
type Config struct {
	// MaxAttempts is the retry budget per Download call. Default: 3.
	MaxAttempts int
	// InitialBackoff is the delay before the first retry. Default: 1s.
	InitialBackoff time.Duration
	// MaxBackoff caps the exponential backoff. Default: 30s.
	MaxBackoff time.Duration
	// JitterFraction adds randomness in [0, 1) to each retry's sleep.
	// 0 disables jitter. Default: 0.
	JitterFraction float64
	// HTTPDownloadTimeout is the per-byte timeout for the progressive
	// HTTP fallback. Default: 5m.
	HTTPDownloadTimeout time.Duration
}

// mapError classifies an underlying transport error into one of the
// centralized artapp sentinels so callers branch on intent, not on
// transport jargon (yt-dlp stderr, http.Client codes, etc.).
func mapError(err error, isHLS bool) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(msg, "timeout"):
		return fmt.Errorf("%w: %v", artapp.ErrTimeout, err)
	case strings.Contains(msg, "bad status: 429"):
		// Rate limit: back off + retry, or surface to orchestrator so
		// it can try the next source.
		return fmt.Errorf("%w: %v", artapp.ErrTransportFallback, err)
	case strings.Contains(msg, "bad status: 404"), strings.Contains(msg, "no playable video"):
		return fmt.Errorf("%w: %v", artapp.ErrNotFound, err)
	case strings.Contains(msg, "bad status: 4"):
		return fmt.Errorf("%w: %v", artapp.ErrInvalidResponse, err)
	case strings.Contains(msg, "bad status: 5"):
		// 5xx usually signals the source is broken. Surface as transport
		// fallback so the orchestrator above this port can try the next
		// Downloader in the chain.
		return fmt.Errorf("%w: %v", artapp.ErrTransportFallback, err)
	}
	// Network errors, yt-dlp crashes, unknown → unavailable / transport
	// fallback. For HLS hangs we lean ErrTransportFallback because
	// yt-dlp's exit code is the more useful "try another downloader"
	// signal than "the source is malformed".
	if isHLS {
		return fmt.Errorf("%w: %v", artapp.ErrTransportFallback, err)
	}
	return fmt.Errorf("%w: %v", artapp.ErrUnavailable, err)
}
