package sourcedl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"go.uber.org/zap"
)

// ── fakes ─────────────────────────────────────────────────────────────

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fakeInner counts downloads and serves canned bytes.
type fakeInner struct {
	calls    int
	payload  []byte
	dlErr    error
	recorded []string
}

func (f *fakeInner) Download(ctx context.Context, url string) (io.ReadCloser, error) {
	f.calls++
	f.recorded = append(f.recorded, url)
	if f.dlErr != nil {
		return nil, f.dlErr
	}
	return io.NopCloser(bytes.NewReader(f.payload)), nil
}

var _ MediaDownloader = (*fakeInner)(nil)

// fakeContent is an in-memory CAS store: dedup by identical bytes.
type fakeContent struct {
	objects  map[string][]byte
	putCalls int
	putErr   error
	openErr  error
}

func (f *fakeContent) Put(ctx context.Context, r io.Reader) (StoredObject, error) {
	f.putCalls++
	if f.putErr != nil {
		return StoredObject{}, f.putErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return StoredObject{}, err
	}
	sha := sha256Hex(b)
	dedup := false
	if _, exists := f.objects[sha]; exists {
		dedup = true
	} else {
		f.objects[sha] = b
	}
	return StoredObject{SHA256: sha, SizeBytes: int64(len(b)), Dedup: dedup}, nil
}

func (f *fakeContent) Open(ctx context.Context, sha256 string) (io.ReadCloser, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	b, ok := f.objects[sha256]
	if !ok {
		return nil, errors.New("fakeContent: missing object")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeContent) Exists(ctx context.Context, sha256 string) (bool, error) {
	_, ok := f.objects[sha256]
	return ok, nil
}

var _ ContentStore = (*fakeContent)(nil)

// fakeIdentities records lookups/records in memory.
type fakeIdentities struct {
	byKey       map[string]*capregistry.SourceIdentity
	records     []capregistry.SourceIdentity
	lookupErr   error
	recordErr   error
	recordCalls int
}

func (f *fakeIdentities) Lookup(ctx context.Context, sourceType, sourceKey string) (*capregistry.SourceIdentity, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	id := f.byKey[sourceType+"|"+sourceKey]
	if id == nil {
		return nil, nil
	}
	return id, nil
}

func (f *fakeIdentities) Record(ctx context.Context, id capregistry.SourceIdentity) error {
	f.recordCalls++
	if f.recordErr != nil {
		return f.recordErr
	}
	f.records = append(f.records, id)
	return nil
}

func (f *fakeIdentities) Count(ctx context.Context) (int64, error) {
	return int64(len(f.byKey)), nil
}

var _ capregistry.SourceIdentityStore = (*fakeIdentities)(nil)

type metricOutcome struct {
	operation     string
	hit           bool
	avoidedBytes  int64
	avoidedWorkMS int64
}

type fakeMetrics struct {
	outcomes []metricOutcome
}

func (m *fakeMetrics) RecordOutcome(_ context.Context, operation string, hit bool, avoidedBytes, avoidedWorkMS int64) error {
	m.outcomes = append(m.outcomes, metricOutcome{operation: operation, hit: hit, avoidedBytes: avoidedBytes, avoidedWorkMS: avoidedWorkMS})
	return nil
}

var _ capcache.MetricsRecorder = (*fakeMetrics)(nil)

func newTestDownloader(inner *fakeInner, ids *fakeIdentities, content *fakeContent) *SourceAwareDownloader {
	d, err := NewSourceAwareDownloader(inner, ids, content, zap.NewNop())
	if err != nil {
		panic(err)
	}
	return d
}

// ── tests ─────────────────────────────────────────────────────────────

func TestDownloadCacheHitSkipsNetwork(t *testing.T) {
	payload := []byte("already-known-bytes")
	sha := sha256Hex(payload)
	inner := &fakeInner{payload: payload}
	ids := &fakeIdentities{byKey: map[string]*capregistry.SourceIdentity{
		"url|https://example.com/v.mp4": {
			SourceType:    capregistry.SourceIdentityURL,
			SourceKey:     "https://example.com/v.mp4",
			ContentSHA256: sha,
			DiscoveredAt:  "2026-08-12T00:00:00Z",
			LastSeenAt:    "2026-08-12T00:00:00Z",
		},
	}}
	content := &fakeContent{objects: map[string][]byte{sha: payload}}
	d := newTestDownloader(inner, ids, content)

	rc, err := d.Download(context.Background(), "https://example.com/v.mp4")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, payload) {
		t.Fatalf("cache hit returned wrong bytes: %q", got)
	}
	if inner.calls != 0 {
		t.Fatalf("cache hit must NOT hit the network: inner.calls=%d", inner.calls)
	}
	if ids.recordCalls != 0 {
		t.Fatalf("cache hit must not re-record the identity: %d calls", ids.recordCalls)
	}
}

