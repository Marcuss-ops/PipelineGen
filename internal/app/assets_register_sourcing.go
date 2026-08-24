// Package app — SourcingService façade composition root.
//
// This file is the composition site for the unified SourcingService
// (PR-WIRE-ASSETS-CAPABILITY-SPLIT, July 2026). It builds the
// YouTubeRegistrar + BatchRegistrar + DriveFolderSynchronizer +
// LocalImporter sub-services and wires them into the slim
// sourcing.NewService ctor. PR-BATCH-REGISTER-ASYNC (July 2026)
// added the jobsSvc parameter so the batch sub-service can
// enqueue media.clip jobs async via appjobs.Service.Enqueue.
package app

import (
	"context"

	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/batch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/drivesync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/localimport"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/youtube"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive/resolver"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// newAssetRegisterService builds the SourcingService façade. After P0-1 /
// commit 1 (June 2026) it first constructs the YouTubeRegistrar sub-service
// (with v2 adapters that wrap legacy ports) and then injects that, plus the
// remaining JobsPort/FileScannerPort needed by SyncDriveFolder + LocalToDrive
// (not yet extracted, planned in commits 3 and 4 of P0-1), into the slim
// sourcing.NewService ctor (now 4 args, was 14 historically).
//
// PR-BATCH-REGISTER-ASYNC (July 2026): added jobsSvc parameter. The batch
// sub-service now uses a ClipJobEnqueuer (wrapping jobsSvc.Enqueue) instead
// of calling YouTubeRegistrar.Register synchronously. Each clip becomes an
// independent media.clip job; yt-dlp + cut + Drive upload happen off-thread.
// The media.clip handler is registered here (inline) so ytSvc.Register is
// reachable from the worker dispatch path.
func newAssetRegisterService(
	cfg *config.Config,
	log *zap.Logger,
	clipsRepo *assetsrepo.ClipsRepository,
	textTrackRepo asset.TextTrackRepository,
	driveUploader *driveutil.Uploader,
	lifecycle driveutil.FileLifecycle,
	assetTreeSvc *assettree.Service,
	providerRegistry *providers.Registry,
	clipEnricher appclips.ClipEnricher,
	dispatcher *outbox.Dispatcher,
	publisher delivery.Publisher,
	jobsSvc *appjobs.Service,
) *sourcing.Service {
	// Build the YouTube sub-service with v2 adapters (June 2026, P0-1 / commit 1).
	// The 2 v2 adapters absorb 6 legacy ports (IndexDispatcher + AssetTree +
	// Jobs + Search + Config + legacy Enrichment) into the YouTubeService's
	// 8-port budget per architecture/policy.yaml::max_constructor_deps.
	ytIndex := &youtubeIndexDispatcherAdapter{disp: dispatcher, tree: assetTreeSvc}
	ytEnrich := &youtubeEnrichmentAdapter{
		// Card 10 (July 2026): wire the canonical ClipEnricher typed
		// port instead of the raw *clips.Handler. The descriptor's
		// exposed surface is now strictly routes + job handlers.
		enrichment: &sourcingEnrichmentAdapter{enricher: clipEnricher},
		config:     &sourcingConfigAdapter{cfg: cfg},
		search:     &sourcingSearchAdapter{registry: providerRegistry},
		// jobs port intentionally nil today (composition root signature does
		// not yet expose JobsPort; this preserves historical behaviour where
		// SyncDriveFolder + LocalToDrive were also non-functional in this
		// composition site, and matches what the thinker audit suggested as
		// the conservative interpretation).
	}
	// PR-YT-DRIVE-SERVICE-COMMENT-CLEANUP (July 2026): the legacy
	// `&sourcingDriveAdapter{drive: driveUploader}` 3rd positional arg
	// is dropped — the corresponding field on youtube.Service was
	// retired (zero production reads; Publisher is the canonical Drive
	// upload canal since FASE 5). FASE 0.3 (July 2026): the
	// `sourcingDriveAdapter` struct itself + `sourcing.DrivePort`
	// interface are now PHYSICALLY RETIRED via PR-YT-DRIVE-LEGACY-RETIRE
	// (godlike/07 no-fake-availability: zero live concrete remained
	// post-CUTOVER; deleting a rot interface is the canonical hygiene).
	// See architecture/deprecations.yaml#PR-YT-DRIVE-LEGACY-RETIRE
	// + internal/app/youtube_adapters_drive.go for the comment audit-pin.
	ytSvc := youtube.NewService(youtube.ServiceDeps{
		Fetcher:     &sourcingFetchAdapter{registry: providerRegistry},
		Clips:       &sourcingClipStoreAdapter{repo: clipsRepo},
		Publisher:   &sourcingPublisherAdapter{publisher: publisher},
		Transcriber: &sourcingTranscriberAdapter{cfg: cfg, log: log},
		// P1-5 CUTOVER (July 2026): lifecycle wired through from composition root.
		// TrashFile now routes via FileLifecycle.Trash (no Admin fallback).
		Metadata:      &sourcingMetadataAdapter{cfg: cfg, admin: driveUploader, reader: driveUploader, lifecycle: lifecycle, publisher: publisher, log: log},
		IndexDisp:     ytIndex,
		Enrichment:    ytEnrich,
		Log:           &zapSourcingLogger{log: log},
		TextTrackRepo: textTrackRepo,
	}).WithRequireDrive(cfg.Features.MediaDriveRequired)

	// P0-1 / commit 2: BatchRegistrar sub-service (PR-BATCH-REGISTER-ASYNC).
	// The synchronous YouTubeRegistrar loop is replaced with an async
	// ClipJobEnqueuer adapter. Each clip becomes an independent media.clip
	// job enqueued via appjobs.Service.Enqueue; the worker handler
	// (registered below) decodes RegisterClipCommand and calls ytSvc.Register
	// off the request thread.
	//
	// PR-BATCH-REGISTER-ASYNC-CONSTANT (closed July 2026): clipRegisterJobType
	// is now appjobs.TypeClipRegister (canonical SSOT per godlike/06).
	batchEnqueuer := &clipJobEnqueuerAdapter{svc: jobsSvc}
	batchSvc := batch.NewService(batchEnqueuer, &zapSourcingLogger{log: log})

	// PR-BATCH-REGISTER-ASYNC: register the media.clip handler so the
	// worker can process enqueued clip registration jobs. The handler
	// decodes RegisterClipCommand from the job payload and calls
	// ytSvc.Register (the canonical single-clip registration pipeline).
	// godlike/07 fail-closed: nil jobsSvc → skip handler registration
	// (the batch enqueuer adapter also returns ErrEnqueuerNotWired on
	// nil svc, so the surface is consistent).
	if jobsSvc != nil {
		if err := jobsSvc.RegisterHandler(appjobs.TypeClipRegister, appjobs.HandlerFunc(func(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
			var cmd sourcing.RegisterClipCommand
			if len(j.Payload) > 0 {
				if err := json.Unmarshal(j.Payload, &cmd); err != nil {
					return nil, fmt.Errorf("media.clip: invalid payload: %w", err)
				}
			}
			res, err := ytSvc.Register(ctx, cmd)
			if err != nil {
				return nil, fmt.Errorf("media.clip: Register: %w", err)
			}
			return map[string]any{
				"ok":              res.OK,
				"clip_id":         res.ClipID,
				"duplicate":       res.Duplicate,
				"name":            res.Name,
				"drive_link":      res.DriveLink,
				"delivery_status": res.DeliveryStatus,
				"message":         res.Message,
			}, nil
		})); err != nil {
			log.Warn("PR-BATCH-REGISTER-ASYNC: failed to register media.clip handler", zap.Error(err))
		} else {
			log.Info("PR-BATCH-REGISTER-ASYNC: registered media.clip handler (async clip registration)")
		}
	}

	// P0-1 / commit 3: DriveFolderSynchronizer sub-service (this commit).
	// 2-dep ctor: jobs (currently nil at this composition site; preserves
	// the historical fail-closed `jobs port not configured` error path)
	// + log. Future composition sites will inject a real JobsPort adapter.
	drvSvc := drivesync.NewService(nil, &zapSourcingLogger{log: log})

	// PR-CLIPS-ENQUEUE-ONLY (July 2026): LocalImporter sub-service.
	// 2-dep ctor: jobs + log (nil at this composition site; the
	// enqueue path no longer pre-scans the directory — the worker
	// is the sole owner of filesystem scanning).
	localSvc := localimport.NewService(nil, &zapSourcingLogger{log: log})

	// P0-1 / commit 5 (this commit): façade di pulizia. 5-arg call:
	// 4 sub-services + log. The historic 14-dep ctor collapses to 5
	// typed sub-service handles. Jobs + scanner ports no longer
	// proxied through the façade — they are owned by drivesync /
	// localimport sub-packages directly (composition site passes nil
	// to both today; future sites inject real JobsPort + FileScannerPort
	// adapters into the respective NewService call sites above).
	//
	// PR-RESOLVER-PORT-EXTRACT (SEMANTIC-LOCATION-API Wave 7, July 2026):
	// the 5-arg NewService signature is preserved (godlike/07
	// minimum-blast-radius) — the resolver is appended via the canonical
	// fluent setter WithLocationResolver. The composition-root adapter
	// is the canonical SOLE owner of the resolver wiring per process boot;
	// when the resolver becomes mandatory, the fluent setter is
	// promoted to a 6-arg ctor (forward-pointer, godlike/06 lockstep).
	//
	// fail-closed-at-construction: NewAdapter returns (*Adapter, error)
	// so a malformed rootFolder surfaces at composition time rather than
	// silently passing through and failing at first /api/media/register
	// call. The error is logged + the fluent setter is skipped so the
	// process boots; future PR may flip this to a hard-fail
	// (forward-pointer: validate-drive-bundle-gate at boot).
	resolverAdapter, resolverErr := resolver.NewAdapter(cfg.Drive, &zapSourcingLogger{log: log})
	if resolverErr != nil {
		log.Warn("PR-RESOLVER-PORT-EXTRACT: failed to construct resolver adapter; fluent setter will be skipped and a non-empty Location will fail-closed via ErrLocationResolverEmpty at runtime", zap.Error(resolverErr))
		return sourcing.NewService(ytSvc, batchSvc, drvSvc, localSvc, &zapSourcingLogger{log: log})
	}
	// Wire real Drive FolderEnsurer so semantic location fields produce
	// real Drive folder IDs (not stub-shift: prefix-mode paths).
	// The resolverFolderEnsurerAdapter wraps drive.EnsureFolderPath
	// (the canonical recursive folder-creation helper) and injects the
	// drive.Admin dependency so the resolver package avoids an import
	// cycle with its parent drive package.
	if driveUploader != nil {
		resolverAdapter = resolverAdapter.WithFolderEnsurer(
			&resolverFolderEnsurerAdapter{admin: driveUploader},
		)
		log.Info("PR-RESOLVER-PORT-EXTRACT: real Drive FolderEnsurer wired (EnsureFolderPath via drive.Admin)")
	} else {
		log.Warn("PR-RESOLVER-PORT-EXTRACT: driveUploader is nil — FolderEnsurer NOT wired; non-empty Location will fail-closed via ErrFolderEnsurerNotWired at runtime")
	}
	log.Info("PR-RESOLVER-PORT-EXTRACT: canonical LocationResolverPort wired into sourcing façade (Wave 7 SEMANTIC-LOCATION-API deliverable)")
	return sourcing.NewService(ytSvc, batchSvc, drvSvc, localSvc, &zapSourcingLogger{log: log}).WithLocationResolver(resolverAdapter)
}

// resolverFolderEnsurerAdapter wraps drive.EnsureFolderPath into the
// resolver.FolderEnsurer port. This adapter lives in the composition
// root (NOT in the resolver package) to break the import cycle:
// resolver is a child of drive, so it cannot import drive.Admin or
// drive.EnsureFolderPath directly.
//
// godlike/06 SSOT one-canonical-owner-per-fact: this is the canonical
// SOLE adapter between resolver.FolderEnsurer and the real Drive
// folder-creation surface.
//
// godlike/07 NO-FAKE-AVAILABILITY: the adapter calls the real
// drive.EnsureFolderPath function which walks segments and creates
// each folder via admin.GetOrCreateFolder (retry-aware, P0.4 admin
// scope). No stub-mode fallback.
type resolverFolderEnsurerAdapter struct {
	admin driveutil.Admin
}

// EnsureFolder satisfies resolver.FolderEnsurer.
func (a *resolverFolderEnsurerAdapter) EnsureFolder(ctx context.Context, rootID string, segments ...string) (string, error) {
	if a == nil || a.admin == nil {
		return "", fmt.Errorf("resolverFolderEnsurerAdapter: drive.Admin not wired")
	}
	return driveutil.EnsureFolderPath(ctx, a.admin, rootID, segments...)
}

var _ resolver.FolderEnsurer = (*resolverFolderEnsurerAdapter)(nil)

// clipJobEnqueuerAdapter bridges batch.ClipJobEnqueuer → appjobs.Service.Enqueue.
// Each RegisterClipCommand is JSON-marshalled and enqueued as a media.clip job.
// The worker handler (registered in newAssetRegisterService above) decodes the
// payload and calls ytSvc.Register off the request thread.
//
// PR-BATCH-REGISTER-ASYNC (July 2026): canonical async bridge.
type clipJobEnqueuerAdapter struct {
	svc *appjobs.Service
}

// EnqueueClip satisfies batch.ClipJobEnqueuer.
//
// PR-BATCH-REGISTER-ASYNC (July 2026): canonical async bridge.
// Uses json.RawMessage to avoid double-encoding: Service.Enqueue calls
// json.Marshal(req.Payload) internally; passing json.RawMessage ensures
// it passes through without base64-encoding (json.RawMessage implements
// json.Marshaler by returning itself).
func (a *clipJobEnqueuerAdapter) EnqueueClip(ctx context.Context, cmd sourcing.RegisterClipCommand) (string, error) {
	if a == nil || a.svc == nil {
		return "", fmt.Errorf("clipJobEnqueuerAdapter: jobs.Service not wired (composition bug — wire jobsSvc at composition time per PR-BATCH-REGISTER-ASYNC)")
	}
	payload, err := json.Marshal(cmd)
	if err != nil {
		return "", fmt.Errorf("clipJobEnqueuerAdapter: marshal RegisterClipCommand: %w", err)
	}
	// Use json.RawMessage to prevent Service.Enqueue from double-encoding.
	// Service.Enqueue internally calls json.Marshal(req.Payload); passing
	// json.RawMessage bypasses the re-encode (RawMessage.MarshalJSON returns
	// itself). Passing raw []byte would produce a base64-encoded string.
	//
	// PR-FIX-FANOUT-JOBID-COLLISION (2026-07-04): derive a payload-scoped
	// CorrelationID so fan-out children (e.g. BatchRegisterFromYouTube with
	// seconds_per_segment=N splitting one URL into N clips) do NOT collapse
	// into one JobID via Service.Enqueue's
	// FindByTypeAndCorrelation idempotency check (enqueue_service.go:97-110).
	// Without this, N children inherit the SAME base CorrelationID from
	// corid.FromContext(ctx) (HTTP request ID), so the broker dedups
	// (Type, CorrelationID) → all children return the 1st child's JobID →
	// batch silently collapses to 1 row in jobs + 1 row in media_assets.
	//
	// Derivation: base = corid.FromContext(ctx) (HTTP request ID, may be "")
	//             suffix = "clip-sha256-<16-hex-of-marshalled-payload>"
	//             empty-base fallback: "clip-sha256-<16-hex>".
	//
	// Idempotency-preserving: identical (request, payload) replays return
	// the same CorrelationID → same JobID (canonical godlike/07 fail-closed
	// dedup). Distinct payloads get distinct CorrelationIDs → distinct jobs.
	dedupSum := digest.SHA256Bytes(payload)
	cid := "clip-sha256-" + dedupSum[:16]
	if baseID := corid.FromContext(ctx); baseID != "" {
		cid = baseID + ":" + cid
	}
	job, err := a.svc.Enqueue(ctx, &job.EnqueueRequest{
		Type:          appjobs.TypeClipRegister,
		Payload:       json.RawMessage(payload),
		CorrelationID: cid,
	})
	if err != nil {
		return "", fmt.Errorf("clipJobEnqueuerAdapter: enqueue media.clip: %w", err)
	}
	return job.ID, nil
}

// Compile-time assertion: clipJobEnqueuerAdapter satisfies batch.ClipJobEnqueuer.
var _ batch.ClipJobEnqueuer = (*clipJobEnqueuerAdapter)(nil)

// ── wireSourcingAtomic (PR-SOURCING-ADAPTER-FAIL-CLOSED, July 2026) ──

// wireSourcingAtomic is the canonical fail-fast-at-composition gate for the
// SourcingAtomicPort wiring decision. It enforces the godlike/07 contract
// that a misconfiguration must surface at BOOT, NOT at first
// /api/media/register call (which would manifest as the pre-fix silent-success
// class — sourcingMetadataAdapter.UpdateCumulativeJSON / sourcingEnrichmentAdapter.EnrichAndIndex
// returned nil even when the handler was unwired, masking the composition
// bug from upstream callers).
//
// Returns:
//   - nil + sourcing.ErrSourcingCapabilitiesRequired when cfg.Features.MediaDriveRequired
//     is true but the handler is nil (composition-time invariant violation;
//     the canonical Drive-required-but-not-configured failure class).
//   - nil + sourcing.ErrSourcingCapabilitiesDisabled when the handler is nil
//     AND cfg.Features.MediaDriveRequired is false. Composition may continue
//     without sourcing capabilities (the canonical Drive-not-required-for-this-deployment
//     mode), but the gate surfaces the typed error so the deferred-at-runtime
//     fail-closed path is explicit (not a silent no-op).
//   - handler + nil on the success path: the original port is returned
//     as-is (caller's existing wiring is preserved byte-identically).
//
// godlike/06 SSOT one-canonical-owner-per-fact: this gate lives ONLY at
// internal/app/assets_register_sourcing.go (the canonical composition-root
// surface for the sourcing façade). Callers MUST call it on the
// SourcingAtomicPort-shaped wiring decision BEFORE consuming the port
// in newAssetRegisterService — the forward-pointer for integration is
// PR-SOURCING-WIRE-INTEGRATION (deadline 2026-08-15) which will thread
// the gate through the actual façade composition site.
//
// Trip-typed-error contract (godlike/07): the returned errors are wrapped
// via fmt.Errorf with %w so callers can probe with errors.Is(err,
// sourcing.ErrSourcingCapabilitiesRequired) or errors.Is(err,
// sourcing.ErrSourcingCapabilitiesDisabled) per the canonical typed-error
// contract — never via raw string match.
//
// Threading-pattern compatibility: this gate is independent of the
// SourcingAtomicPort concrete implementation (interface-only argument +
// struct{} + cfg) so it is testable in isolation with zero infrastructure
// dependencies.
func wireSourcingAtomic(cfg *config.Config, h sourcing.SourcingAtomicPort) (sourcing.SourcingAtomicPort, error) {
	if h == nil {
		if cfg != nil && cfg.Features.MediaDriveRequired {
			return nil, fmt.Errorf("sourcing: wireSourcingAtomic: %w (handler nil but cfg.Features.MediaDriveRequired=true)", sourcing.ErrSourcingCapabilitiesRequired)
		}
		return nil, fmt.Errorf("sourcing: wireSourcingAtomic: %w (handler nil)", sourcing.ErrSourcingCapabilitiesDisabled)
	}
	return h, nil
}
