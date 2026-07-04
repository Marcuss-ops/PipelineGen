// Package app — build_bundles_source_stagers.go (ART-002 P4.3, July 2026).
//
// WireSourceStagers is the composition-root helper that registers the
// 5 canonical SourceStager adapters in a fresh assets.SourceStagerRegistry.
//
// godlike/06 SSOT — one canonical owner per fact:
//
//	"Which SourceStager adapter runs for a given SourceKind?"
//	lives in assets.SourceStagerRegistry (the typed resolver, not
//	a switch in caller code). WireSourceStagers is the single
//	composition-root function that populates the registry with
//	the 5 canonical adapters. Every composition-root path that
//	needs a SourceStagerRegistry MUST route through this helper
//	(or its successor) — ad-hoc registry construction is an
//	antipattern that breaks godlike/06 'one owner per fact'.
//
// godlike/07 typed-error contract: WireSourceStagers returns the
// first registration error wrapped with the failing kind in the
// message so log-scanners can correlate. Nil-adapter checks use
// errors.Is(err, assets.ErrSourceStagerNil) for the per-adapter
// case (composition-time fail-closed).
//
// forward-pointer (godlike/07): the 3 skeleton adapters
// (httpstager, drive, catalog) are SKELETON per godlike/07
// no-fake-availability — they exist only so the registry can
// resolve the slot. When the real http/drive/catalog provider
// packages land, replace the skeleton with the real adapter at
// the composition root and let the SKELETON retirement commit
// drop the SKELETON marker. The WireSourceStagers signature is
// stable across the retirement.
package app

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// WireSourceStagers registers the 5 canonical SourceStager adapters in
// a fresh assets.SourceStagerRegistry and returns it.
//
// Parameters are typed as assets.SourceStager (the legacy per-call
// staging port) so the composition root can pass any implementation
// that satisfies the contract. The 5 parameters are positional in the
// canonical SourceKind order (youtube, http, artlist, drive,
// existing_catalog) so the wiring intent is visible at the call site.
//
// Order matches assets.CanonicalSourceKinds() (lexicographic, not
// semantic): artlist, drive, existing_catalog, http, youtube. A
// future agent adding a 6th SourceKind MUST update both
// assets.CanonicalSourceKinds AND this signature in lockstep per
// godlike/06 SSOT.
//
// Returns:
//   - *assets.SourceStagerRegistry: the populated registry, ready for
//     Resolve(kind) calls. Always non-nil (a fresh registry is created
//     even if all registrations fail).
//   - []error: the per-kind registration errors. Composition roots
//     feed these into log+drop per godlike/07; tests assert via
//     require.Empty(t, errs). An empty slice means all 5 kinds
//     registered successfully.
//
// godlike/07 typed-error contract: each returned error is a
// fmt.Errorf("%w: kind=%q", assets.ErrSourceStagerNil, kind) chain
// so callers can branch on the sentinel via errors.Is.
func WireSourceStagers(
	youtubeStager assets.SourceStager,
	httpStager assets.SourceStager,
	artlistStager assets.SourceStager,
	driveStager assets.SourceStager,
	catalogStager assets.SourceStager,
) (*assets.SourceStagerRegistry, []error) {
	reg := assets.NewSourceStagerRegistry()
	var errs []error

	// Register in canonical SourceKind order so iteration is
	// deterministic and the wiring intent is visible at the call site.
	// The first non-nil adapter wins; nil adapters are skipped (the
	// returned error slice carries the per-kind nil-adapter error).
	registrations := []struct {
		kind   assets.SourceKind
		stager assets.SourceStager
	}{
		{assets.SourceKindYouTube, youtubeStager},
		{assets.SourceKindHTTP, httpStager},
		{assets.SourceKindArtlist, artlistStager},
		{assets.SourceKindDrive, driveStager},
		{assets.SourceKindExistingCatalog, catalogStager},
	}

	for _, r := range registrations {
		if r.stager == nil {
			errs = append(errs, fmt.Errorf("%w: kind=%q (composition root did not wire this kind)", assets.ErrSourceStagerNil, r.kind))
			continue
		}
		if err := reg.Register(r.kind, r.stager); err != nil {
			errs = append(errs, fmt.Errorf("wire SourceStagers: register %q: %w", r.kind, err))
		}
	}

	return reg, errs
}
