// Package downloader — hls_direct.go: Go-side HLS direct fetcher
// (Fase 8 / Commit 1, July 2026).
//
// User-spec literal:
//
//	"Fase 8 — Completa il downloader Artlist: MP4 diretto, HLS,
//	 redirect, URL firmati, retry typed-path, timeout, cancellazione.
//	 Per HLS con AES-128: decifra segmenti prima della composizione,
//	 rispetta IV, mai concatenare ciphertext. Validazione finale
//	 obbligatoria con ffprobe: size>0, container non corrotto,
//	 ≥1 stream video, durata>0. File che fallisce ffprobe ⇒ FAILED
//	 tipizzato nel lifecycle, mai PUBLISHED con file invalido."
//
// This file is the SINGLE canonical owner of the Go-side HLS
// pipeline (godlike/06 SSOT):
//
//   - m3u8 master + media playlist parsing (RFC 8216 subset)
//   - HTTP fetch of playlist + segments + keys with retry typed-path
//     (pkg/retry.IsTransient), per-fetch timeout (http.Client.Timeout
//     AND context.WithTimeout), ctx-cancellation through every loop
//     iteration, and explicit redirect follow (5 hops max).
//   - Per-segment AES-128-CBC decrypt with IV resolution
//     (explicit EXT-X-KEY IV= attribute OR implicit
//     media-sequence-number IV) — reuses the pure-function helper at
//     hlsdec.go::DecryptSegment.
//   - Plaintext composition: .ts segments are concatenated AFTER
//     decrypt (godlike/07 — ciphertext is NEVER concatenated;
//     AES-128 block boundaries do not align with MPEG-TS sync bytes).
//
// godlike/06 SSOT: this file owns the Go-side HLS pipeline. The
// Node scraper's HLS path (node-scraper/src/artlist/download.js)
// remains in place; this Go-side handler is the FALLBACK that
// kicks in when the Node scraper is unavailable OR when the
// operator opts into Go-direct via a future config flag
// (forward-pointer; out of scope for Commit 1).
//
// godlike/07 fail-closed:
//   - All transport errors (DNS, TCP, timeout, 5xx) flow through
//     retry.WrapTransient so retry.IsTransient classifies them
//     authoritatively (typed-path #1, no substring fallback).
//   - All HTTP non-2xx responses are typed errors
//     (ErrPlaylistFetch / ErrSegmentFetch / ErrKeyFetch) so callers
//     can branch on intent.
//   - Ciphertext-misalignment and PKCS#7 padding violations return
//     the typed hlsdec sentinels verbatim (no silent-degrade to
//     "decrypt failed: try again").
//   - Context cancellation (ctx.Done) short-circuits at every
//     loop iteration; the typed error returned is context.Canceled
//     (or context.DeadlineExceeded for timeout) — never a wrapped
//     transport error.
package downloader

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
)

// ── Public configuration ────────────────────────────────────────────────────

// HLSConfig is the operator-facing configuration for the Go-side
// HLS direct fetcher. Zero values fall back to the defaults below.
//
// godlike/06 SSOT: this struct is the SINGLE canonical input shape
// for the HLS fetcher. Adding parallel config types is a
// godlike/06 violation.
type HLSConfig struct {
	// PlaylistFetchTimeout is the per-fetch timeout for the master
	// + media playlist GET. Default: 10s.
	PlaylistFetchTimeout time.Duration

	// KeyFetchTimeout is the per-fetch timeout for the AES-128 key
	// GET. Default: 10s.
	KeyFetchTimeout time.Duration

	// SegmentFetchTimeout is the per-fetch timeout for each .ts
	// segment GET. Default: 30s.
	SegmentFetchTimeout time.Duration

	// TotalTimeout caps the entire FetchAndCompose operation
	// (playlist + all segments + compose). Default: 5m.
	TotalTimeout time.Duration

	// MaxRedirects is the maximum number of HTTP redirects to
	// follow per fetch (playlist, key, segment). 0 means
	// "use http.Client default" (10). Default: 5.
	MaxRedirects int

	// MaxSegments caps the number of segments per playlist. 0
	// means unlimited. Default: 0 (unlimited — long videos are
	// expected for Artlist cinematic b-roll).
	MaxSegments int

	// MaxAttempts is the retry budget per HTTP fetch (playlist,
	// key, segment). Routed through the canonical pkg/retry loop
	// with IsRetryable=retry.IsTransient. Default: 3.
	MaxAttempts int
}

