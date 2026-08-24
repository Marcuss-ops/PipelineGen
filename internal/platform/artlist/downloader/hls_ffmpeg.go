// Package downloader — hls_ffmpeg.go: HLSFetcher orchestration + config
// + typed sentinels + URL / variant resolution.
//
// Split from hls_direct.go (commit refactor(downloader): split hls_direct.go
// into playlist/segment/ffmpeg concerns). Despite the file name, this
// file does NOT invoke ffmpeg — the final composition writes
// plaintext MPEG-TS bytes directly via bufio.Writer; downstream
// ffprobe validation lives in the Resolver's PostValidator (see
// build_bundles_artlist.go). The "ffmpeg" name labels this file as
// the END of the pipeline (composes .ts ready for ffmpeg.RemuxHLS /
// ffprobe consumption).
//
// godlike/06 SSOT: this file owns the fetcher struct + the entry
// point. The companion files hls_playlist.go (parsing) and
// hls_segment.go (network downloads) split the rest of the surface
// by concern so each file stays under the "send a code reviewer a
// single coherent concern" line that 990-LOC files violate.
package downloader

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

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

var ErrPlaylistFetch = errors.New("hls_direct: playlist fetch failed")
var ErrKeyFetch = errors.New("hls_direct: AES-128 key fetch failed")
var ErrSegmentFetch = errors.New("hls_direct: segment fetch failed")
var ErrTooManyRedirects = errors.New("hls_direct: too many redirects (signed URL handoff failed)")
var ErrUnsupportedEncryption = errors.New("hls_direct: encryption method is not AES-128 (only AES-128-CBC is supported)")
var ErrEmptyPlaylist = errors.New("hls_direct: playlist contains no segments and no variants")

type HLSFetcher struct {
	cfg    HLSConfig
	client *http.Client
	log    *zap.Logger
}

func NewHLSFetcher(cfg HLSConfig, log *zap.Logger) *HLSFetcher {
	return &HLSFetcher{
		cfg: cfg,
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

func (f *HLSFetcher) FetchAndCompose(ctx context.Context, playlistURL, outputDir string) (*HLSFetchResult, error) {
	if strings.TrimSpace(playlistURL) == "" {
		return nil, fmt.Errorf("hls_direct: playlist URL is required (godlike/07 \u2014 refuse to start with empty input)")
	}
	if strings.TrimSpace(outputDir) == "" {
		return nil, fmt.Errorf("hls_direct: output dir is required (godlike/07 \u2014 refuse to start with empty input)")
	}
	totalCtx, totalCancel := context.WithTimeout(ctx, f.cfg.totalTimeout())
	defer totalCancel()
	pl, err := f.fetchPlaylist(totalCtx, playlistURL)
	if err != nil {
		return nil, err
	}
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
	if f.cfg.MaxSegments > 0 && len(pl.Segments) > f.cfg.MaxSegments {
		return nil, fmt.Errorf("%w: playlist has %d segments (max=%d) \u2014 refusing to compose (godlike/07 fail-closed)",
			ErrEmptyPlaylist, len(pl.Segments), f.cfg.MaxSegments)
	}
	if len(pl.Segments) == 0 {
		return nil, fmt.Errorf("%w: media playlist has 0 segments", ErrEmptyPlaylist)
	}
	if mkErr := os.MkdirAll(outputDir, 0o755); mkErr != nil {
		return nil, fmt.Errorf("hls_direct: mkdir output dir: %w", mkErr)
	}
	tempOut, err := os.CreateTemp(outputDir, "hls_compose_*.ts.tmp")
	if err != nil {
		return nil, fmt.Errorf("hls_direct: create temp output: %w", err)
	}
	tempPath := tempOut.Name()
	defer func() {
		_ = tempOut.Close()
		_ = os.Remove(tempPath)
	}()
	keyCache := make(map[string]keyCacheEntry)
	writer := bufio.NewWriterSize(tempOut, 64*1024)
	var totalDuration time.Duration
	var bytesWritten int64
	var keyURL string
	for i, seg := range pl.Segments {
		select {
		case <-totalCtx.Done():
			return nil, totalCtx.Err()
		default:
		}
		segURL, uErr := resolveRelativeURL(playlistURL, seg.URI)
		if uErr != nil {
			return nil, fmt.Errorf("%w: resolve segment %q: %v", ErrSegmentFetch, seg.URI, uErr)
		}
		ct, fetchErr := f.fetchWithRetry(totalCtx, segURL, f.cfg.segmentFetchTimeout(), seg.ByteRange)
		if fetchErr != nil {
			return nil, fmt.Errorf("%w: segment %d/%d (%s): %v", ErrSegmentFetch, i+1, len(pl.Segments), segURL, fetchErr)
		}
		var plaintext []byte
		if seg.Key != nil && seg.Key.Method != "" {
			if seg.Key.Method != "AES-128" {
				return nil, fmt.Errorf("%w: %q (segment %d)", ErrUnsupportedEncryption, seg.Key.Method, i+1)
			}
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
			plaintext = pt
			keyURL = resolvedKeyURL
		} else {
			plaintext = ct
		}
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
	tempPath = ""
	return &HLSFetchResult{
		OutputPath:      finalPath,
		SegmentsFetched: len(pl.Segments),
		BytesWritten:    bytesWritten,
		Duration:        totalDuration,
		KeyURL:          keyURL,
	}, nil
}

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
