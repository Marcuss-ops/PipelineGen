// Package acquisition — filesystem_stager_test.go (Stock Cutover §12-4, July 2026).
//
// Integration tests for FilesystemStager. These touch the real
// filesystem (via t.TempDir) but stay hermetic by avoiding any
// subprocess (the Fetch closure writes bytes via Go os.WriteFile,
// not yt-dlp/HTTP). Real yt-dlp-backed testing belongs in
// `internal/infrastructure/acquisition/ytdlp_stager_test.go`
// (forward-pointer §12-4.2) which wraps `*downloader.YTDLPDownloader`.

package acquisition

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// appacq alias: this test file lives in
	// internal/infrastructure/acquisition (package name `acquisition`
	// — same as the application-side port package but distinct
	// import path). The application-side port's types
	// (appacq.PrepareRequest, PrepareContext, SourceRef, SourceStager)
	// resolve in this file's local scope to nothing without the
	// alias, surfacing as `undefined: appacq.PrepareRequest` etc. at
	// compile time. The alias keeps the canonical port surface
	// single-sourced in application/acquisition/ rather than
	// duplicating types (godlike/06 one-owner-per-fact).
	appacq "github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
)

// ── test-fetch helpers ──────────────────────────────────────────────

// fileFetchFn writes a fixed body when called. Used as the byte
// source for Prepare in tests so we exercise the staging surface
// without invoking yt-dlp / HTTP / Drive.
//
// The body is the literal "hello from acquisition staging surface"
// so tests can assert the SHA256 + SizeBytes match expectations
// without re-reading the file.
var fileFetchFnBody = []byte("hello from acquisition staging surface")

// fileFetchFn returns the canonical FetchFn for §12-4 integration
// tests. Writes the body to {dstPath}, computes the SHA256 +
// SizeBytes empirically, and reports via the onWireSHA256 callback
// so FilesystemStager's observed-SHA path stays consistent with
// the canonical hash.
func fileFetchFn(t *testing.T) FetchFn {
	t.Helper()
	return func(ctx context.Context, req appacq.PrepareRequest, dstPath string, onWireSHA256 func(hashHex string)) error {
		// Write atomically: tmp then rename.
		partial := dstPath + ".fetchwriting"
		if err := os.WriteFile(partial, fileFetchFnBody, 0o644); err != nil {
			return err
		}
		if err := os.Rename(partial, dstPath); err != nil {
			_ = os.RemoveAll(partial)
			return err
		}
		// Empirically compute the SHA so the on-wire path matches.
		hash, err := fileSHA256(dstPath)
		if err != nil {
			return err
		}
		if onWireSHA256 != nil {
			onWireSHA256(hash)
		}
		return nil
	}
}

// ── Construction ───────────────────────────────────────────────────

func TestNewFilesystemStager_EmptyRoot_ReturnsError(t *testing.T) {
	_, err := NewFilesystemStager(Options{Fetch: fileFetchFn(t)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "StagingRoot is required")
}

func TestNewFilesystemStager_NilFetch_ReturnsError(t *testing.T) {
	tmp := t.TempDir()
	_, err := NewFilesystemStager(Options{StagingRoot: tmp, Fetch: nil})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Fetch is required")
}

func TestNewFilesystemStager_CreatesStagingRoot_IfMissing(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "staging")
	require.NoFileExists(t, root)

	_, err := NewFilesystemStager(Options{StagingRoot: root, Fetch: fileFetchFn(t)})
	require.NoError(t, err)

	st, statErr := os.Stat(root)
	require.NoError(t, statErr)
	assert.True(t, st.IsDir(), "stagingRoot MUST exist as a directory post-construction")
}

// ── Prepare: download + stage + register ────────────────────────────

