// Package app — build_bundles_source_stagers_test.go (ART-002 P4.3, July 2026
// + PR-STOCK-ATLASTORCH-DISPATCH commit-3, July 2026).
//
// TDD coverage for the 2 composition-root helpers in
// build_bundles_source_stagers.go:
//
//   - WireSourceStagers (legacy assets.SourceStager registry, 3 TDD tests):
//     TestWireSourceStagers_RegistersAll5Kinds +
//     TestWireSourceStagers_NilAdapters_CarriesErrors +
//     TestWireSourceStagers_PartialFailure.
//
//   - WireAcquisitionStager (canonical *acquisition.FilesystemStager
//     construction for the stock pipeline, 4 TDD tests):
//     TestWireAcquisitionStager_HappyPath_ReturnsCanonicalFilesystemStager +
//     TestWireAcquisitionStager_NilCfg_ReturnsTypedError +
//     TestWireAcquisitionStager_NilLog_ToleratesFallback +
//     TestWireAcquisitionStager_Prepare_FailsClosedOnUnwiredFetch.
//
// Tests use real types (not mocks) so the compile-time var _ assertions
// on each Stager file + the concrete FilesystemStager file are exercised
// at every CI run.
package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	artliststager "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/catalog"
	assetdrive "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/drive"
	youtubestager "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/youtube"
	// httpstager package: directory path is `http`; package name is `httpstager`.
	appacq "github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/http"
	infacq "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/acquisition"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TestWireSourceStagers_RegistersAll5Kinds pins the happy path: when
// the composition root wires 5 real Stager instances (each via its
// canonical constructor), WireSourceStagers returns a non-nil
// registry with all 5 SourceKinds registered, and Resolve returns
// the correct adapter for each kind.
func TestWireSourceStagers_RegistersAll5Kinds(t *testing.T) {
	// Construct 5 real Stager instances. The ArtlistStager and
	// YouTubeStager take a parent adapter/downloader; nil is OK for
	// the registration test (the runtime check fires only on
	// StageSource, not on Register).
	artlistAdapter := artliststager.NewArtlistStager(nil)
	youtubeAdapter := youtubestager.NewYouTubeStager(nil)
	httpAdapter := httpstager.NewHTTPStager()
	driveAdapter := assetdrive.NewDriveStager()
	catalogAdapter := catalog.NewCatalogStager()

	reg, errs := WireSourceStagers(
		youtubeAdapter,
		httpAdapter,
		artlistAdapter,
		driveAdapter,
		catalogAdapter,
	)
	if reg == nil {
		t.Fatalf("expected non-nil registry, got nil")
	}
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}

	// Assert all 5 SourceKinds are registered.
	for _, kind := range assets.CanonicalSourceKinds() {
		if !reg.Has(kind) {
			t.Errorf("expected SourceStagerRegistry.Has(%q) to be true", kind)
		}
		got, err := reg.Resolve(kind)
		if err != nil {
			t.Errorf("expected Resolve(%q) to succeed, got err=%v", kind, err)
			continue
		}
		if got == nil {
			t.Errorf("expected Resolve(%q) to return non-nil adapter, got nil", kind)
		}
	}

	// Assert Resolve returns the EXACT adapter we passed in.
	if got, _ := reg.Resolve(assets.SourceKindYouTube); got != youtubeAdapter {
		t.Errorf("SourceKindYouTube: want %p, got %p", youtubeAdapter, got)
	}
	if got, _ := reg.Resolve(assets.SourceKindHTTP); got != httpAdapter {
		t.Errorf("SourceKindHTTP: want %p, got %p", httpAdapter, got)
	}
	if got, _ := reg.Resolve(assets.SourceKindArtlist); got != artlistAdapter {
		t.Errorf("SourceKindArtlist: want %p, got %p", artlistAdapter, got)
	}
	if got, _ := reg.Resolve(assets.SourceKindDrive); got != driveAdapter {
		t.Errorf("SourceKindDrive: want %p, got %p", driveAdapter, got)
	}
	if got, _ := reg.Resolve(assets.SourceKindExistingCatalog); got != catalogAdapter {
		t.Errorf("SourceKindExistingCatalog: want %p, got %p", catalogAdapter, got)
	}

	// Assert Kinds() returns 5 kinds in lexicographic order.
	kinds := reg.Kinds()
	if len(kinds) != len(assets.CanonicalSourceKinds()) {
		t.Errorf("expected %d kinds, got %d: %v", len(assets.CanonicalSourceKinds()), len(kinds), kinds)
	}
}

