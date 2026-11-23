// chronon_timing_fetcher.go — the object-store read half for the raw Chronon
// timing sidecar.
//
// The render outcome carries a content-addressed reference to the verbatim
// `<output>.timing.json` deep profile RenderingGen preserved
// (Outcome.ChrononTimingStorageKey / ChrononTimingURL). This adapter is the
// single owner of turning that reference into bytes: it speaks the same
// object-store contract as the prefetch bridge (`/objects/<key>`) and reuses
// the SAME bounded HTTP client, so a slow store cannot pin a worker.
//
// It deliberately performs no parsing: the Chronon schema belongs to
// cliprender.ParseChrononSidecar, exactly like the README's "one parse, one
// canonical write" rule — this file only transports bytes.
package renderinggen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// maxChrononTimingBytes caps a raw sidecar read. The unbounded per-frame
// frame_times_ms array makes the document large but finite; 16 MiB is above
// the measured 5–10.6 MiB documents and still bounds a corrupt store.
const maxChrononTimingBytes = 16 << 20

// maxChrononTimingSummaryBytes caps the compact companion. It omits the
// per-frame array, so anything near this size is a producer bug, not a valid
// summary; the producer falls back to raw rather than trusting it.
const maxChrononTimingSummaryBytes = 8 << 20

// ErrChrononTimingUnavailable is the typed sentinel for a reference that
// carries neither a fetchable URL nor a content-addressable storage key.
var ErrChrononTimingUnavailable = errors.New("renderinggen: chronon timing sidecar reference is not fetchable")

// ChrononTimingFetcher reads the verbatim Chronon timing sidecar from the
// RenderingGen object store.
type ChrononTimingFetcher struct {
	storeURL string
	client   *http.Client
}

// NewChrononTimingFetcher constructs the adapter. Fail-fast: an empty store
// URL is a composition error, not a silent no-op (godlike/07).
func NewChrononTimingFetcher(storeURL string) (*ChrononTimingFetcher, error) {
	storeURL = strings.TrimRight(strings.TrimSpace(storeURL), "/")
	if storeURL == "" {
		return nil, errors.New("renderinggen chronon timing fetcher: store URL is required")
	}
	return &ChrononTimingFetcher{storeURL: storeURL, client: objectStoreHTTPClient}, nil
}

// FetchChrononTiming returns the sidecar bytes for the reference.
//
// Resolution order: an absolute http(s) URL wins (that is what RenderingGen
// certified); otherwise the storage key is addressed inside the configured
// object store. When the storage key IS a SHA-256 the returned bytes are
// re-hashed and must match — a transport that silently serves different bytes
// than the content address promises is rejected, not recorded.
func (f *ChrononTimingFetcher) FetchChrononTiming(ctx context.Context, storageKey, url string) ([]byte, error) {
	key, target, err := f.resolveTarget(storageKey, url)
	if err != nil {
		return nil, err
	}
	body, err := f.fetch(ctx, target, maxChrononTimingBytes)
	if err != nil {
		return nil, err
	}
	if digest.IsCanonicalSHA256(key) {
		if !strings.EqualFold(digest.SHA256Bytes(body), key) {
			return nil, fmt.Errorf("chronon timing sidecar digest mismatch: content-address %s", key)
		}
	}
	return body, nil
}

// FetchChrononTimingSummary fetches the compact companion the RenderingGen
// worker stores next to the raw sidecar under `<raw-key>.summary.json`. The hot
// projection path uses it because it omits the unbounded frame_times_ms array;
// the raw document remains the post-mortem evidence.
//
// The companion is derived from a content address rather than being one, so it
// is not re-hashed here. It is still bounded and fail-closed on HTTP errors; a
// missing summary makes the caller fall back to the raw document.
func (f *ChrononTimingFetcher) FetchChrononTimingSummary(ctx context.Context, storageKey, url string) ([]byte, error) {
	if f == nil {
		return nil, errors.New("renderinggen chronon timing fetcher: not wired")
	}
	_, target, err := f.resolveTarget(storageKey, url)
	if err != nil {
		return nil, err
	}
	return f.fetch(ctx, target+".summary.json", maxChrononTimingSummaryBytes)
}

func (f *ChrononTimingFetcher) resolveTarget(storageKey, url string) (string, string, error) {
	key := strings.TrimSpace(storageKey)
	target := strings.TrimSpace(url)
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		if key == "" {
			return "", "", fmt.Errorf("%w (no URL, no storage key)", ErrChrononTimingUnavailable)
		}
		target = f.storeURL + "/objects/" + key
	}
	return key, target, nil
}

func (f *ChrononTimingFetcher) fetch(ctx context.Context, target string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("chronon timing sidecar request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chronon timing sidecar transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("chronon timing sidecar: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("chronon timing sidecar read: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("chronon timing sidecar exceeds %d bytes", maxBytes)
	}
	return body, nil
}