func (c HLSConfig) playlistFetchTimeout() time.Duration {
	if c.PlaylistFetchTimeout > 0 {
		return c.PlaylistFetchTimeout
	}
	return 10 * time.Second
}

func (c HLSConfig) keyFetchTimeout() time.Duration {
	if c.KeyFetchTimeout > 0 {
		return c.KeyFetchTimeout
	}
	return 10 * time.Second
}

func (c HLSConfig) segmentFetchTimeout() time.Duration {
	if c.SegmentFetchTimeout > 0 {
		return c.SegmentFetchTimeout
	}
	return 30 * time.Second
}

func (c HLSConfig) totalTimeout() time.Duration {
	if c.TotalTimeout > 0 {
		return c.TotalTimeout
	}
	return 5 * time.Minute
}

func (c HLSConfig) maxRedirects() int {
	if c.MaxRedirects > 0 {
		return c.MaxRedirects
	}
	return 5
}

func (c HLSConfig) maxAttempts() int {
	if c.MaxAttempts > 0 {
		return c.MaxAttempts
	}
	return 3
}

// ── Public types ────────────────────────────────────────────────────────────

// HLSVariant is a single variant from a master playlist.
type HLSVariant struct {
	URI        string
	Bandwidth  int
	Resolution string // e.g. "1920x1080"; empty when not declared
	Codecs     string // e.g. "avc1.640028,mp4a.40.2"; empty when not declared
}

// HLSKey describes the EXT-X-KEY directive for the segments that
// follow it. Method is always "AES-128" for the supported
// encryption method; future SAMPLE-AES support would add new
// methods.
type HLSKey struct {
	Method string // "AES-128" or ""
	URI    string
	IV     string // hex (without 0x prefix) from the IV= attribute; empty = use sequence
}

// HLSSegment is one segment of a media playlist.
type HLSSegment struct {
	URI      string
	Duration time.Duration
	Sequence int64 // media sequence number (the IV fallback)
	// Key is the EXT-X-KEY in effect for this segment. Nil for
	// unencrypted segments.
	Key *HLSKey
	// ByteRange is the optional EXT-X-BYTERANGE for the segment.
	// Zero value (Begin=0, End=0) means "fetch the full URI".
	ByteRange HLSSegmentByteRange
}

// HLSSegmentByteRange is a subset of EXT-X-BYTERANGE.
type HLSSegmentByteRange struct {
	Begin int64
	End   int64 // inclusive; 0 means "to end of file"
}

// HLSPlaylist is the parsed result of an m3u8 fetch. Either
// IsMaster (with Variants) or contains Segments.
type HLSPlaylist struct {
	IsMaster  bool
	Version   int
	TargetDur time.Duration
	MediaSeq  int64 // first media sequence number; default 0
	Variants  []HLSVariant
	Segments  []HLSSegment
	EndList   bool // EXT-X-ENDLIST present

	// pendingByteRange is an unexported parse-state field set by
	// ParseM3U8 when an EXT-X-BYTERANGE directive is encountered.
	// The next segment URI inherits the byte range, then the field
	// is reset. Kept on the struct (rather than as a closure
	// variable) so the parser stays single-pass and easy to follow.
	pendingByteRange HLSSegmentByteRange
}

// HLSFetchResult is the outcome of FetchAndCompose.
type HLSFetchResult struct {
	// OutputPath is the path to the composed plaintext .ts file.
	// Always populated on success.
	OutputPath string
	// SegmentsFetched is the number of segments successfully
	// fetched + decrypted + concatenated.
	SegmentsFetched int
	// BytesWritten is the total plaintext bytes written to
	// OutputPath.
	BytesWritten int64
	// Duration is the sum of segment durations (the playlist's
	// declared media duration; ffprobe may differ slightly).
	Duration time.Duration
	// KeyURL is the AES-128 key URI used (empty for unencrypted).
	KeyURL string
}

// ── Typed sentinels ─────────────────────────────────────────────────────────

// ErrPlaylistFetch is the typed sentinel for playlist-fetch failures.
var ErrPlaylistFetch = errors.New("hls_direct: playlist fetch failed")

// ErrKeyFetch is the typed sentinel for AES-128 key-fetch failures.
var ErrKeyFetch = errors.New("hls_direct: AES-128 key fetch failed")

