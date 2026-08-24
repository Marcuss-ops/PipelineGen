// Package sourcedl — source-aware download wrapper (CAS design,
// August 2026).
//
// SourceAwareDownloader implements assets.MediaDownloader and wraps the
// existing HTTP downloader with the CAS acquisition flow:
//
//	SOURCE
//	  │
//	  ├── source_identity_registry lookup (source -> content SHA-256)
//	  │     ├── KNOWN + CAS object present → serve from CAS, NO network
//	  │     └── unknown / missing → download
//	  │
//	  └── download → stream through the CAS store (SHA-256 computed DURING
//	                 the write, via the LocalStore staging discipline)
//	                 → duplicate bytes discarded by CAS (dedup hit)
//	                 → record source -> sha256 in the identity registry
//	                 → serve the stored object
//
// The wrapper is a drop-in replacement for the plain MediaDownloader: the
// ingest flow keeps receiving an io.ReadCloser and keeps writing its own
// local copy. On a cache hit no network request is made at all.
//
// godlike/07 fail-closed contract:
//   - a nil inner downloader is a construction error (NewSourceAwareDownloader
//     fails closed);
//   - a CAS Put failure (streaming hash + store) is surfaced as an error —
//     partial content is never served;
//   - the identity lookup is advisory ONLY: a lookup/record failure must
//     never block media acquisition, so those paths degrade to the plain
//     download (logged, not swallowed).
//
// Known limitation (URL identity without revalidation): the identity key
// is the trimmed URL and source_version is not populated (assets.
// MediaDownloader exposes no response headers, so the design's "URL+etag"
// identity is only half-implemented here). A URL whose content changes
// (signed Drive links, redirect-to-latest endpoints) is pinned to the
// first-seen digest on cache hits with no TTL/revalidation. The registry
// keeps last_seen_at precisely so a future revalidation sweep can refresh
// such rows; until then, content-immutable URLs get the full dedup benefit
// while mutable URLs may serve stale bytes. Provider flows (Drive file ID,
// Artlist asset ID) that CAN observe versions should record them via their
// own source types.
package sourcedl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"go.uber.org/zap"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// MediaDownloader is the capability port. Its method set is intentionally
// compatible with the legacy ingest port while keeping this capability
// independent from internal/application.
type MediaDownloader interface {
	Download(context.Context, string) (io.ReadCloser, error)
}

var (
	// ErrInnerDownloaderNil is returned when the wrapper is constructed
	// without the inner downloader (composition error, fail-closed).
	ErrInnerDownloaderNil = errors.New("sourcedl: inner downloader is nil")
	// ErrContentStoreNil is returned when the wrapper is constructed without
	// the CAS content store (composition error, fail-closed).
	ErrContentStoreNil = errors.New("sourcedl: content store is nil")
)

// StoredObject mirrors the CAS object fields the wrapper needs. The local
// type keeps the application layer decoupled from the platform CAS store
// (the composition root adapts cas.Store to this port).
type StoredObject struct {
	SHA256    string // canonical 64-hex address
	SizeBytes int64  // size of the stored bytes
	Dedup     bool   // true when Put hit an existing object (bytes discarded)
}

// ContentStore is the narrow application-layer port over the CAS content
// store. Put streams content and returns the content-addressed object;
// Open/Exists serve already-stored bytes.
type ContentStore interface {
	// Put streams content into the store, computing the SHA-256 during the
	// write. Identical bytes at an existing address are discarded (dedup hit).
	Put(ctx context.Context, content io.Reader) (StoredObject, error)
	// Open returns a ReadCloser over the stored object at sha256.
	Open(ctx context.Context, sha256 string) (io.ReadCloser, error)
	// Exists reports whether the object at sha256 is present.
	Exists(ctx context.Context, sha256 string) (bool, error)
}

// ContentSizer is an optional extension used only for avoided-download
// metrics. Keeping it separate preserves compatibility with lightweight
// test and provider stores.
type ContentSizer interface {
	Size(context.Context, string) (int64, error)
}