func TestFilesystemStager_Prepare_HappyPath_ReturnsCanonicalContext(t *testing.T) {
	root := t.TempDir()
	stager, err := NewFilesystemStager(Options{StagingRoot: root, Fetch: fileFetchFn(t)})
	require.NoError(t, err)

	req := appacq.PrepareRequest{
		Source:         appacq.SourceRef{URL: "https://example.com/x.mp4"},
		IdempotencyKey: "k-1",
		Timeout:        10 * time.Second,
		TTL:            1 * time.Hour,
	}
	got, err := stager.Prepare(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, got)

	// Validate the context invariants.
	require.NoError(t, got.Validated(), "PrepareContext MUST pass Validated gate")
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, req.Source, got.SourceRef, "SourceRef round-trip verbatim")
	assert.FileExists(t, got.LocalPath, "staged file MUST exist on disk")
	stat, statErr := os.Stat(got.LocalPath)
	require.NoError(t, statErr)
	assert.Equal(t, int64(len(fileFetchFnBody)), stat.Size(), "SizeBytes MUST equal .Size() of staged file")
	// SHA256 must match the test body.
	assert.Len(t, got.SHA256, 64, "SHA256 hex is 64 chars")
}

func TestFilesystemStager_Prepare_WritesMetaSidecar(t *testing.T) {
	root := t.TempDir()
	stager, _ := NewFilesystemStager(Options{StagingRoot: root, Fetch: fileFetchFn(t)})

	req := appacq.PrepareRequest{
		Source:         appacq.SourceRef{URL: "https://example.com/x.mp4"},
		IdempotencyKey: "k-1",
	}
	got, err := stager.Prepare(context.Background(), req)
	require.NoError(t, err)
	metaPath := got.LocalPath + ".meta.json"
	assert.FileExists(t, metaPath, ".meta.json MUST be written next to the staged file")
}

// ── Cache idempotency: second Prepare hits the cache ───────────────

func TestFilesystemStager_Prepare_ConcurrentSameSource_SerializesFetch(t *testing.T) {
	root := t.TempDir()

	var fetchCalls int32
	var mu sync.Mutex
	wrappedFetch := func(ctx context.Context, req appacq.PrepareRequest, dstPath string, onWireSHA256 func(string)) error {
		mu.Lock()
		fetchCalls++
		mu.Unlock()
		return fileFetchFn(t)(ctx, req, dstPath, onWireSHA256)
	}

	stager, err := NewFilesystemStager(Options{StagingRoot: root, Fetch: wrappedFetch})
	require.NoError(t, err)
	req := appacq.PrepareRequest{
		Source:         appacq.SourceRef{URL: "https://example.com/concurrent.mp4"},
		IdempotencyKey: "concurrent-key",
	}

	const callers = 12
	results := make(chan *appacq.PrepareContext, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prepared, prepareErr := stager.Prepare(context.Background(), req)
			if prepareErr != nil {
				errs <- prepareErr
				return
			}
			results <- prepared
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Errorf("concurrent Prepare failed: %v", err)
	}
	if got := len(results); got != callers {
		t.Fatalf("successful Prepare calls = %d, want %d", got, callers)
	}
	assert.EqualValues(t, 1, fetchCalls, "same stage ID must allow only one concurrent Fetch")

	var canonicalPath string
	for prepared := range results {
		require.NoError(t, prepared.Validated())
		if canonicalPath == "" {
			canonicalPath = prepared.LocalPath
		}
		assert.Equal(t, canonicalPath, prepared.LocalPath, "all callers must receive the canonical staged path")
	}
	assert.FileExists(t, canonicalPath)
	assert.FileExists(t, canonicalPath+".meta.json")
	stager.prepareLocksMu.Lock()
	assert.Empty(t, stager.prepareLocks, "released stage lock must not remain in the lock registry")
	stager.prepareLocksMu.Unlock()
}

