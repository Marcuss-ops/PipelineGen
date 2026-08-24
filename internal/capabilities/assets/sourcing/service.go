// Package sourcing — thin façade. Post P0-1 (June 2026) the god Service is
// split into 4 use case sub-packages (youtube/, batch/, drivesync/,
// localimport/). The façade holds one handle per sub-service and routes
// the legacy public methods (RegisterFromYouTube, BatchRegisterFromYouTube,
// SyncDriveFolder, LocalToDrive) to the corresponding sub-package service.
//
// P0-1 / commit 5 (this final commit): façade cleaned up. NewService takes
// 5 args (4 sub-services + Logger; was 14 historically, 4 after commit 1,
// 5 after commit 2, 6 after commit 3, 7 after commit 4). The proxy
// `jobs` and `scanner` fields are dropped — they were only consumed by
// LocalToDrive (now delegated to localimport) and SyncDriveFolder (now
// delegated to drivesync). The composition root injects jobs + scanner
// DIRECTLY into localimport.NewService / drivesync.NewService instead.
//
// Per AGENTS.md Pattern 8 (API package: thin transport only) the façade
// has no business logic; delegation is one line per method.
package assets

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// Service is the SourcingService façade. After P0-1 / commit 5 the ctor
// takes 5 args — 4 sub-services + Logger. The god Service's 14-dep ctor
// is now fully distributed across 4 typed use-case packages.
//
// PR-RESOLVER-PORT-EXTRACT (SEMANTIC-LOCATION-API Wave 7, July 2026):
// the façade gains a 6th optional handle — locationResolver — wired via
// the canonical godlike/07 minimum-blast-radius fluent setter
// WithLocationResolver. The ctor itself stays 5-arg so the 1 existing
// caller (internal/app/assets_register_sourcing.go::newAssetRegisterService)
// compiles without modification. When locationResolver is nil AND
// RegisterClipCommand carries a non-empty Location with an empty
// FolderID, the service fail-closed-resolves via ErrLocationResolverEmpty
// rather than silently dropping the request (godlike/07 NO-FAKE-AVAILABILITY).
type Service struct {
	// P0-1 / commit 1: YouTube sub-service.
	youtube YouTubeRegistrar

	// P0-1 / commit 2: BatchRegistrar sub-service.
	batch BatchRegistrar

	// P0-1 / commit 3: DriveFolderSynchronizer sub-service.
	drivesync DriveFolderSynchronizer

	// P0-1 / commit 4: LocalImporter sub-service.
	localimport LocalImporter

	// PR-RESOLVER-PORT-EXTRACT (Wave 7, SEMANTIC-LOCATION-API-2026-07-06):
	// canonical Pattern-0 typed port that resolves a semantic-location
	// AssetLocationInput into a concrete Drive folder-id. Nil-resolver +
	// empty-folder-id + non-empty-location is the fail-closed surface;
	// the resolver is the SOLE canonical owner of the location→folder
	// translation for the UNINITIALIZED resolver case (godlike/06 SSOT).
	locationResolver LocationResolverPort

	log Logger
}

// NewService creates a SourcingService façade. After commit 5 NewService
// takes 5 args: youtube + batch + drivesync + localimport sub-services +
// Logger. JobsPort + FileScannerPort live with the sub-packages that
// need them (drivesync + localimport) and are no longer proxied here.
//
// PR-RESOLVER-PORT-EXTRACT (Wave 7): the 5-arg signature is preserved
// (godlike/07 minimum-blast-radius) — the resolver is added via the
// fluent WithLocationResolver setter rather than as a 6th ctor arg.
// The fluent setter is the canonical "additive dependency injection"
// pattern at the existing composition sites; a future PR may promote
// it to a true ctor arg once the resolver wiring is mandatory.
func NewService(
	yt YouTubeRegistrar,
	batch BatchRegistrar,
	drivesync DriveFolderSynchronizer,
	localimport LocalImporter,
	log Logger,
) *Service {
	return &Service{
		youtube:     yt,
		batch:       batch,
		drivesync:   drivesync,
		localimport: localimport,
		log:         log,
	}
}

// WithLocationResolver wires the Pattern-0 LocationResolverPort into the
// façade via a fluent setter (godlike/07 minimum-blast-radius additive
// DI). Once wired, the resolver is invoked by registerFromYouTube /
// batchRegisterFromYouTube fallback paths (resolveLocationFallback) to
// derive a folder-id when RegisterClipCommand.Location is non-empty AND
// FolderID is empty.
//
// The setter returns *Service so callers can chain it inline at the
// existing 5-arg composition sites without introducing a second call:
//
//	return sourcing.NewService(yt, b, d, l, log).WithLocationResolver(r)
//
// godlike/06 SSOT one-canonical-owner-per-fact: the resolver
// integration point lives ONLY here. Each call to WithLocationResolver
// REPLACES (does not append) the previous resolver — composition root
// wiring is the canonical one-time setup per process boot.
func (s *Service) WithLocationResolver(r LocationResolverPort) *Service {
	if s == nil {
		return nil
	}
	s.locationResolver = r
	return s
}

