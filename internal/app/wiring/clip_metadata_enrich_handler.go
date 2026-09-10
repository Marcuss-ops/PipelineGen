// Package app — clip_metadata_enrich_handler.go: durable consumer for the
// metadata.enrich.requested media outbox event (Sept 2026).
//
// When VELOX_YOUTUBE_ASYNC_ENRICHMENT is enabled, the YouTube per-segment
// pipeline commits the clip with its raw segment metadata and emits the
// enrichment intent atomically (PostgresMediaCommitter.CommitClipTextAndIndexEvent
// → AdditionalOutboxEvents). This handler consumes that intent, runs the
// canonical metadata analyzer, and persists the semantic snapshot together
// with the corresponding asset.index.requested event in ONE transaction
// (MetadataService.EnrichClip → UpdateClipMetadataAndRequestIndex).
//
// The payload is the serialized youtubetypes.ClipMetadataInput, so the
// worker needs no second read of partial state and the sync/async analysis
// paths share the exact same input shape (godlike/06 SSOT).
package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// clipMetadataEnrichHandler implements pgmedia.OutboxHandler for the
// metadata.enrich.requested event type.
type clipMetadataEnrichHandler struct {
	svc     *ytmetadata.MetadataService
	log     *zap.Logger
	metrics *observability.MetadataEnrichmentRecorder
}

var _ pgmedia.OutboxHandler = (*clipMetadataEnrichHandler)(nil)

// newClipMetadataEnrichHandler constructs the consumer. A nil metadata
// service is a composition error: the caller decides whether to abort boot
// or skip registration.
func newClipMetadataEnrichHandler(svc *ytmetadata.MetadataService, log *zap.Logger) (*clipMetadataEnrichHandler, error) {
	if svc == nil {
		return nil, fmt.Errorf("clip metadata enrichment handler: metadata service is required")
	}
	return &clipMetadataEnrichHandler{
		svc:     svc,
		log:     log,
		metrics: observability.NewMetadataEnrichmentRecorder(),
	}, nil
}

// Handle runs the analyzer + atomic metadata/index write for one claimed
// event. Errors are returned to the worker so the canonical lease-fenced
// retry/dead-letter path owns delivery (never silently dropped).
//
// Sept 2026 enrichment SRE surface: queue age (now − event created_at)
// is observed at claim time and the run outcome (total / duration /
// failures) after EnrichClip — the SAME collector family the
// synchronous use case path records through, so one dashboard covers
// both enrichment modes.
func (h *clipMetadataEnrichHandler) Handle(ctx context.Context, claim *pgmedia.OutboxClaim) error {
	if claim == nil {
		return nil
	}
	h.observeQueueAge(claim.Event.CreatedAt)
	raw := strings.TrimSpace(claim.Event.PayloadJSON)
	if raw == "" {
		return fmt.Errorf("metadata enrichment: event %d carries an empty payload", claim.Event.ID)
	}
	var in youtubetypes.ClipMetadataInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return fmt.Errorf("metadata enrichment: event %d malformed payload: %w", claim.Event.ID, err)
	}
	if in.ClipID == "" {
		in.ClipID = claim.Event.AggregateID
	}
	if in.ClipID == "" {
		return fmt.Errorf("metadata enrichment: event %d has no clip identity", claim.Event.ID)
	}
	runStart := time.Now()
	if _, err := h.svc.EnrichClip(ctx, in); err != nil {
		h.metrics.IncEnrichmentFailures()
		h.metrics.ObserveEnrichmentDuration(time.Since(runStart).Seconds())
		return fmt.Errorf("metadata enrichment: clip %q: %w", in.ClipID, err)
	}
	h.metrics.IncEnrichmentTotal()
	h.metrics.ObserveEnrichmentDuration(time.Since(runStart).Seconds())
	if h.log != nil {
		h.log.Info("async metadata enrichment committed semantic snapshot + index request",
			zap.String("clip_id", in.ClipID),
			zap.String("event_key", claim.Event.EventKey))
	}
	return nil
}

// observeQueueAge records the async intent age (now − event created_at)
// on the canonical enrichment metrics. A missing/unparseable created_at
// (zero time) is skipped — reporting a fabricated age would corrupt the
// queue-latency SLO.
func (h *clipMetadataEnrichHandler) observeQueueAge(createdAt string) {
	if h == nil || h.metrics == nil || createdAt == "" {
		return
	}
	created := timeutil.ParseRFC3339(createdAt)
	if created.IsZero() {
		return
	}
	h.metrics.ObserveEnrichmentQueueAge(time.Since(created).Seconds())
}
