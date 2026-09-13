package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// QueuePrepareEnqueuer submits the overlay.prepare job for the run's
// pre-timing OverlayIntents to the central RenderingGen queue. Unlike the
// render enqueuer it is fire-and-forget: prepare resolves templates and
// prefetches entity assets independently of the timing-frozen render path
// and must never block the pipeline. The job id is deterministic
// ("prepare-"+planID) so replays are idempotent.
type QueuePrepareEnqueuer struct {
	client RenderQueueClient
}

// NewQueuePrepareEnqueuer creates a queue-backed prepare enqueuer.
func NewQueuePrepareEnqueuer(client RenderQueueClient) (*QueuePrepareEnqueuer, error) {
	if client == nil {
		return nil, fmt.Errorf("queue prepare enqueuer requires a queue client")
	}
	return &QueuePrepareEnqueuer{client: client}, nil
}

// EnqueuePrepare submits the prepare job and returns immediately. A job that
// already exists (ErrJobExists) is treated as idempotent success so a retry
// never double-prepares.
func (e *QueuePrepareEnqueuer) EnqueuePrepare(ctx context.Context, req capoverlay.PrepareRequest) error {
	if e == nil || e.client == nil {
		return fmt.Errorf("queue prepare enqueuer is not configured")
	}
	if err := req.Validate(); err != nil {
		return err
	}
	// Prepare has its own deliberately small wire contract. The capability
	// intent is rich (entity identity, payload and asset refs), but the worker
	// only needs template_id + PENDING timing to warm its registry; the asset
	// refs travel in the queue manifest. Projecting here prevents the rich
	// OverlayIntent fields (for example kind/entity) from crossing a strict
	// renderinggen.overlay-prepare.v1 boundary.
	spec, err := json.Marshal(newOverlayPrepareWire(req))
	if err != nil {
		return fmt.Errorf("chronon queue prepare marshal: %w", err)
	}
	job := RenderQueueJob{
		ID:          "prepare-" + req.PlanID,
		JobType:     capoverlay.JobTypePrepare,
		OverlaySpec: spec,
		Assets:      prepareAssets(req.Intents),
	}
	if err := e.client.Submit(ctx, job); err != nil {
		if errors.Is(err, ErrJobExists) {
			return nil // idempotent replay
		}
		return fmt.Errorf("chronon queue prepare submit failed: %w", err)
	}
	return nil
}

type overlayPrepareWire struct {
	SchemaVersion string                     `json:"schema_version"`
	PlanID        string                     `json:"plan_id"`
	VideoID       string                     `json:"video_id"`
	Width         int                        `json:"width"`
	Height        int                        `json:"height"`
	FPSNum        int                        `json:"fps_num"`
	FPSDen        int                        `json:"fps_den"`
	Intents       []overlayPrepareIntentWire `json:"intents"`
}

type overlayPrepareIntentWire struct {
	TemplateID  string `json:"template_id"`
	TimingState string `json:"timing_state"`
}

func newOverlayPrepareWire(req capoverlay.PrepareRequest) overlayPrepareWire {
	wire := overlayPrepareWire{
		SchemaVersion: req.SchemaVersion,
		PlanID:        req.PlanID,
		VideoID:       req.VideoID,
		Width:         req.Width,
		Height:        req.Height,
		FPSNum:        req.FPSNum,
		FPSDen:        req.FPSDen,
		Intents:       make([]overlayPrepareIntentWire, len(req.Intents)),
	}
	for i, intent := range req.Intents {
		wire.Intents[i] = overlayPrepareIntentWire{TemplateID: intent.TemplateID, TimingState: string(intent.TimingState)}
	}
	return wire
}

// prepareAssets collects the entity-image assets referenced by the intents,
// deduplicated by content hash, so the queue worker can prefetch them during
// the prepare phase.
func prepareAssets(intents []capoverlay.OverlayIntent) []RenderQueueAsset {
	var assets []RenderQueueAsset
	seen := make(map[string]bool)
	for _, intent := range intents {
		for _, ref := range intent.Payload.AssetRefs {
			hash := strings.ToLower(strings.TrimSpace(ref.SHA256))
			if hash == "" || seen[hash] {
				continue
			}
			seen[hash] = true
			asset := RenderQueueAsset{Hash: hash, URL: ref.URL, LocalPath: ref.LocalPath}
			if strings.HasPrefix(ref.URL, "http") {
				asset.SourceURL = ref.URL
			}
			assets = append(assets, asset)
		}
	}
	return assets
}