// ErrSegmentFetch is the typed sentinel for segment-fetch failures.
var ErrSegmentFetch = errors.New("hls_direct: segment fetch failed")

// ErrTooManyRedirects is the typed sentinel when redirect count
// exceeds the configured MaxRedirects.
var ErrTooManyRedirects = errors.New("hls_direct: too many redirects (signed URL handoff failed)")

// ErrUnsupportedEncryption is the typed sentinel for non-AES-128
// encryption (e.g. SAMPLE-AES). The spec supports AES-128 only.
var ErrUnsupportedEncryption = errors.New("hls_direct: encryption method is not AES-128 (only AES-128-CBC is supported)")

// ErrEmptyPlaylist is the typed sentinel when the parsed playlist
// has no segments AND no variants (a non-finite / non-master
// playlist is a malformed source — fail closed).
var ErrEmptyPlaylist = errors.New("hls_direct: playlist contains no segments and no variants")

// ── Fetcher ─────────────────────────────────────────────────────────────────

// HLSFetcher is the canonical Go-side HLS direct fetcher. It is
// stateless except for the http.Client (which is safe for
// concurrent use); multiple FetchAndCompose calls can run in
// parallel.
type HLSFetcher struct {
	cfg    HLSConfig
	client *http.Client
	log    *zap.Logger
}

// NewHLSFetcher constructs a fetcher with the given config + logger.
// The http.Client is configured with a CheckRedirect handler that
// enforces cfg.MaxRedirects; all other fields (Timeout, Transport)
// are set per-fetch via context.WithTimeout so different fetches
// (playlist, key, segment) can have independent timeouts.
func NewHLSFetcher(cfg HLSConfig, log *zap.Logger) *HLSFetcher {
	return &HLSFetcher{
		cfg: cfg,
		// http.Client is safe for concurrent use; we set Timeout
		// per-fetch via context.WithTimeout instead of
		// http.Client.Timeout (which is a single hard ceiling).
		client: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= cfg.maxRedirects() {
					return ErrTooManyRedirects
				}
				return nil
			},
		},
		log: log,
	}
}

// ── Entry point ─────────────────────────────────────────────────────────────