// resolveLocationFallback implements the F3 service-layer fallback
// contract (PR-RESOLVER-PORT-EXTRACT Wave 7):
//
//  1. cmd.FolderID non-empty → return as-is, no resolver call.
//  2. cmd.Location.IsEmpty() → return cmd.FolderID (legacy bare call).
//  3. s.locationResolver nil → typed error ErrLocationResolverEmpty
//     at the by-call-site surface so misconfigured compositions
//     fail loudly rather than silently pass through empty locations.
//  4. otherwise → resolver.Resolve; on err wrap with the typed sentinel.
//     The resolver returns the canonical folder-id; merged into
//     cmd.FolderID before downstream YouTubeRegistrar.Register
//     consumes the cmd.
//
// godlike/07 NO-FAKE-AVAILABILITY: this helper either returns a
// non-empty folder-id or returns nil+error. There is no
// silent-fail-closed pass-through to the orchestrator.
func (s *Service) resolveLocationFallback(ctx context.Context, cmd RegisterClipCommand, dest delivery.DestinationKey) (string, error) {
	if cmd.FolderID != "" {
		return cmd.FolderID, nil
	}
	if cmd.Location.IsEmpty() {
		return cmd.FolderID, nil
	}
	if s == nil || s.locationResolver == nil {
		return "", fmt.Errorf(
			"%w (composition site must call sourcing.NewService(...).WithLocationResolver(r) per PR-RESOLVER-PORT-EXTRACT Wave 7 fail-closed contract)",
			ErrLocationResolverEmpty,
		)
	}
	folderID, err := s.locationResolver.Resolve(ctx, cmd.Location, dest)
	if err != nil {
		return "", fmt.Errorf("sourcing.Service.resolveLocationFallback: resolver error: %w", err)
	}
	if folderID == "" {
		return "", fmt.Errorf(
			"%w: resolver returned empty folder-id for non-empty Location",
			ErrLocationResolverDestinationUnsupported,
		)
	}
	if s.log != nil {
		s.log.Info(
			"sourcing: resolved semantic-location to folder",
			"folder_id", folderID,
			"category", cmd.Location.Category,
			"subject", cmd.Location.Subject,
		)
	}
	return folderID, nil
}

// RegisterFromYouTube delegates to the YouTube sub-package service.
// The legacy method body has moved to
// internal/application/assets/sourcing/youtube/service.go::Service.Register.
// Behavior is identical — the façade only changes the lookup direction.
//
// PR-RESOLVER-PORT-EXTRACT (SEMANTIC-LOCATION-API Wave 7, July 2026):
// F3 service-layer fallback. The resolution step runs FIRST (godlike/07
// fail-fast-at-input > fail-slow-at-orchestration): when cmd.Location
// is non-empty AND cmd.FolderID is empty, the resolveLocationFallback
// returns a typed sentinel if the resolver is misconfigured — callers
// see the resolver-error surface BEFORE the youtube-not-wired surface,
// preserving typed-error probing on a per missing-component basis.
//
// Per godlike/07 minimum-blast-radius, callers that pre-Wave-7 set only
// cmd.FolderID continue to work byte-identically: resolveLocationFallback
// returns cmd.FolderID as-is at step 1 and never invokes the resolver.
// When BOTH cmd.FolderID AND cmd.Location are non-empty, the cmd.FolderID
// wins (legacy precedence preserved) and a Warn log is emitted for
// operator visibility.
func (s *Service) RegisterFromYouTube(ctx context.Context, cmd RegisterClipCommand) (*RegisterClipResult, error) {
	// F3 service-layer fallback runs FIRST so resolver-misconfig failures
	// surface at the typed sentinel rather than the orchestrator-nil one.
	resolved, err := s.resolveLocationFallback(ctx, cmd, delivery.DestinationYouTubeClip)
	if err != nil {
		return nil, err
	}
	if cmd.FolderID == "" && resolved != "" {
		cmd.FolderID = resolved
	} else if cmd.FolderID != "" && !cmd.Location.IsEmpty() && s.log != nil {
		// Round-2 SHOULD-FIX (2026-07-06, debounced): the precedence
		// override fired Warn per call, spamming hot-path request
		// streams. Down-level to Debug per godlike/07 minimum-blast-radius
		// (stateless, no sync.Once map, no atomic counter) so operators
		// running --log-level=info see no signal but log-level=debug
		// captures the structural override context for forensics.
		s.log.Debug(
			"sourcing: cmd.FolderID takes precedence over cmd.Location (legacy precedence preserved per F3 / PR-RESOLVER-PORT-EXTRACT)",
			"folder_id", cmd.FolderID,
			"category", cmd.Location.Category,
			"subject", cmd.Location.SubjectOrName(),
		)
	}
	if s == nil || s.youtube == nil {
		return nil, fmt.Errorf("sourcing.RegisterFromYouTube: youtube registrar not wired (compose-time bug — check newAssetRegisterService)")
	}
	return s.youtube.Register(ctx, cmd)
}

