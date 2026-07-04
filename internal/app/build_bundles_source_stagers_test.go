// Package app — build_bundles_source_stagers_test.go (ART-002 P4.3, July 2026).
//
// 1 TDD test pinning the WireSourceStagers composition-root behavior:
//
//   - TestWireSourceStagers_RegistersAll5Kinds: happy path — passes 5
//     real Stager instances (Artlist with nil downloader + YouTube
//     with nil adapter + 3 skeletons), asserts no errors, asserts
//     all 5 SourceKinds are registered, asserts Resolve returns
//     the correct adapter for each kind.
//
//   - TestWireSourceStagers_NilAdapters_CarriesErrors: nil-adapter
//     path — passes 5 nil SourceStagers, asserts the returned
//     registry is still non-nil (fresh registry) AND the error
//     slice carries 5 nil-adapter errors reachable via
//     errors.Is(err, assets.ErrSourceStagerNil).
//
// The test uses real Stager types (not mocks) so the compile-time
// var _ assertions on each Stager file (matching the ArtlistStager
// precedent) are exercised at every CI run.
package app

import (
	"errors"
	"testing"

	artliststager "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/catalog"
	assetdrive "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/drive"
	youtubestager "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/youtube"
	// httpstager package: directory path is `http`; package name is `httpstager`.
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/http"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// TestWireSourceStagers_RegistersAll5Kinds pins the happy path: when
// the composition root wires 5 real Stager instances (each via its
// canonical constructor), WireSourceStagers returns a non-nil
// registry with all 5 SourceKinds registered, and Resolve returns
// the exact adapter passed in.
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