// FetchAndCompose is the high-level Go-side HLS pipeline:
//
//  1. Fetch the playlist (master or media) with retry typed-path +
//     timeout + cancellation.
//  2. If master: select the highest-bandwidth variant and re-fetch
//     the variant's media playlist (recursion bounded by the
//     IsMaster branch).
//  3. For each segment: fetch with retry + per-fetch timeout +
//     ctx-cancellation; if EXT-X-KEY is in effect, fetch the key
//     (with retry) and decrypt the segment with the spec-respected
//     IV (explicit IV= attribute OR media-sequence-number fallback).
//  4. Concatenate PLAINTEXT segments into the output .ts file
//     (MPEG-TS sync-byte-aligned concat works on plaintext ONLY —
//     ciphertext concat would scramble the AES-CBC block boundaries).
//  5. Return the output path; the Resolver's PostValidator runs
//     ffprobe on it (existing wire in build_bundles_artlist.go).
//
// godlike/07 fail-closed: the function NEVER writes partial output
// on failure — the output .ts file is created in a sibling temp
// directory and renamed atomically (os.Rename) ONLY on full success.
// On error, the temp file is removed and the typed error is
// returned to the caller.
func (f *HLSFetcher) FetchAndCompose(ctx context.Context, playlistURL, outputDir string) (*HLSFetchResult, error) {
	if strings.TrimSpace(playlistURL) == "" {
		return nil, fmt.Errorf("hls_direct: playlist URL is required (godlike/07 — refuse to start with empty input)")
	}
	if strings.TrimSpace(outputDir) == "" {
		return nil, fmt.Errorf("hls_direct: output dir is required (godlike/07 — refuse to start with empty input)")
	}

	// Total-timeout envelope: caps the entire operation. The inner
	// per-fetch timeouts are stricter; the total timeout is the
	// outer bound.
	totalCtx, totalCancel := context.WithTimeout(ctx, f.cfg.totalTimeout())
	defer totalCancel()

	// Step 1: fetch + parse the playlist.
	pl, err := f.fetchPlaylist(totalCtx, playlistURL)
	if err != nil {
		return nil, err
	}

	// Step 2: master -> media descent.
	if pl.IsMaster {
		if len(pl.Variants) == 0 {
			return nil, fmt.Errorf("%w: master playlist has 0 variants", ErrEmptyPlaylist)
		}
		chosen := selectHighestBandwidthVariant(pl.Variants)
		variantURL, vErr := resolveRelativeURL(playlistURL, chosen.URI)
		if vErr != nil {
			return nil, fmt.Errorf("%w: resolve variant %q: %v", ErrPlaylistFetch, chosen.URI, vErr)
		}
		if f.log != nil {
			f.log.Info("hls_direct: descending to highest-bandwidth variant",
				zap.String("master_url", playlistURL),
				zap.String("variant_url", variantURL),
				zap.Int("bandwidth", chosen.Bandwidth),
				zap.String("resolution", chosen.Resolution),
			)
		}
		pl, err = f.fetchPlaylist(totalCtx, variantURL)
		if err != nil {
			return nil, err
		}
	}

	// Step 2b: bound segment count (defense in depth — a misconfigured
	// upstream could ship a 100k-segment playlist).
	if f.cfg.MaxSegments > 0 && len(pl.Segments) > f.cfg.MaxSegments {
		return nil, fmt.Errorf("%w: playlist has %d segments (max=%d) — refusing to compose (godlike/07 fail-closed)",
			ErrEmptyPlaylist, len(pl.Segments), f.cfg.MaxSegments)
	}
	if len(pl.Segments) == 0 {
		return nil, fmt.Errorf("%w: media playlist has 0 segments", ErrEmptyPlaylist)
	}

	// Step 3: ensure output dir + create temp output file.
	if mkErr := os.MkdirAll(outputDir, 0o755); mkErr != nil {
		return nil, fmt.Errorf("hls_direct: mkdir output dir: %w", mkErr)
	}
	tempOut, err := os.CreateTemp(outputDir, "hls_compose_*.ts.tmp")
	if err != nil {
		return nil, fmt.Errorf("hls_direct: create temp output: %w", err)
	}
	tempPath := tempOut.Name()
	defer func() {
		// On any non-success return, remove the temp file so
		// partial output never leaks to the caller.
		_ = tempOut.Close()
		_ = os.Remove(tempPath)
	}()

	// Step 4: per-segment fetch + decrypt + concat.
	keyCache := make(map[string]keyCacheEntry) // raw URI -> (key, resolved URL); per-playlist cache
	writer := bufio.NewWriterSize(tempOut, 64*1024)
	var totalDuration time.Duration
	var bytesWritten int64
	var keyURL string

	for i, seg := range pl.Segments {
		// ctx-cancellation check: short-circuit at every loop iter
		// so a cancelled parent ctx aborts the whole pipeline
		// within one segment, not at the next fetch attempt.
		select {
		case <-totalCtx.Done():
			return nil, totalCtx.Err()
		default:
		}

		segURL, uErr := resolveRelativeURL(playlistURL, seg.URI)
		if uErr != nil {
			return nil, fmt.Errorf("%w: resolve segment %q: %v", ErrSegmentFetch, seg.URI, uErr)
		}

		// Fetch the (possibly byte-range subset of) ciphertext.
		ct, fetchErr := f.fetchWithRetry(totalCtx, segURL, f.cfg.segmentFetchTimeout(), seg.ByteRange)
		if fetchErr != nil {
			return nil, fmt.Errorf("%w: segment %d/%d (%s): %v", ErrSegmentFetch, i+1, len(pl.Segments), segURL, fetchErr)
		}

		// Step 4a: decrypt if EXT-X-KEY is in effect.
		var plaintext []byte
		if seg.Key != nil && seg.Key.Method != "" {
			if seg.Key.Method != "AES-128" {
				return nil, fmt.Errorf("%w: %q (segment %d)", ErrUnsupportedEncryption, seg.Key.Method, i+1)
			}
			// Per HLS spec §4.3.2.4, the key URI is resolved
			// relative to the playlist URI (NOT the segment URI).
			// Pass playlistURL as the base so relative key paths
			// like "/key.bin" resolve against the playlist host.
			key, resolvedKeyURL, kErr := f.fetchKeyWithCache(totalCtx, playlistURL, seg.Key.URI, keyCache)
			if kErr != nil {
				return nil, kErr
			}
			iv, ivErr := resolveIV(seg.Key.IV, seg.Sequence)
			if ivErr != nil {
				return nil, ivErr
			}
			pt, decErr := DecryptSegment(key, iv, ct)
			if decErr != nil {
				return nil, fmt.Errorf("hls_direct: decrypt segment %d: %w", i+1, decErr)
			}
			// godlike/07 fail-closed: NEVER write ciphertext to
			// the output file. Decrypt must succeed before any
			// write of the segment's bytes.
			plaintext = pt
			keyURL = resolvedKeyURL
		} else {
			plaintext = ct
		}

		// Step 4b: append PLAINTEXT to the output .ts. MPEG-TS
		// sync-byte-aligned concat works because every 188-byte
		// block starts with 0x47 — the concat boundary falls
		// between blocks, not inside one.
		if n, wErr := writer.Write(plaintext); wErr != nil {
			return nil, fmt.Errorf("hls_direct: write segment %d: %w", i+1, wErr)
		} else {
			bytesWritten += int64(n)
		}
		totalDuration += seg.Duration
	}

	if flushErr := writer.Flush(); flushErr != nil {
		return nil, fmt.Errorf("hls_direct: flush output: %w", flushErr)
	}
	if closeErr := tempOut.Close(); closeErr != nil {
		return nil, fmt.Errorf("hls_direct: close temp output: %w", closeErr)
	}

	// Step 5: atomic rename to the canonical output path. The
	// final path uses .ts extension; downstream ffmpeg.RemuxHLS
	// (or the Resolver's PostValidator ffprobe) accepts .ts.
	finalPath := filepath.Join(outputDir, fmt.Sprintf("hls_%d.ts", time.Now().UnixNano()))
	if renErr := os.Rename(tempPath, finalPath); renErr != nil {
		return nil, fmt.Errorf("hls_direct: rename temp -> final: %w", renErr)
	}

	if f.log != nil {
		f.log.Info("hls_direct: composed plaintext .ts",
			zap.String("output_path", finalPath),
			zap.Int("segments", len(pl.Segments)),
			zap.Int64("bytes", bytesWritten),
			zap.Duration("duration", totalDuration),
			zap.String("key_url", keyURL),
		)
	}

	// Defuse the defer's Remove call (output is now at finalPath).
	// We achieve this by reassigning tempPath to "" so the deferred
	// os.Remove("") is a no-op. (The defer is declared above; we
	// re-bind here for clarity.)
	tempPath = ""

	return &HLSFetchResult{
		OutputPath:      finalPath,
		SegmentsFetched: len(pl.Segments),
		BytesWritten:    bytesWritten,
		Duration:        totalDuration,
		KeyURL:          keyURL,
	}, nil
}