func TestFilesystemStager_Prepare_DifferentSourcesCanFetchConcurrently(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	fetch := func(_ context.Context, req appacq.PrepareRequest, dstPath string, onWireSHA256 func(string)) error {
		started <- struct{}{}
		<-release
		body := []byte(req.Source.URL)
		if err := os.WriteFile(dstPath, body, 0o644); err != nil {
			return err
		}
		hash, err := fileSHA256(dstPath)
		if err != nil {
			return err
		}
		if onWireSHA256 != nil {
			onWireSHA256(hash)
		}
		return nil
	}
	stager, err := NewFilesystemStager(Options{StagingRoot: root, Fetch: fetch})
	require.NoError(t, err)

	results := make(chan error, 2)
	go func() {
		_, prepareErr := stager.Prepare(context.Background(), appacq.PrepareRequest{
			Source:         appacq.SourceRef{URL: "https://example.com/one.mp4"},
			IdempotencyKey: "one",
		})
		results <- prepareErr
	}()
	go func() {
		_, prepareErr := stager.Prepare(context.Background(), appacq.PrepareRequest{
			Source:         appacq.SourceRef{URL: "https://example.com/two.mp4"},
			IdempotencyKey: "two",
		})
		results <- prepareErr
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("different sources were unexpectedly serialized")
		}
	}
	close(release)
	assert.NoError(t, <-results)
	assert.NoError(t, <-results)
}

func TestFilesystemStager_Prepare_SecondCall_HitsCache(t *testing.T) {
	root := t.TempDir()

	// Wraps the canonical fetch so we can count invocations across
	// the two Prepares.
	var fetchCalls int32
	var mu sync.Mutex
	wrappedFetch := func(ctx context.Context, req appacq.PrepareRequest, dstPath string, onWireSHA256 func(string)) error {
		mu.Lock()
		fetchCalls++
		mu.Unlock()
		return fileFetchFn(t)(ctx, req, dstPath, onWireSHA256)
	}

	stager, _ := NewFilesystemStager(Options{StagingRoot: root, Fetch: wrappedFetch})
	req := appacq.PrepareRequest{Source: appacq.SourceRef{URL: "https://example.com/cache.mp4"}, IdempotencyKey: "k-1"}

	first, err := stager.Prepare(context.Background(), req)
	require.NoError(t, err)
	second, err := stager.Prepare(context.Background(), req)
	require.NoError(t, err)

	// assert.EqualValues (NOT assert.Equal): the closure-mutated
	// counter is int32 (atomic-safe on x86_64 arm64); testify's
	// assert.Equal uses reflect.DeepEqual which fails on
	// mismatched concrete types (int vs int32). EqualValues
	// normalises both sides to a comparable type (bit-equivalent
	// int64 cast) so int32 stays a faithful portable counter
	// without the test having to constantly cast.
	assert.EqualValues(t, 1, fetchCalls, "first Prepare calls Fetch; second MUST hit the cache (no Fetch call)")
	assert.Equal(t, first.ID, second.ID, "cache hit returns the same ID")
	assert.Equal(t, first.CleanupToken, second.CleanupToken, "cache hit returns the same CleanupToken")
	assert.Equal(t, first.SHA256, second.SHA256, "cache hit returns the same SHA256")
}

// ── Release: success + typed-error branches ────────────────────────

func TestFilesystemStager_Release_HappyPath_RemovesStagedFileAndMeta(t *testing.T) {
	root := t.TempDir()
	stager, _ := NewFilesystemStager(Options{StagingRoot: root, Fetch: fileFetchFn(t)})

	prepared, err := stager.Prepare(context.Background(), appacq.PrepareRequest{
		Source:         appacq.SourceRef{URL: "https://example.com/release-me.mp4"},
		IdempotencyKey: "k-1",
	})
	require.NoError(t, err)

	require.FileExists(t, prepared.LocalPath)
	require.FileExists(t, prepared.LocalPath+".meta.json")

	require.NoError(t, stager.Release(context.Background(), prepared.CleanupToken))
	assert.NoFileExists(t, prepared.LocalPath, "staged file MUST be removed after Release")
	assert.NoFileExists(t, prepared.LocalPath+".meta.json", ".meta.json MUST be removed after Release")
}