func TestDownloadMissStreamsThroughCASAndRecords(t *testing.T) {
	payload := []byte("fresh-bytes")
	inner := &fakeInner{payload: payload}
	ids := &fakeIdentities{byKey: map[string]*capregistry.SourceIdentity{}}
	content := &fakeContent{objects: map[string][]byte{}}
	d := newTestDownloader(inner, ids, content)

	rc, err := d.Download(context.Background(), "https://example.com/v2.mp4")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded bytes mismatch: %q", got)
	}
	if inner.calls != 1 {
		t.Fatalf("miss must download exactly once: calls=%d", inner.calls)
	}
	if content.putCalls != 1 {
		t.Fatalf("miss must stream through CAS: putCalls=%d", content.putCalls)
	}
	sha := sha256Hex(payload)
	if len(ids.records) != 1 {
		t.Fatalf("identity must be recorded: %d records", len(ids.records))
	}
	rec := ids.records[0]
	if rec.SourceType != capregistry.SourceIdentityURL || rec.SourceKey != "https://example.com/v2.mp4" {
		t.Fatalf("recorded wrong identity: %+v", rec)
	}
	if rec.ContentSHA256 != sha {
		t.Fatalf("recorded wrong sha: %s want %s", rec.ContentSHA256, sha)
	}
}

func TestDownloadDuplicateBytesAcrossURLsDedup(t *testing.T) {
	// Same bytes behind two different URLs: second acquisition must be a
	// CAS dedup hit (no second copy of the bytes).
	payload := []byte("identical-bytes")
	sha := sha256Hex(payload)
	inner := &fakeInner{payload: payload}
	ids := &fakeIdentities{byKey: map[string]*capregistry.SourceIdentity{}}
	content := &fakeContent{objects: map[string][]byte{}}
	d := newTestDownloader(inner, ids, content)

	if _, err := d.Download(context.Background(), "https://a.example/v.mp4"); err != nil {
		t.Fatal(err)
	}
	// Second download of identical bytes via a different URL.
	rc, err := d.Download(context.Background(), "https://b.example/v.mp4")
	if err != nil {
		t.Fatalf("second download: %v", err)
	}
	defer rc.Close()
	if content.putCalls != 2 {
		t.Fatalf("want 2 put calls, got %d", content.putCalls)
	}
	// The fake reports Dedup on the second put because the bytes already exist.
	if len(content.objects) != 1 {
		t.Fatalf("identical bytes must be stored ONCE: %d objects", len(content.objects))
	}
	if got, _ := content.objects[sha]; !bytes.Equal(got, payload) {
		t.Fatalf("stored bytes mismatch")
	}
	if inner.calls != 2 {
		t.Fatalf("both URLs must hit the network on a miss: calls=%d", inner.calls)
	}
}