// BatchRegisterFromYouTube processes a batch of clip registration
// commands sequentially, delegating to the batch sub-package service
// (P0-1 / commit 2). The legacy inline loop has moved to
// internal/application/assets/sourcing/batch/service.go::Service.BatchRegister.
func (s *Service) BatchRegisterFromYouTube(ctx context.Context, commands []RegisterClipCommand) *BatchRegisterResult {
	if s == nil || s.batch == nil {
		return &BatchRegisterResult{
			OK:            false,
			Total:         len(commands),
			EnqueueFailed: len(commands),
			Results:       make([]BatchClipResult, len(commands)),
		}
	}

	// Resolve semantic locations before enqueueing. The async worker calls the
	// YouTube registrar directly, so resolving only in RegisterFromYouTube
	// would leave batch jobs with an empty FolderID and route them to the
	// configured default Drive root. Resolve the complete batch first so a
	// resolver failure cannot leave a partially-enqueued request.
	resolved := make([]RegisterClipCommand, len(commands))
	copy(resolved, commands)
	for i := range resolved {
		folderID, err := s.resolveLocationFallback(ctx, resolved[i], delivery.DestinationYouTubeClip)
		if err != nil {
			results := make([]BatchClipResult, len(commands))
			for j := range results {
				results[j].Name = commands[j].Name
				results[j].Error = fmt.Sprintf("resolve location for clip %d: %v", i, err)
			}
			return &BatchRegisterResult{
				OK:            false,
				Total:         len(commands),
				EnqueueFailed: len(commands),
				Results:       results,
			}
		}
		if resolved[i].FolderID == "" {
			resolved[i].FolderID = folderID
		}
	}
	return s.batch.BatchRegister(ctx, resolved)
}

// SyncDriveFolder delegates to the drivesync sub-package service.
// The legacy method body has moved to
// internal/application/assets/sourcing/drivesync/service.go::Service.Sync.
// Behavior is identical — the façade only changes the lookup direction.
// Nil-svc guard preserved at the façade boundary so test fixtures that
// construct sourcing.NewService with a nil drvSvc continue to surface
// the error as `drive_folder_id is required` (drivesync.Sync's own
// first-line validation order matches the historical god method).
func (s *Service) SyncDriveFolder(ctx context.Context, cmd SyncDriveFolderCommand) (*SyncDriveFolderResult, error) {
	if s == nil || s.drivesync == nil {
		return nil, fmt.Errorf("sourcing.SyncDriveFolder: drivesync registrar not wired (compose-time bug — check newAssetRegisterService)")
	}
	return s.drivesync.Sync(ctx, cmd)
}

// LocalToDrive delegates to the localimport sub-package service.
// The legacy method body has moved to
// internal/application/assets/sourcing/localimport/service.go::Service.Import.
// Behavior is identical — the façade only changes the lookup direction.
// Nil-svc guard at the façade boundary so test fixtures with a nil
// localimport continue to surface the error message consistently with
// the other 3 delegated methods.
func (s *Service) LocalToDrive(ctx context.Context, cmd LocalToDriveCommand) (*LocalToDriveResult, error) {
	if s == nil || s.localimport == nil {
		return nil, fmt.Errorf("sourcing.LocalToDrive: localimport registrar not wired (compose-time bug — check newAssetRegisterService)")
	}
	return s.localimport.Import(ctx, cmd)
}

// Compile-time assertion: the YouTube sub-package's Service must satisfy
// the façade-level YouTubeRegistrar interface. Live in the composition
// root (internal/app/assets_register_sourcing.go) where both packages can
// be transitively imported without creating a cycle. See Go-1 import
// cycle rule: the sourcing package itself cannot import youtube without
// pulling sourcing back through youtube's transitive import of sourcing
// (the cycle breaks via the YouTubeRegistrar interface declared in this
// package's contract.go).
//
// (no assertion here — see internal/app/assets_register_sourcing.go for the
// composition-time assertion that catches drift between *youtube.Service.Register
// and YouTubeRegistrar.Register before the wire.)
