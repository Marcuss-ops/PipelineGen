// queue_asset_prefetch.go — HTTP asset prefetch bridge for RenderingGen.
//
// Extracted from queue_client.go (September 2026) to keep the queue
// adapter under the 600-LOC forward-prevention gate (policy
// max_lines_per_file_strict). This file owns the object-store staging
// surface; queue_client.go retains the queue wire adapter and the
// clip-render executor.
package renderinggen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"golang.org/x/sync/errgroup"
)

// objectStoreHTTPClient bounds every object-store HTTP call (asset
// prefetch uploads and certified-artifact downloads). Without a timeout
// a hanging store would pin the caller — and the lease it holds —
// forever.
var objectStoreHTTPClient = &http.Client{
	Timeout:   5 * time.Minute,
	Transport: objectStoreTransport(),
}

// objectStoreTransport sizes the shared connection pool for the object
// store's real concurrency. The prefetch bridge uploads up to four assets in
// parallel to the same host (errgroup.SetLimit(4)) and the same client serves
// every subsequent HEAD/PUT/GET, so the default MaxIdleConnsPerHost=2 would
// close and rebuild connections between jobs. A larger idle pool keeps the
// concurrent transfers on reused keep-alive connections instead of paying a
// fresh TCP (and TLS, when configured) handshake per request.
func objectStoreTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 32
	return transport
}

// NewHTTPAssetPrefetcher bridges durable image bindings (verified
// remote URL) into RenderingGen's content-addressed object store. The
// queue worker accepts hashes only; PipelineGen must stage a
// cache-miss asset before enqueueing the render job.
func NewHTTPAssetPrefetcher(storeURL string) *AssetPrefetcher {
	storeURL = strings.TrimRight(strings.TrimSpace(storeURL), "/")
	return NewAssetPrefetcher(func(ctx context.Context, assets []scriptgen.RenderQueueAsset) error {
		var group errgroup.Group
		group.SetLimit(4)
		seen := make(map[string]struct{}, len(assets))
		for _, asset := range assets {
			asset := asset
			localPath := strings.TrimSpace(asset.LocalPath)
			downloadURL := asset.SourceURL
			if downloadURL == "" {
				downloadURL = asset.URL
			}
			// Official RenderingGen presets reference their font by canonical
			// logical path. That path is intentionally not an HTTP source and
			// LocalPath is producer-only, so resolve it against the configured
			// asset bundle before deciding whether the asset is stageable.
			// Without this bridge the font reaches the queue manifest but is
			// silently skipped by prefetch, leaving the worker with a cache miss.
			if localPath == "" && canonicalPresetFontPath(downloadURL) {
				font, err := ResolveFontAsset(FontPoppinsBold)
				if err != nil {
					return fmt.Errorf("resolve preset font %q: %w", downloadURL, err)
				}
				localPath = font.LocalPath
			}
			if strings.TrimSpace(asset.Hash) == "" ||
				(localPath == "" && strings.TrimSpace(downloadURL) == "") ||
				(localPath == "" && !strings.HasPrefix(downloadURL, "http")) {
				continue
			}
			hash := strings.ToLower(strings.TrimSpace(asset.Hash))
			if _, ok := seen[hash]; ok {
				continue
			}
			seen[hash] = struct{}{}
			group.Go(func() error {
				present, err := objectStored(ctx, storeURL, hash)
				if err != nil {
					return err
				}
				if present {
					return nil
				}
				if localPath != "" {
					if err := streamPutFile(ctx, storeURL, hash, localPath); err != nil {
						return fmt.Errorf("asset %s local stage: %w", hash, err)
					}
					return nil
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
				if err != nil {
					return fmt.Errorf("asset %s request: %w", hash, err)
				}
				resp, err := objectStoreHTTPClient.Do(req)
				if err != nil {
					return fmt.Errorf("asset %s download: %w", hash, err)
				}
				if resp.StatusCode < 200 || resp.StatusCode >= 300 {
					_ = resp.Body.Close()
					return fmt.Errorf("asset %s download: HTTP %d", hash, resp.StatusCode)
				}
				// Single-pass: stream remote → object store without temp file.
				err = streamPutReader(ctx, storeURL, hash, resp.Body, resp.ContentLength)
				_ = resp.Body.Close()
				if err != nil {
					return fmt.Errorf("asset %s stage: %w", hash, err)
				}
				return nil
			})
		}
		return group.Wait()
	})
}

func canonicalPresetFontPath(path string) bool {
	switch strings.TrimSpace(filepath.Clean(path)) {
	case "assets/fonts/Poppins-Bold.ttf", "fonts/Poppins-Bold.ttf", "Poppins-Bold.ttf":
		return true
	default:
		return false
	}
}

// objectStored reports whether the object store already holds key,
// using HEAD so no bytes cross the wire. 404/405 means absent;
// transport errors or other statuses fail closed.
func objectStored(ctx context.Context, store, key string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, store+"/objects/"+key, nil)
	if err != nil {
		return false, err
	}
	resp, err := objectStoreHTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("head %s: %w", key, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return false, nil
	default:
		return false, fmt.Errorf("head %s: HTTP %d", key, resp.StatusCode)
	}
}

// streamPutFile uploads a local file to the object store without
// loading it into RAM.
func streamPutFile(ctx context.Context, store, key, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	return streamPutReader(ctx, store, key, file, info.Size())
}

// streamPutReader uploads a streaming reader to the object store.
// When size >= 0 it is sent as ContentLength; otherwise chunked.
func streamPutReader(ctx context.Context, store, key string, r io.Reader, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, store+"/objects/"+key, r)
	if err != nil {
		return err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	resp, err := objectStoreHTTPClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