func TestDownloadMetricsPersistDedupBytesAndWorkEstimate(t *testing.T) {
	payload := []byte("identical-metric-bytes")
	inner := &fakeInner{payload: payload}
	ids := &fakeIdentities{byKey: map[string]*capregistry.SourceIdentity{}}
	content := &fakeContent{objects: map[string][]byte{}}
	metrics := &fakeMetrics{}
	d := newTestDownloader(inner, ids, content)
	d.SetMetrics(metrics)

	first, err := d.Download(context.Background(), "https://a.example/metric.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	second, err := d.Download(context.Background(), "https://b.example/metric.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Close()

	var dedup *metricOutcome
	for i := range metrics.outcomes {
		if metrics.outcomes[i].operation == "cas_dedup" {
			dedup = &metrics.outcomes[i]
			break
		}
	}
	if dedup == nil || !dedup.hit || dedup.avoidedBytes != int64(len(payload)) || dedup.avoidedWorkMS != 0 {
		t.Fatalf("dedup metric=%+v, outcomes=%+v; want persistent storage bytes without claiming avoided network work", dedup, metrics.outcomes)
	}
}

func TestDownloadPutErrorSurfaced(t *testing.T) {
	inner := &fakeInner{payload: []byte("x")}
	content := &fakeContent{objects: map[string][]byte{}, putErr: errors.New("disk full")}
	d := newTestDownloader(inner, &fakeIdentities{}, content)

	if _, err := d.Download(context.Background(), "https://example.com/fail.mp4"); err == nil {
		t.Fatal("CAS put failure must be surfaced, got nil")
	}
}

func TestDownloadInnerErrorSurfaced(t *testing.T) {
	inner := &fakeInner{dlErr: errors.New("404")}
	content := &fakeContent{objects: map[string][]byte{}}
	d := newTestDownloader(inner, &fakeIdentities{}, content)

	if _, err := d.Download(context.Background(), "https://example.com/missing.mp4"); err == nil {
		t.Fatal("inner download failure must be surfaced, got nil")
	}
}

func TestDownloadLookupErrorFallsBack(t *testing.T) {
	payload := []byte("fallback-bytes")
	inner := &fakeInner{payload: payload}
	ids := &fakeIdentities{byKey: map[string]*capregistry.SourceIdentity{}, lookupErr: errors.New("db down")}
	content := &fakeContent{objects: map[string][]byte{}}
	d := newTestDownloader(inner, ids, content)

	rc, err := d.Download(context.Background(), "https://example.com/fb.mp4")
	if err != nil {
		t.Fatalf("identity lookup failure must NOT block the download: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, payload) {
		t.Fatalf("fallback bytes mismatch: %q", got)
	}
	if inner.calls != 1 {
		t.Fatalf("fallback must download: calls=%d", inner.calls)
	}
}

func TestDownloadNilIdentitiesSkipsLookupAndRecord(t *testing.T) {
	// The identity registry is optional: a nil store must keep the CAS
	// streaming path working with no lookups/records (advisory layer off).
	payload := []byte("no-registry-bytes")
	inner := &fakeInner{payload: payload}
	content := &fakeContent{objects: map[string][]byte{}}
	d, err := NewSourceAwareDownloader(inner, nil, content, zap.NewNop())
	if err != nil {
		t.Fatalf("nil identities must be accepted: %v", err)
	}

	rc, err := d.Download(context.Background(), "https://example.com/nr.mp4")
	if err != nil {
		t.Fatalf("download with nil identities: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, payload) {
		t.Fatalf("bytes mismatch: %q", got)
	}
	if content.putCalls != 1 {
		t.Fatalf("CAS streaming must still happen: putCalls=%d", content.putCalls)
	}
}

func TestDownloadFinalOpenFailureSurfaced(t *testing.T) {
	// After the bytes are stored, the final serve-from-CAS open must not be
	// silently swallowed into a re-download: the content is already stored,
	// so the caller must see the error (fail-closed, never re-download loop).
	inner := &fakeInner{payload: []byte("x")}
	content := &fakeContent{objects: map[string][]byte{}, openErr: errors.New("cas io error")}
	d := newTestDownloader(inner, &fakeIdentities{}, content)

	if _, err := d.Download(context.Background(), "https://example.com/io.mp4"); err == nil {
		t.Fatal("final CAS open failure must be surfaced, got nil")
	}
}

func TestDownloadRecordErrorNonFatal(t *testing.T) {
	payload := []byte("record-fail-bytes")
	inner := &fakeInner{payload: payload}
	ids := &fakeIdentities{byKey: map[string]*capregistry.SourceIdentity{}, recordErr: errors.New("db down")}
	content := &fakeContent{objects: map[string][]byte{}}
	d := newTestDownloader(inner, ids, content)

	rc, err := d.Download(context.Background(), "https://example.com/rf.mp4")
	if err != nil {
		t.Fatalf("record failure must NOT fail the download (bytes are stored): %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, payload) {
		t.Fatalf("bytes mismatch: %q", got)
	}
}

func TestNewSourceAwareDownloaderFailClosed(t *testing.T) {
	if _, err := NewSourceAwareDownloader(nil, nil, &fakeContent{}, zap.NewNop()); !errors.Is(err, ErrInnerDownloaderNil) {
		t.Fatalf("nil inner: want ErrInnerDownloaderNil, got %v", err)
	}
	if _, err := NewSourceAwareDownloader(&fakeInner{}, nil, nil, zap.NewNop()); !errors.Is(err, ErrContentStoreNil) {
		t.Fatalf("nil content store: want ErrContentStoreNil, got %v", err)
	}
}

func TestCanonicalKeyTrimsWhitespace(t *testing.T) {
	if got := canonicalKey("  https://example.com/v.mp4  "); got != "https://example.com/v.mp4" {
		t.Fatalf("canonicalKey must trim whitespace: %q", got)
	}
}