// ContentVerifier is an optional integrity extension. Production CAS stores
// implement it so a source identity never serves bytes whose hash no longer
// matches the recorded address.
type ContentVerifier interface {
	Verify(context.Context, string) (bool, error)
}

// SourceAwareDownloader is the CAS-backed downloader. It implements the
// canonical assets.MediaDownloader port so the ingest service consumes it
// without changes.
type SourceAwareDownloader struct {
	inner      MediaDownloader
	identities capregistry.SourceIdentityStore
	content    ContentStore
	metrics    capcache.MetricsRecorder
	contentReg capregistry.ContentObjectStore
	log        *zap.Logger
}

// NewSourceAwareDownloader constructs the wrapper. Fail-closed: the inner
// downloader and the content store are required; the identity registry is
// optional (nil disables lookups/records while keeping the CAS streaming).
func NewSourceAwareDownloader(inner MediaDownloader, identities capregistry.SourceIdentityStore, content ContentStore, log *zap.Logger) (*SourceAwareDownloader, error) {
	if inner == nil {
		return nil, ErrInnerDownloaderNil
	}
	if content == nil {
		return nil, ErrContentStoreNil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SourceAwareDownloader{inner: inner, identities: identities, content: content, log: log}, nil
}

// SetMetrics attaches the shared durable cache-metrics recorder without
// changing the downloader constructor contract. Download caching remains
// functional when metrics are unavailable; metric write failures are logged
// and never turn a successful media download into a failed one.
func (d *SourceAwareDownloader) SetContentRegistry(registry capregistry.ContentObjectStore) *SourceAwareDownloader {
	if d != nil {
		d.contentReg = registry
	}
	return d
}

func (d *SourceAwareDownloader) SetMetrics(metrics capcache.MetricsRecorder) *SourceAwareDownloader {
	if d != nil {
		d.metrics = metrics
	}
	return d
}

var _ MediaDownloader = (*SourceAwareDownloader)(nil)

// Download fetches url with a CAS lookup before any network request:
//
//  1. known source identity + CAS object present → serve the object (hit);
//  2. otherwise download and stream through the CAS store (streaming
//     SHA-256 + dedup), record the source identity, serve the object.
func (d *SourceAwareDownloader) Download(ctx context.Context, url string) (io.ReadCloser, error) {
	if d == nil || d.inner == nil {
		return nil, ErrInnerDownloaderNil
	}

	key := canonicalKey(url)
	if key != "" && d.identities != nil {
		if rc, err := d.tryCacheHit(ctx, key); err != nil {
			d.log.Debug("sourcedl: cache probe failed; falling through to download",
				zap.String("url", url), zap.Error(err))
		} else if rc != nil {
			cachedBytes := d.cachedSize(ctx, key)
			d.recordMetric(ctx, "download", true, cachedBytes, estimatedDownloadWorkMS(cachedBytes))
			return rc, nil
		}
	}
	if key != "" {
		d.recordMetric(ctx, "download", false, 0, 0)
	}

	body, err := d.inner.Download(ctx, url)
	if err != nil {
		return nil, err
	}

	// Streaming SHA-256 + global dedup: the CAS store hashes the bytes
	// DURING the write; identical bytes at an existing address are
	// discarded (Put reports Dedup=true).
	obj, err := d.content.Put(ctx, body)
	_ = body.Close()
	if err != nil {
		return nil, fmt.Errorf("sourcedl: cas put: %w", err)
	}
	if obj.SHA256 == "" {
		return nil, errors.New("sourcedl: cas put returned an empty sha256")
	}
	if obj.Dedup {
		// CAS deduplication still required the network transfer, so this
		// is storage work avoided rather than network download work. Keep
		// it as a separate durable operation and report only the avoided
		// duplicate storage bytes.
		d.recordMetric(ctx, "cas_dedup", true, obj.SizeBytes, 0)
	}

	if d.contentReg != nil {
		if registryErr := d.contentReg.Put(ctx, capregistry.ContentObject{
			SHA256: obj.SHA256, SizeBytes: obj.SizeBytes, StorageURI: "cas://" + obj.SHA256,
			CreatedAt: nowRFC3339(), IntegrityStatus: capregistry.IntegrityUnverified,
		}); registryErr != nil {
			d.log.Warn("sourcedl: content object registry write failed (content stored)", zap.String("sha256", obj.SHA256), zap.Error(registryErr))
		}
	}

	if d.identities != nil && key != "" {
		now := nowRFC3339()
		if err := d.identities.Record(ctx, capregistry.SourceIdentity{
			SourceType:         capregistry.SourceIdentityURL,
			SourceKey:          key,
			ContentSHA256:      obj.SHA256,
			SourceVersion:      "",
			DiscoveredAt:       now,
			LastSeenAt:         now,
			VerificationStatus: capregistry.SourceIdentityUnverified,
		}); err != nil {
			// The bytes are already safely stored; recording the mapping is
			// bookkeeping for future downloads. Log, never fail the ingest.
			d.log.Warn("sourcedl: record source identity failed (content stored)",
				zap.String("url", url), zap.String("sha256", obj.SHA256), zap.Error(err))
		}
	}

	return d.content.Open(ctx, obj.SHA256)
}

// tryCacheHit serves the stored object when the identity registry knows the
// digest and the CAS store still holds it. Returns (nil, nil) on a probe
// miss so Download falls through to the network path.
func (d *SourceAwareDownloader) cachedSize(ctx context.Context, key string) int64 {
	if sizer, ok := d.content.(ContentSizer); ok {
		if id, err := d.identities.Lookup(ctx, capregistry.SourceIdentityURL, key); err == nil && id != nil {
			if size, sizeErr := sizer.Size(ctx, id.ContentSHA256); sizeErr == nil {
				return size
			}
		}
	}
	return 0
}

func (d *SourceAwareDownloader) recordMetric(ctx context.Context, operation string, hit bool, avoidedBytes, avoidedWorkMS int64) {
	if d == nil || d.metrics == nil {
		return
	}
	if err := d.metrics.RecordOutcome(ctx, operation, hit, avoidedBytes, avoidedWorkMS); err != nil {
		d.log.Warn("sourcedl: durable cache metric write failed", zap.String("operation", operation), zap.Error(err))
	}
}

// estimatedDownloadWorkMS converts the bytes skipped by a source-cache hit
// from a conservative 50 MiB/s baseline. This is explicitly an estimate
// (not wall time); the durable metric is useful for comparing cache behavior
// across runs without claiming a measured network duration.
func estimatedDownloadWorkMS(sizeBytes int64) int64 {
	if sizeBytes <= 0 {
		return 0
	}
	ms := (sizeBytes * 1000) / (50 * 1024 * 1024)
	if ms < 1 {
		return 1
	}
	return ms
}

func (d *SourceAwareDownloader) tryCacheHit(ctx context.Context, key string) (io.ReadCloser, error) {
	id, err := d.identities.Lookup(ctx, capregistry.SourceIdentityURL, key)
	if err != nil {
		return nil, err
	}
	if id == nil || id.ContentSHA256 == "" {
		return nil, nil
	}
	exists := false
	if verifier, ok := d.content.(ContentVerifier); ok {
		verified, verifyErr := verifier.Verify(ctx, id.ContentSHA256)
		if verifyErr != nil || !verified {
			return nil, verifyErr
		}
		exists = true
	} else {
		var existsErr error
		exists, existsErr = d.content.Exists(ctx, id.ContentSHA256)
		if existsErr != nil || !exists {
			return nil, existsErr
		}
	}
	if !exists {
		return nil, nil
	}
	rc, err := d.content.Open(ctx, id.ContentSHA256)
	if err != nil {
		return nil, nil
	}
	d.log.Debug("sourcedl: cache hit — serving from CAS without network download",
		zap.String("url", key), zap.String("sha256", id.ContentSHA256))
	return rc, nil
}

// canonicalKey normalizes a URL into the identity-registry key. The trimmed
// URL is the canonical key for the url source type (provider-issued IDs —
// Drive file ID, Artlist asset ID — are recorded with their own source
// types by the provider flows).
func canonicalKey(url string) string {
	return strings.TrimSpace(url)
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
