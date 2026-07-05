// Package app — build_bundles_source_stagers.go (ART-002 P4.3, July 2026 +
// PR-STOCK-ATLASTORCH-DISPATCH commit-3, July 2026).
//
// Two composition-root helpers live here:
//
//  1. WireSourceStagers: registers the 5 canonical SourceStager adapters
//     (youtube/http/artlist/drive/existing_catalog) in a fresh
//     assets.SourceStagerRegistry. Lives in the legacy `assets.SourceStager`
//     port namespace (per-call cleanup-on-the-caller contract).
//
//  2. WireAcquisitionStager: constructs the canonical FS-backed
//     *acquisition.FilesystemStager concrete for the persistent-staging
//     port `acquisition.SourceStager` (PR-STOCK-ATLASTORCH-DISPATCH §12-4).
//     Returns the typed port directly — BuildStockBundle's caller threads
//     it into StockBundleDeps.SourceStager.
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
//	"Where is the FS-backed persistent-staging concrete for the
//	stock pipeline constructed?" lives ONLY in WireAcquisitionStager.
//	Every composition-root path that needs acquisition.SourceStager
//	MUST route through this helper (or its successor) so the
//	construction is consistent and the (StagingRoot + Fetch) pair
//	contract is fail-closed at boot.
//
// godlike/07 typed-error contract: WireSourceStagers returns the
// first registration error wrapped with the failing kind in the
// message so log-scanners can correlate. Nil-adapter checks use
// errors.Is(err, assets.ErrSourceStagerNil) for the per-adapter
// case (composition-time fail-closed).
// WireAcquisitionStager returns ErrStockPipelineStagerInit wrapped
// around the underlying FilesystemStager constructor error via dual
// %w (Go 1.20+ chain preservation) so callers can errors.Is walk
// the canonical typed sentinel or errors.As recover the cause.
//
// forward-pointer (godlike/07): the 3 skeleton adapters
// (httpstager, drive, catalog) are SKELETON per godlike/07
// no-fake-availability — they exist only so the registry can
// resolve the slot. When the real http/drive/catalog provider
// packages land, replace the skeleton with the real adapter at
// the composition root and let the SKELETON retirement commit
// drop the SKELETON marker. The WireSourceStagers signature is
// stable across the retirement. Similarly the WireAcquisitionStager
// Fetch closure is a fail-closed typed-error stub awaiting the
// yt-dlp / HTTP / Drive Downloader wrappers (forward-pointer
// PR-STOCK-FETCH-WIRE-2026-Q3).
package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	appacq "github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	infacq "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ErrStockPipelineStagerInit is the typed sentinel returned by
// WireAcquisitionStager when the FS-backed *FilesystemStager ctor
// fails. Composition-time fail-closed: BuildStockBundle surfaces
// this sentinel via errors.Is so a missing/wrong dispatch brings
// the pipeline down at boot, NOT at first /run.
//
// Compounded with the underlying ctor error via dual %w so callers
// can either errors.Is the sentinel OR errors.As the cause via
// %w chain (Go 1.20+ chain preservation).
var ErrStockPipelineStagerInit = errors.New("internal/app: WireAcquisitionStager failed to construct *acquisition.FilesystemStager")

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

// WireAcquisitionStager constructs the canonical FS-backed
// *acquisition.FilesystemStager concrete (the canonical concrete
// for the acquisition.SourceStager port) and returns it as the
// typed port.
//
// godlike/07 fail-closed contract: NewFilesystemStager requires
// both opts.StagingRoot AND opts.Fetch. WireAcquisitionStager
// derives StagingRoot from cfg.Storage.TempPath() + a stock-pipeline
// subdirectory (created canonically by MkdirAll inside the
// concrete). For Fetch, WireAcquisitionStager supplies a fail-closed
// typed-error stub that returns appacq.Wrap(ErrAcquisitionPrepareFailed, ...)
// until the yt-dlp / HTTP / Drive Downloader wrapper lands (forward-
// pointer PR-STOCK-FETCH-WIRE-2026-Q3).
//
// godlike/07 typed-error contract: returns ErrStockPipelineStagerInit
// wrapped dual-%w around the ctor error; callers use errors.Is(err,
// ErrStockPipelineStagerInit) for the canonical sentinel OR
// errors.As for the underlying ctor cause.
//
// godlike/07 minimum-blast-radius: WireAcquisitionStager is pure-
// additive to the composition surface. The existing WireSourceStagers
// (assets.SourceStager registry) is untouched. Stock pipeline callers
// thread the return value into StockBundleDeps.SourceStager; production
// callers expect nil ONLY when ctor fails (in which case the
// booted-config aborts via godlike/07 fail-closed).
//
// Args:
//   - cfg: the canonical platform config; nil returns ErrStockPipelineStagerInit
//     typed error (composition-time fail-closed).
//   - log: zap logger; nil → falls back to zap.NewNop() per the concrete's
//     own Option.Log default (no compositional need for a panic on nil).
//
// Returns:
//   - (acquisition.SourceStager, nil) on success — the ready-to-use FilesystemStager.
//   - (nil, %w+wrap) on construction failure (cfg nil / StagingRoot mkdir failure).
func WireAcquisitionStager(cfg *config.Config, log *zap.Logger) (appacq.SourceStager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: cfg is nil (composition root must wire *platform/config.Config from ComposeRoot)", ErrStockPipelineStagerInit)
	}

	// godlike/07 nil-tolerant log default: stash so we always pass a
	// usable logger to the ctor (the ctor's Options.Log default is
	// zap.NewNop() but threading it explicit here keeps the log line
	// ownership clearly with WireAcquisitionStager).
	if log == nil {
		log = zap.NewNop()
	}

	// Stock-pipeline staging lives under cfg.Storage.TempPath() + a
	// canonical subdirectory. The MkdirAll is performed INSIDE the
	// concrete's ctor so WireAcquisitionStager doesn't duplicate the
	// creation logic (godlike/06 one-canonical-owner-per-fact: only
	// the ctor creates the staging root).
	stagingRoot := filepath.Join(cfg.Storage.TempPath(), "stock_pipeline_staging")

	// Fail-closed typed-error Fetch stub. When a future agent lands
	// the yt-dlp / HTTP / Drive Downgr wrappers (forward-pointer
	// PR-STOCK-FETCH-WIRE-2026-Q3), replace this stub with a real
	// concrete-adapter closure that calls the wrapper. The wrap with
	// %w preserves the typed sentinel recovery via errors.Is at the
	// call site — supervisors can branch on ErrAcquisitionPrepareFailed
	// without parsing the message string.
	fetchStub := func(_ context.Context, _ appacq.PrepareRequest, _ string, _ func(string)) error {
		return appacq.Wrap(
			appacq.ErrAcquisitionPrepareFailed,
			"acquisition.WireAcquisitionStager: Fetch is unwired (forward-pointer PR-STOCK-FETCH-WIRE-2026-Q3 awaiting yt-dlp/HTTP/Drive Downloader wrappers)",
		)
	}

	fsstager, err := infacq.NewFilesystemStager(infacq.Options{
		StagingRoot: stagingRoot,
		Fetch:       fetchStub,
		Log:         log,
	})
	if err != nil {
		// Dual-%w chain (Go 1.20+): callers can errors.Is(sentinel)
		// OR errors.As(cause) in one probe — neither side is opaque.
		return nil, fmt.Errorf("%w: %w", ErrStockPipelineStagerInit, err)
	}

	return fsstager, nil
}