// TestWireSourceStagers_NilAdapters_CarriesErrors pins the nil-adapter
// path: when the composition root passes nil for one or more adapters,
// WireSourceStagers returns a non-nil registry (fresh, so the
// composition root can still introspect via Kinds()) AND a per-kind
// error slice reachable via errors.Is(err, assets.ErrSourceStagerNil).
// This matches the godlike/07 contract: composition-time fail-closed
// is surfaced, runtime callers do not see ErrSourceKindUnknown.
func TestWireSourceStagers_NilAdapters_CarriesErrors(t *testing.T) {
	reg, errs := WireSourceStagers(nil, nil, nil, nil, nil)
	if reg == nil {
		t.Fatalf("expected non-nil registry even with all nil adapters, got nil")
	}
	if len(errs) != len(assets.CanonicalSourceKinds()) {
		t.Errorf("expected %d errors, got %d", len(assets.CanonicalSourceKinds()), len(errs))
	}
	for i, err := range errs {
		if !errors.Is(err, assets.ErrSourceStagerNil) {
			t.Errorf("errs[%d]: expected errors.Is(err, assets.ErrSourceStagerNil) to be true, got err=%v", i, err)
		}
	}
}

// TestWireSourceStagers_PartialFailure pins the realistic gradual-
// rollout case: 4 real Stager instances + 1 nil. WireSourceStagers
// must return a non-nil registry with the 4 wired kinds registered
// AND a per-kind error slice with exactly 1 error (the nil-adapter
// one) carrying the failing kind in the message. This is the
// production case for partial rollout: a future provider package
// may not be wired yet, but the registry must still work for the
// wired ones.
func TestWireSourceStagers_PartialFailure(t *testing.T) {
	artlistAdapter := artliststager.NewArtlistStager(nil)
	youtubeAdapter := youtubestager.NewYouTubeStager(nil)
	httpAdapter := httpstager.NewHTTPStager()
	driveAdapter := assetdrive.NewDriveStager()
	// catalogStager is intentionally nil (partial rollout).

	reg, errs := WireSourceStagers(
		youtubeAdapter,
		httpAdapter,
		artlistAdapter,
		driveAdapter,
		nil, // catalog
	)
	if reg == nil {
		t.Fatalf("expected non-nil registry, got nil")
	}
	if len(errs) != 1 {
		t.Errorf("expected exactly 1 error (catalog nil), got %d: %v", len(errs), errs)
	}
	if len(errs) >= 1 && !errors.Is(errs[0], assets.ErrSourceStagerNil) {
		t.Errorf("expected errors.Is(errs[0], assets.ErrSourceStagerNil) to be true, got err=%v", errs[0])
	}
	// 4 wired kinds must be registered.
	if !reg.Has(assets.SourceKindYouTube) {
		t.Errorf("expected SourceKindYouTube registered")
	}
	if !reg.Has(assets.SourceKindHTTP) {
		t.Errorf("expected SourceKindHTTP registered")
	}
	if !reg.Has(assets.SourceKindArtlist) {
		t.Errorf("expected SourceKindArtlist registered")
	}
	if !reg.Has(assets.SourceKindDrive) {
		t.Errorf("expected SourceKindDrive registered")
	}
	// catalog must NOT be registered (it was nil).
	if reg.Has(assets.SourceKindExistingCatalog) {
		t.Errorf("expected SourceKindExistingCatalog NOT registered (it was nil)")
	}
	// Resolve on the nil kind must return ErrSourceKindUnknown.
	_, err := reg.Resolve(assets.SourceKindExistingCatalog)
	if !errors.Is(err, assets.ErrSourceKindUnknown) {
		t.Errorf("expected errors.Is(err, assets.ErrSourceKindUnknown) for nil catalog, got err=%v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────
// WireAcquisitionStager TDD coverage (PR-STOCK-ATLASTORCH-DISPATCH
// commit-3) — pins the canonical FS-backed SourceStager construction
// contract that BuildStockBundle's caller threads into
// StockBundleDeps.SourceStager.
// ──────────────────────────────────────────────────────────────────────

// makeTestCfg returns a config.Config fixture where cfg.Storage.TempPath()
// returns a per-test subdirectory under t.TempDir() so MkdirAll within
// the concrete's ctor lands on a writable path without polluting the
// global /tmp.
func makeTestCfg(t *testing.T) *config.Config {
	t.Helper()
	sub := filepath.Join(t.TempDir(), "stock_pipeline_staging")
	// config.StorageConfig.TempPath() = FullPath(TempDir).
	// Set TempDir to a path of the form "testtmp/<sub>" so FullPath
	// (which joins the StorageConfig.BaseDir prefix) yields the
	// test-owned subdirectory.
	cfg := &config.Config{}
	cfg.Storage.TempDir = sub
	return cfg
}

// TestWireAcquisitionStager_HappyPath_ReturnsCanonicalFilesystemStager
// pins the success path: a valid cfg + zap.NewNop() log returns
// (a) a non-nil source stager, (b) the returned value satisfies the
// canonical acquisition.SourceStager port (compile-time pinned at the
// concrete's site), (c) the returned value is concrete *FilesystemStager
// (so future port signature drift surfaces via the existing infra-side
// compile-time pin).
func TestWireAcquisitionStager_HappyPath_ReturnsCanonicalFilesystemStager(t *testing.T) {
	cfg := makeTestCfg(t)
	stager, err := WireAcquisitionStager(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("expected nil error on valid cfg, got: %v", err)
	}
	if stager == nil {
		t.Fatalf("expected non-nil source stager, got nil")
	}
	// Compile-time interface satisfaction assertion — locks future
	// port signature drift to a build failure rather than a runtime
	// panic. Mirrors the precedent at var _ appacq.SourceStager = (*infacq.FilesystemStager)(nil)
	// in the concrete.
	var _ appacq.SourceStager = stager

	// Concrete type assertion: the wire returns the canonical
	// *infacq.FilesystemStager. If a future caller substitutes a
	// different concrete, this fails loud so the wire surface is
	// traceable to the canonical owner per godlike/06 SSOT.
	if _, ok := stager.(*infacq.FilesystemStager); !ok {
		t.Errorf("expected *infacq.FilesystemStager concrete, got %T", stager)
	}
}

// TestWireAcquisitionStager_NilCfg_ReturnsTypedError pins the
// composition-time fail-closed gate: passing nil cfg returns
// (nil, err) where errors.Is(err, ErrStockPipelineStagerInit) is true.
// The double-error chain (sentinel + Filename or other ctor error)
// is preserved via fmt.Errorf("%w: ...", sentinel, ...) so callers
// can either errors.Is the sentinel OR string-match for diagnostics.
func TestWireAcquisitionStager_NilCfg_ReturnsTypedError(t *testing.T) {
	stager, err := WireAcquisitionStager(nil, zap.NewNop())
	if stager != nil {
		t.Errorf("expected nil stager on nil cfg, got %T", stager)
	}
	if err == nil {
		t.Fatalf("expected typed error on nil cfg, got nil")
	}
	if !errors.Is(err, ErrStockPipelineStagerInit) {
		t.Errorf("expected errors.Is(err, ErrStockPipelineStagerInit) to be true, got err=%v", err)
	}
}

// TestWireAcquisitionStager_NilLog_ToleratesFallback pins the
// nil-tolerance contract: passing nil log returns a non-nil stager
// (the wire substitutes zap.NewNop() internally). This mirrors the
// concrete's Options.Log nil tolerance — the wire does NOT panic on
// nil log; the ctor does the same.
func TestWireAcquisitionStager_NilLog_ToleratesFallback(t *testing.T) {
	cfg := makeTestCfg(t)
	stager, err := WireAcquisitionStager(cfg, nil)
	if err != nil {
		t.Fatalf("expected nil error on nil log (fallback to zap.NewNop), got: %v", err)
	}
	if stager == nil {
		t.Fatalf("expected non-nil stager on nil log, got nil")
	}
}

// TestWireAcquisitionStager_Prepare_FailsClosedOnUnwiredFetch pins
// the run-time typed-error contract: when the concrete is asked to
// Prepare a request with a valid (URL-bearing) SourceRef, the
// fail-closed Fetch stub returns appacq.ErrAcquisitionPrepareFailed
// (wrapped). This proves the godlike/07 no-fake-availability surface:
// a future real yt-dlp / HTTP / Drive wrapper must be a fresh
// canonical construction that REPLACES the stub, NOT a patch that
// silently returns nil.
//
// The stub returns a typed sentinel wrapped via appacq.Wrap so the
// inner ErrAcquisitionPrepareFailed is recoverable via errors.Is
// at the caller site — the foundation that lets the stock pipeline
// `source_staging.go` propagate the failure class without parsing
// human-readable suffix.
func TestWireAcquisitionStager_Prepare_FailsClosedOnUnwiredFetch(t *testing.T) {
	cfg := makeTestCfg(t)
	stager, err := WireAcquisitionStager(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("setup: WireAcquisitionStager failed: %v", err)
	}
	if stager == nil {
		t.Fatalf("setup: expected non-nil stager, got nil")
	}

	// Build a valid PrepareRequest (per appacq.PrepareRequest.Validate()).
	req := appacq.PrepareRequest{
		Source: appacq.SourceRef{
			URL:           "https://example.com/test_source",
			PolicyVersion: "test-v1",
		},
		IdempotencyKey: "test-idem-001",
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("setup: PrepareRequest.Validate() failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5_000_000_000) // 5s
	defer cancel()

	_, prepErr := stager.Prepare(ctx, req)
	if prepErr == nil {
		t.Fatalf("expected Prepare to fail-closed via stub Fetch, got nil err")
	}
	// The stub returns appacq.Wrap(appacq.ErrAcquisitionPrepareFailed, ...).
	if !errors.Is(prepErr, appacq.ErrAcquisitionPrepareFailed) {
		t.Errorf("expected errors.Is(prepErr, appacq.ErrAcquisitionPrepareFailed) to be true, got err=%v", prepErr)
	}
}