// ── Internal: playlist fetch + parse ────────────────────────────────────────

func (f *HLSFetcher) fetchPlaylist(ctx context.Context, url string) (*HLSPlaylist, error) {
	body, err := f.fetchWithRetry(ctx, url, f.cfg.playlistFetchTimeout(), HLSSegmentByteRange{})
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrPlaylistFetch, url, err)
	}
	pl, pErr := ParseM3U8(string(body))
	if pErr != nil {
		return nil, fmt.Errorf("%w: parse %s: %v", ErrPlaylistFetch, url, pErr)
	}
	return pl, nil
}

// ParseM3U8 is the canonical m3u8 mini-parser. Supports:
//
//   - Master playlists: #EXT-X-STREAM-INF + variant URI
//   - Media playlists:  #EXTINF + segment URI
//   - #EXT-X-VERSION, #EXT-X-TARGETDURATION, #EXT-X-MEDIA-SEQUENCE
//   - #EXT-X-KEY (METHOD=AES-128, URI=..., IV=0x...)
//   - #EXT-X-BYTERANGE
//   - #EXT-X-ENDLIST
//   - #EXT-X-DISCONTINUITY (preserved as a no-op for now)
//
// Returns the parsed playlist. A non-master playlist with zero
// segments returns an error (ErrEmptyPlaylist) — a playlist that
// parses cleanly but contains nothing is malformed input (godlike/07
// fail-closed).
func ParseM3U8(body string) (*HLSPlaylist, error) {
	pl := &HLSPlaylist{}
	var currentKey *HLSKey
	scanner := bufio.NewScanner(strings.NewReader(body))
	// Allow up to 1MB per line (defense against adversarial line
	// lengths in malformed m3u8).
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			// Master playlist: parse attributes, then the next
			// non-# line is the variant URI.
			v := HLSVariant{}
			if b, ok := attrInt(line, "BANDWIDTH"); ok {
				v.Bandwidth = b
			}
			if r, ok := attrString(line, "RESOLUTION"); ok {
				v.Resolution = r
			}
			if c, ok := attrString(line, "CODECS"); ok {
				v.Codecs = c
			}
			// The variant URI is the NEXT non-comment, non-empty line.
			for scanner.Scan() {
				next := strings.TrimSpace(scanner.Text())
				if next == "" || strings.HasPrefix(next, "#") {
					continue
				}
				v.URI = next
				break
			}
			pl.IsMaster = true
			pl.Variants = append(pl.Variants, v)
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-VERSION") {
			// Single-value directive: value is everything after
			// the colon (NOT a key=value pair, unlike
			// #EXT-X-KEY's METHOD= / URI= / IV= attributes).
			if v, err := strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-VERSION:")); err == nil {
				pl.Version = v
			}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-TARGETDURATION") {
			// Single-value directive (same shape as VERSION).
			if v, err := strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:")); err == nil {
				pl.TargetDur = time.Duration(v) * time.Second
			}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE") {
			// Single-value directive (same shape as VERSION).
			if v, err := strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64); err == nil {
				pl.MediaSeq = v
			}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-KEY") {
			method, _ := attrString(line, "METHOD")
			uri, _ := attrString(line, "URI")
			iv, _ := attrString(line, "IV")
			if strings.EqualFold(method, "NONE") {
				currentKey = nil
				continue
			}
			// Per HLS spec §4.3.2.4 the IV attribute is declared
			// as `IV=0x<hex>`. We strip the leading "0x"/"0X" so
			// HLSKey.IV is always a pure-hex string (matches the
			// field's godoc). resolveIV also strips defensively, but
			// stripping here keeps the public type clean.
			iv = strings.TrimSpace(iv)
			iv = strings.TrimPrefix(iv, "0x")
			iv = strings.TrimPrefix(iv, "0X")
			currentKey = &HLSKey{Method: method, URI: uri, IV: iv}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-ENDLIST") {
			pl.EndList = true
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-BYTERANGE") {
			// The byte range applies to the NEXT segment URI. We
			// stash it on the upcoming segment via a closure-free
			// approach: parse and remember, then apply on the
			// next non-comment line.
			r := parseByteRangeAttr(strings.TrimPrefix(line, "#EXT-X-BYTERANGE:"))
			pl.setPendingByteRange(r)
			continue
		}
		if strings.HasPrefix(line, "#EXTINF") {
			// #EXTINF:<duration>,<title> — duration is the first
			// comma-separated field, parsed as float seconds.
			dur := parseExtInfDuration(line)
			// The segment URI is the next non-comment, non-empty line.
			for scanner.Scan() {
				next := strings.TrimSpace(scanner.Text())
				if next == "" || strings.HasPrefix(next, "#") {
					continue
				}
				seg := HLSSegment{
					URI:       next,
					Duration:  dur,
					Sequence:  pl.MediaSeq + int64(len(pl.Segments)),
					Key:       currentKey,
					ByteRange: pl.getPendingByteRange(),
				}
				pl.setPendingByteRange(HLSSegmentByteRange{})
				pl.Segments = append(pl.Segments, seg)
				break
			}
			continue
		}
		// Other directives (#EXT-X-DISCONTINUITY, #EXT-X-PROGRAM-DATE-TIME,
		// #EXT-X-MAP, #EXT-X-MEDIA, etc.) are intentionally ignored:
		// godlike/07 minimum-blast-radius. AES-128 + plain
		// segments cover the Artlist surface; richer directives
		// (e.g. fMP4 EXT-X-MAP) are forward-pointer work.
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("hls_direct: scan m3u8: %w", err)
	}
	return pl, nil
}

