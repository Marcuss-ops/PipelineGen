package app

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/batch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/drivesync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/localimport"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/youtube"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// clipRegisterJobType is the canonical job type for async clip registration
// (PR-BATCH-REGISTER-ASYNC, July 2026). Each clip enqueued via the batch endpoint
// becomes a media.clip job; the worker handler decodes RegisterClipCommand and
// calls YouTubeRegistrar.Register off the request thread.
//
// PR-BATCH-REGISTER-ASYNC-CONSTANT (forward-pointer): promote this constant
// to appjobs.TypeClipRegister and add a JobPolicy entry in Compose().
const clipRegisterJobType = "media.clip"

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
	driveUploader *driveutil.Uploader,
	assetTreeSvc *assettree.Service,
	providerRegistry *providers.Registry,
	clipsHandler *clipsapi.Handler,
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
		enrichment: &sourcingEnrichmentAdapter{handler: clipsHandler},
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
	ytSvc := youtube.NewService(
		&sourcingFetchAdapter{registry: providerRegistry},
		&sourcingClipStoreAdapter{repo: clipsRepo},
		&sourcingPublisherAdapter{publisher: publisher},
		&sourcingTranscriberAdapter{cfg: cfg, log: log},
		&sourcingMetadataAdapter{cfg: cfg, admin: driveUploader, reader: driveUploader, log: log},
		ytIndex,
		ytEnrich,
		&zapSourcingLogger{log: log},
	)

	// P0-1 / commit 2: BatchRegistrar sub-service (PR-BATCH-REGISTER-ASYNC).
	// The synchronous YouTubeRegistrar loop is replaced with an async
	// ClipJobEnqueuer adapter. Each clip becomes an independent media.clip
	// job enqueued via appjobs.Service.Enqueue; the worker handler
	// (registered below) decodes RegisterClipCommand and calls ytSvc.Register
	// off the request thread.
	//
	// PR-BATCH-REGISTER-ASYNC-CONSTANT (forward-pointer): extract
	// clipRegisterJobType to a shared package-level constant across
	// adapter + handler + Compose() JobPolicy entry.
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
		if err := jobsSvc.RegisterHandler(clipRegisterJobType, appjobs.HandlerFunc(func(ctx context.Context, j *jobdomain.Job, tools *appjobs.JobTools) (map[string]any, error) {
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

	// P0-1 / commit 4 (this commit): LocalImporter sub-service.
	// 3-dep ctor: jobs + scanner + log (all nil at this composition site
	// today; preserves historical behaviour — file-scanner-not-configured
	// and jobs-port-not-configured errors fire when CLI invokes them in
	// dry-run or non-dry-run paths respectively).
	localSvc := localimport.NewService(nil, nil, &zapSourcingLogger{log: log})

	// P0-1 / commit 5 (this commit): façade di pulizia. 5-arg call:
	// 4 sub-services + log. The historic 14-dep ctor collapses to 5
	// typed sub-service handles. Jobs + scanner ports no longer
	// proxied through the façade — they are owned by drivesync /
	// localimport sub-packages directly (composition site passes nil
	// to both today; future sites inject real JobsPort + FileScannerPort
	// adapters into the respective NewService call sites above).
	return sourcing.NewService(ytSvc, batchSvc, drvSvc, localSvc, &zapSourcingLogger{log: log})
}

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
	// Note: Service.Enqueue takes the request BY VALUE (not pointer)
	// per jobdomain.JobStore interface — godlike/07 typed-error contract.
	// (Earlier draft used &jobdomain.EnqueueRequest{...}; that produced
	// a type-mismatch build error at the call site.)
	job, err := a.svc.Enqueue(ctx, jobdomain.EnqueueRequest{
		Type:    clipRegisterJobType,
		Payload: json.RawMessage(payload),
	})
	if err != nil {
		return "", fmt.Errorf("clipJobEnqueuerAdapter: enqueue media.clip: %w", err)
	}
	return job.ID, nil
}

// Compile-time assertion: clipJobEnqueuerAdapter satisfies batch.ClipJobEnqueuer.
var _ batch.ClipJobEnqueuer = (*clipJobEnqueuerAdapter)(nil)