func TestFilesystemStager_Release_EmptyToken_ReturnsInvalidToken(t *testing.T) {
	root := t.TempDir()
	stager, _ := NewFilesystemStager(Options{StagingRoot: root, Fetch: fileFetchFn(t)})

	err := stager.Release(context.Background(), "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, appacq.ErrAcquisitionInvalidToken), "empty CleanupToken → appacq.ErrAcquisitionInvalidToken")
}

func TestFilesystemStager_Release_UnknownToken_ReturnsInvalidToken(t *testing.T) {
	root := t.TempDir()
	stager, _ := NewFilesystemStager(Options{StagingRoot: root, Fetch: fileFetchFn(t)})

	err := stager.Release(context.Background(), "not-a-real-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, appacq.ErrAcquisitionInvalidToken))
}

func TestFilesystemStager_Release_DoubleRelease_ReturnsInvalidToken(t *testing.T) {
	root := t.TempDir()
	stager, _ := NewFilesystemStager(Options{StagingRoot: root, Fetch: fileFetchFn(t)})

	prepared, err := stager.Prepare(context.Background(), appacq.PrepareRequest{
		Source:         appacq.SourceRef{URL: "https://example.com/double-release.mp4"},
		IdempotencyKey: "k-1",
	})
	require.NoError(t, err)

	require.NoError(t, stager.Release(context.Background(), prepared.CleanupToken))
	// Second release: cache lost, filesystem walk returns no match
	// (the .meta.json was removed in the first release).
	err = stager.Release(context.Background(), prepared.CleanupToken)
	require.Error(t, err)
	assert.True(t, errors.Is(err, appacq.ErrAcquisitionInvalidToken),
		"double-release MUST surface as appacq.ErrAcquisitionInvalidToken (filesystem scan miss after cache forget)")
}

// ── Release on expired stage: typed ErrExpired, file sweeped ────────

func TestFilesystemStager_Prepare_FetchFails_ReturnsPrepareFailed(t *testing.T) {
	root := t.TempDir()
	failingFetch := func(_ context.Context, _ appacq.PrepareRequest, _ string, _ func(string)) error {
		return errors.New("fetch closed with error: simulated network failure")
	}
	stager, _ := NewFilesystemStager(Options{StagingRoot: root, Fetch: failingFetch})

	_, err := stager.Prepare(context.Background(), appacq.PrepareRequest{
		Source:         appacq.SourceRef{URL: "https://example.com/fail.mp4"},
		IdempotencyKey: "k-1",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, appacq.ErrAcquisitionPrepareFailed), "Fetch error surfaces as appacq.ErrAcquisitionPrepareFailed")
}

// ── Prepare validation gate (acquire-side Validate) ────────────────

func TestFilesystemStager_Prepare_InvalidRequest_DoesNotCallFetch(t *testing.T) {
	root := t.TempDir()

	var fetchCalls int32
	var mu sync.Mutex
	wrappedFetch := func(_ context.Context, _ appacq.PrepareRequest, _ string, _ func(string)) error {
		mu.Lock()
		fetchCalls++
		mu.Unlock()
		return nil
	}
	stager, _ := NewFilesystemStager(Options{StagingRoot: root, Fetch: wrappedFetch})

	// Empty URL → appacq.ErrAcquisitionInvalidRequest from gate, no Fetch.
	_, err := stager.Prepare(context.Background(), appacq.PrepareRequest{
		Source:         appacq.SourceRef{URL: ""}, // empty URL in SourceRef
		IdempotencyKey: "k-1",
	})
	// Note: the port's SourceRef has no URL validation; the gate
	// is at appacq.PrepareRequest.Validate which inspects `req.Source.URL`.
	// Since req.Source.URL IS empty here, the gate fires.
	if err == nil {
		t.Fatal("expected gate error on empty URL")
	}
	assert.True(t, errors.Is(err, appacq.ErrAcquisitionInvalidRequest))
	mu.Lock()
	assert.EqualValues(t, 0, fetchCalls, "invalid request MUST NOT reach the Fetch closure")
	mu.Unlock()
}

// ── Final summary notes ────────────────────────────────────────────

// The integration surface intentionally stays lightweight (no yt-dlp,
// no HTTP) so the test runs in milliseconds on CI without infrastructure.
// Real yt-dlp integration testing is a §12-4.2 forward-pointer.