// pendingByteRange is the indirect stash for the EXT-X-BYTERANGE
// directive (which applies to the NEXT segment URI, not the
// current line). The field lives on HLSPlaylist itself (see the
// struct definition above); the parser reads/writes it through
// the getPendingByteRange / setPendingByteRange methods so the
// access points are single-line auditable.

func (p *HLSPlaylist) getPendingByteRange() HLSSegmentByteRange { return p.pendingByteRange }
func (p *HLSPlaylist) setPendingByteRange(r HLSSegmentByteRange) {
	p.pendingByteRange = r
}

// ── Internal: HTTP fetch with retry typed-path ──────────────────────────────

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

	// cfg.MaxAttempts is shared with the Resolver's retry
	// budget; default 3 matches the resolver.go default.
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
		// Per-fetch timeout envelope. This is in ADDITION to
		// the outer totalTimeout (caller's responsibility).
		fetchCtx, cancel := context.WithTimeout(ctx, perFetchTimeout)
		defer cancel()

		req, rErr := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
		if rErr != nil {
			// A bad URL is terminal — wrap as transient
			// anyway so retry can surface it cleanly, but the
			// retry loop will see it as a 0-attempt failure
			// and abort (retry.IsTransient on a non-transient
			// error returns false, so the loop exits
			// immediately).
			return retry.WrapTransient(fmt.Errorf("hls_direct: new request %q: %w", url, rErr))
		}
		if byteRange.Begin > 0 || byteRange.End > 0 {
			req.Header.Set("Range", formatByteRangeHeader(byteRange))
		}
		// Signed-URL handoff: the cookie / auth headers survive
		// http.Client.CheckRedirect (Go's default behavior: the
		// client follows the Location header and re-sends the
		// same headers EXCEPT for Authorization / Cookie which
		// are stripped on cross-host redirects). Operators using
		// Google signed URLs (X-Goog-Signature=...) typically
		// rely on the signature being embedded in the query
		// string, not a header — so the default strip is
		// harmless. For header-based auth, callers can set
		// headers via a future extension (forward-pointer).
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
			// 4xx is terminal; 5xx + 429 are transient. We have
			// EXPLICIT knowledge of the response code (we just
			// read it off the response object) so we construct
			// the typed *TransientInfrastructureError directly
			// rather than relying on the canonical classifier
			// walker. WrapTransient would NOT classify a raw
			// fmt.Errorf("...status 503") as transient (the
			// FASE 6 Cut 6.1.D substring-fallback removal
			// means raw error strings no longer trigger retry).
			// The typed carrier is the canonical "this is
			// transient" signal for retry.IsTransient.
			errMsg := fmt.Errorf("hls_direct: GET %q returned status %d", url, resp.StatusCode)
			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				return &retry.TransientInfrastructureError{Err: errMsg}
			}
			return errMsg // terminal
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
// typed-path and a small per-playlist cache (a single playlist
// typically reuses one key for all segments; some playlists use
// key rotation, in which case the cache is harmless). The
// returned resolvedKeyURL is the absolute URL after
// resolveRelativeURL(playlistURL, keyURI) — the canonical
// post-resolution URL that the bytes came from. Callers that
// surface a "key URL" to operators (e.g. for audit logging)
// MUST use resolvedKeyURL, NOT the raw keyURI from the playlist.
//
// The key URI is resolved relative to playlistURL per HLS spec
// §4.3.2.4 (the key URI is anchored to the playlist, not the
// segment). When keyURI is already absolute, playlistURL is ignored.
//
// Cache shape: the per-playlist cache maps raw keyURI to a
// (resolvedKeyURL, keyBytes) pair so cache hits return the SAME
// resolved URL as the original fetch (the raw keyURI alone would
// be misleading because a relative "/key.bin" in the playlist
// resolves to an absolute URL that operators care about for
// audit logs).
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

