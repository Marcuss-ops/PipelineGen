// Package downloader — hls_segment.go: segment + AES-128 key HTTP download
// + retry typed-path (the network-layer concern).
//
// Split from hls_direct.go (commit refactor(downloader): split hls_direct.go
// into playlist/segment/ffmpeg concerns). This file owns the
// network-bound half of the Go-side HLS pipeline:
//
//   - fetchWithRetry: the canonical retry typed-path wrapper used for
//     ALL Go-side HLS GETs (playlist bytes via fetchPlaylist, segment
//     ciphertext, AES-128 key bytes). Delegates the
//     "what counts as transient" decision to pkg/retry.IsTransient +
//     retry.WrapTransient + retry.TransientInfrastructureError
//     (canonical typed-path #1).
//   - fetchKeyWithCache + keyCacheEntry: per-playlist AES-128 key
//     cache (key bytes + the resolved URL the bytes came from for
//     operator-facing audit logs).
//   - resolveIV: AES-128 IV resolution per HLS spec §4.3.2.4
//     (explicit IV= attribute OR media-sequence fallback).
//   - formatByteRangeHeader: HTTP Range: header value builder used
//     by fetchWithRetry when an EXT-X-BYTERANGE specifies a subset
//     of a segment URI.
//
// godlike/06 SSOT: this file owns the network-layer retry loop. The
// pre-Fix hand-rolled loop was a godlike/06 violation (duplicated
// the retry contract and the is-transient decision in
// isRetryableTransportError). The FASE 6 cutover centralised that
// decision at pkg/retry.IsTransient; this file wires the canonical
// classifier.
package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// fetchWithRetry performs a single HTTP GET with retry typed-path
// (canonical pkg/retry.Do + IsRetryable: retry.IsTransient — the
// typed-path #1 from pkg/retry/transient.go), per-fetch timeout
// (context.WithTimeout), and ctx cancellation. The returned body
// is the raw bytes (ciphertext if the caller is fetching a
// segment; the caller decides whether to decrypt).
//
// godlike/06 SSOT: this function routes through the canonical
// pkg/retry loop. The pre-Fix hand-rolled loop was a godlike/06
// violation (it duplicated the retry contract and the
// "is this transient?" decision in isRetryableTransportError);
// the FASE 6 cutover deliberately centralised that decision
// at pkg/retry.IsTransient.
func (f *HLSFetcher) fetchWithRetry(ctx context.Context, url string, perFetchTimeout time.Duration, byteRange HLSSegmentByteRange) ([]byte, error) {
	if perFetchTimeout <= 0 {
		perFetchTimeout = 30 * time.Second
	}
	maxAttempts := f.cfg.maxAttempts()
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	opts := retry.Options{
		MaxAttempts:    maxAttempts,
		InitialBackoff: 500 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		JitterFraction: 0.2,
		IsRetryable:    retry.IsTransient,
	}
	var body []byte
	err := retry.Do(ctx, func() error {
		fetchCtx, cancel := context.WithTimeout(ctx, perFetchTimeout)
		defer cancel()
		req, rErr := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
		if rErr != nil {
			return retry.WrapTransient(fmt.Errorf("hls_direct: new request %q: %w", url, rErr))
		}
		if byteRange.Begin > 0 || byteRange.End > 0 {
			req.Header.Set("Range", formatByteRangeHeader(byteRange))
		}
		req.Header.Set("User-Agent", "PipelineGen/HLS-Fetcher (Fase 8)")
		resp, fErr := f.client.Do(req)
		if fErr != nil {
			return retry.WrapTransient(fmt.Errorf("hls_direct: GET %q: %w", url, fErr))
		}
		defer resp.Body.Close()
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return retry.WrapTransient(fmt.Errorf("hls_direct: read body %q: %w", url, readErr))
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errMsg := fmt.Errorf("hls_direct: GET %q returned status %d", url, resp.StatusCode)
			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				return &retry.TransientInfrastructureError{Err: errMsg}
			}
			return errMsg
		}
		body = bodyBytes
		return nil
	}, opts)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// fetchKeyWithCache fetches an AES-128 key (16 bytes) with retry
// typed-path and a small per-playlist cache. The returned
// resolvedKeyURL is the absolute URL after
// resolveRelativeURL(playlistURL, keyURI) — the canonical
// post-resolution URL that the bytes came from.
func (f *HLSFetcher) fetchKeyWithCache(ctx context.Context, playlistURL, keyURI string, cache map[string]keyCacheEntry) (keyBytes []byte, resolvedKeyURL string, err error) {
	if cached, ok := cache[keyURI]; ok {
		return cached.keyBytes, cached.resolvedKeyURL, nil
	}
	keyURL, uErr := resolveRelativeURL(playlistURL, keyURI)
	if uErr != nil {
		return nil, "", fmt.Errorf("%w: resolve %q against %q: %v", ErrKeyFetch, keyURI, playlistURL, uErr)
	}
	body, err := f.fetchWithRetry(ctx, keyURL, f.cfg.keyFetchTimeout(), HLSSegmentByteRange{})
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s: %v", ErrKeyFetch, keyURL, err)
	}
	if len(body) != 16 {
		return nil, "", fmt.Errorf("%w: %s returned %d bytes (need exactly 16 for AES-128)",
			ErrKeyFetch, keyURL, len(body))
	}
	cache[keyURI] = keyCacheEntry{keyBytes: body, resolvedKeyURL: keyURL}
	return body, keyURL, nil
}

type keyCacheEntry struct {
	keyBytes       []byte
	resolvedKeyURL string
}

// resolveIV resolves the AES-128 IV for a segment per HLS spec
// §4.3.2.4. Explicit IV from the EXT-X-KEY attribute (hex) takes
// precedence; otherwise the media sequence number is encoded as a
// 128-bit big-endian integer.
func resolveIV(explicitIVHex string, sequence int64) ([]byte, error) {
	if explicitIVHex != "" {
		stripped := strings.TrimSpace(explicitIVHex)
		stripped = strings.TrimPrefix(stripped, "0x")
		stripped = strings.TrimPrefix(stripped, "0X")
		return IVFromHex(stripped)
	}
	return IVFromSequence(sequence), nil
}

// formatByteRangeHeader formats a byte range as an HTTP Range
// header value (e.g. "bytes=0-1023" or "bytes=2048-").
func formatByteRangeHeader(r HLSSegmentByteRange) string {
	if r.End > 0 {
		return fmt.Sprintf("bytes=%d-%d", r.Begin, r.End)
	}
	return fmt.Sprintf("bytes=%d-", r.Begin)
}

// isRetryableTransportError was REMOVED in favor of the canonical
// retry.IsTransient predicate (pkg/retry/transient.go). The
// pre-Fix hand-rolled loop in fetchWithRetry duplicated the
// "what counts as transient" decision; the FASE 6 cutover
// deliberately centralised that decision at pkg/retry.IsTransient
// (typed-path #1 + TransientInfrastructureError carrier). New
// classifier shapes are assembled into a retry.ClassifierRegistry
// (see pkg/retry/registry_*.go).