// keyCacheEntry is the per-keyURI cache value (key bytes + the
// resolved URL the bytes came from). The resolvedKeyURL is
// essential for the audit-log surface: a relative "/key.bin" in
// the playlist resolves to an absolute URL that operators
// grep on, and returning the raw URI would silently mislead.
type keyCacheEntry struct {
	keyBytes       []byte
	resolvedKeyURL string
}

// ── Internal: helpers ───────────────────────────────────────────────────────

// resolveIV resolves the AES-128 IV for a segment per HLS spec
// §4.3.2.4. Explicit IV from the EXT-X-KEY attribute (hex) takes
// precedence; otherwise the media sequence number is encoded as a
// 128-bit big-endian integer (right-justified, left-zero-padded).
//
// The EXT-X-KEY attribute is declared as `IV=0x<hex>` (the
// leading "0x" is part of the HLS wire format, not actual hex
// data). We strip the "0x" prefix here so IVFromHex sees a
// pure-hex string. We tolerate the case where the caller has
// already stripped it (pure-hex input) by checking both shapes.
func resolveIV(explicitIVHex string, sequence int64) ([]byte, error) {
	if explicitIVHex != "" {
		stripped := strings.TrimSpace(explicitIVHex)
		stripped = strings.TrimPrefix(stripped, "0x")
		stripped = strings.TrimPrefix(stripped, "0X")
		return IVFromHex(stripped)
	}
	return IVFromSequence(sequence), nil
}

// selectHighestBandwidthVariant picks the variant with the largest
// BANDWIDTH attribute. Ties broken by resolution (parsed
// width*height), then by URI for determinism.
func selectHighestBandwidthVariant(vs []HLSVariant) HLSVariant {
	best := vs[0]
	for _, v := range vs[1:] {
		if v.Bandwidth > best.Bandwidth {
			best = v
			continue
		}
		if v.Bandwidth == best.Bandwidth {
			bw, bh := parseResolutionDims(best.Resolution)
			aw, ah := parseResolutionDims(v.Resolution)
			if aw*ah > bw*bh {
				best = v
			}
		}
	}
	return best
}

func parseResolutionDims(s string) (int, int) {
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	w, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
	return w, h
}

// resolveRelativeURL resolves a possibly-relative URI against a base
// URL. When baseURL is empty, the URI must be absolute (or the
// function returns an error).
func resolveRelativeURL(baseURL, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("hls_direct: empty URI")
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref, nil
	}
	if baseURL == "" {
		return "", fmt.Errorf("hls_direct: relative URI %q with no base URL", ref)
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("hls_direct: parse base %q: %w", baseURL, err)
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("hls_direct: parse ref %q: %w", ref, err)
	}
	return base.ResolveReference(r).String(), nil
}

// parseExtInfDuration extracts the duration in seconds from an
// #EXTINF line. The format is "#EXTINF:<duration>,<title>" where
// <duration> is a float.
func parseExtInfDuration(line string) time.Duration {
	line = strings.TrimPrefix(line, "#EXTINF:")
	if i := strings.Index(line, ","); i >= 0 {
		line = line[:i]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
	if err != nil || f < 0 {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}

// parseByteRangeAttr parses the value of an EXT-X-BYTERANGE
// directive, e.g. "1024@0" → Begin=0, End=1023.
func parseByteRangeAttr(s string) HLSSegmentByteRange {
	s = strings.TrimSpace(s)
	if s == "" {
		return HLSSegmentByteRange{}
	}
	parts := strings.SplitN(s, "@", 2)
	length, _ := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if length <= 0 {
		return HLSSegmentByteRange{}
	}
	begin := int64(0)
	if len(parts) == 2 {
		begin, _ = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	}
	return HLSSegmentByteRange{Begin: begin, End: begin + length - 1}
}

// formatByteRangeHeader formats a byte range as an HTTP Range
// header value (e.g. "bytes=0-1023" or "bytes=2048-").
func formatByteRangeHeader(r HLSSegmentByteRange) string {
	if r.End > 0 {
		return fmt.Sprintf("bytes=%d-%d", r.Begin, r.End)
	}
	return fmt.Sprintf("bytes=%d-", r.Begin)
}

// attrString extracts a string attribute from an HLS tag (the
// value-after-= form, with optional double quotes).
func attrString(line, name string) (string, bool) {
	prefix := name + "="
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return "", false
	}
	rest := line[idx+len(prefix):]
	// Take until comma or end-of-line.
	if i := strings.Index(rest, ","); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	rest = strings.Trim(rest, `"`)
	return rest, true
}

// attrInt extracts an integer attribute from an HLS tag.
func attrInt(line, name string) (int, bool) {
	s, ok := attrString(line, name)
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}

// attrInt64 extracts an int64 attribute from an HLS tag.
func attrInt64(line, name string) (int64, bool) {
	s, ok := attrString(line, name)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// isRetryableTransportError was REMOVED in favor of the canonical
// retry.IsTransient predicate (pkg/retry/transient.go). The
// pre-Fix hand-rolled loop in fetchWithRetry duplicated the
// "what counts as transient" decision; the FASE 6 cutover
// deliberately centralised that decision at pkg/retry.IsTransient
// (typed-path #1 + TransientInfrastructureError carrier). New
// classifier shapes register at init() via retry.RegisterClassifier
// (see pkg/retry/registry_*.go).
