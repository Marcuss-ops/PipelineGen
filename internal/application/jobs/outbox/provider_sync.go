// Package outbox — provider_sync handler.
//
// provider.sync.requested events dispatch a real sync request per
// provider. Schema v1 envelope (see providerSyncRequest below) is the
// contract.
//
// Per the Operational Readiness PR (June 2026) the dispatch rules are:
//
//   - schema_version MUST be exactly "provider.sync.requested.v1". Other
//     values are terminal (no retry — producer upgrades).
//
//   - provider switch (allowlist: drive|youtube|stock):
//
//   - stock     → ack nil + reason="fetch_only_by_design".
//     Stock has no inbound sync path (it resolves
//     sources on-demand via the YouTube channel lister,
//     rather than ingesting a provider catalog). Validating
//     the envelope + emitting the audit log is the only
//     correct action. Producers must not retry.
//
//   - drive | youtube → enqueue a real sync job onto jobs.Service
//     (the canonical async pipeline) with JobType
//     "provider.sync.drive" / "provider.sync.youtube".
//     If jobs.Service is unavailable, OR the enqueue
//     fails, the handler returns an error → outbox
//     retries per the pool's backoff. The outbox pool
//     dead-letters the event after max_attempts; the
//     operator then sees a sync that couldn't even be
//     enqueued.
//
//   - unknown   → terminal error wrapped with ErrUnknownProvider
//     so a producer typo is loud, not silent.
//
//   - account_id is INTERNAL — credentials NEVER appear in the payload.
//     The handler resolves them through jobs.Service → internal/credentials
//     (existing pipeline). For now we just pass account_id through
//     opaquely.
//
//   - cursor is used ONLY in mode=incremental. mode=full ignores cursor.
//
// Behaviour summary:
//   - stock / enqueue-success            → MarkCompleted.
//   - enqueue-failure on drive|youtube   → non-nil error → retry.
//   - terminal schema / provider errors  → non-nil error → outbox pool
//     dead-letters.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const (
	providerSyncSchemaVersion = "provider.sync.requested.v1"

	providerSyncProviderDrive   = "drive"
	providerSyncProviderYouTube = "youtube"
	providerSyncProviderStock   = "stock"

	providerSyncModeIncremental = "incremental"
	providerSyncModeFull        = "full"

	// ProviderSyncJobTypeDrive / ProviderSyncJobTypeYouTube are the
	// canonical job types enqueued by this handler. The handler for
	// these job types lives in appjobs alongside the other system jobs
	// (and is registered alongside them).
	ProviderSyncJobTypeDrive   = "provider.sync.drive"
	ProviderSyncJobTypeYouTube = "provider.sync.youtube"
)

// ErrUnknownProvider is a TERMINAL error: producer typed a provider
// that doesn't exist. Wrap with errors.Is to detect.
var ErrUnknownProvider = errors.New("provider.sync.requested: unknown provider (terminal)")

// ErrInvalidMode is a TERMINAL error: producer typed mode incorrectly.
var ErrInvalidMode = errors.New("provider.sync.requested: invalid mode (terminal)")

// JobsEnqueuer is the minimum surface the provider_sync handler needs
// from the jobs service. Declared locally so the outbox package depends
// only on the canonical EnqueueRequest shape from domain/job.
type JobsEnqueuer interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// providerSyncRequest is the canonical v1 envelope.
//
// Required: schema_version, event_id, requested_at, provider, mode,
// scope.resource, idempotency_key.
//
// Credentials NEVER appear here. account_id is opaque.
type providerSyncRequest struct {
	SchemaVersion string `json:"schema_version"`
	EventID       string `json:"event_id"`
	RequestedAt   string `json:"requested_at,omitempty"` // RFC3339 UTC
	TraceID       string `json:"trace_id,omitempty"`
	Provider      string `json:"provider"` // drive|youtube|stock
	Mode          string `json:"mode"`     // incremental|full
	Scope         struct {
		AccountID   string   `json:"account_id,omitempty"`
		Resource    string   `json:"resource"` // assets|channels|deliveries|folders
		ResourceIDs []string `json:"resource_ids,omitempty"`
	} `json:"scope"`
	Cursor         string `json:"cursor,omitempty"` // incremental only
	DryRun         bool   `json:"dry_run,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
	RequestedBy    string `json:"requested_by,omitempty"` // system|operator|workflow
}

// ProviderSyncHandler is the real handler for provider.sync.requested.v1.
//
// jobs is OPTIONAL: nil means drive|youtube dispatch is impossible
// (the handler returns an error → outbox retries; never silently
// acks). Injecting a nil service is the explicit "not wired in this
// deployment" affordance.
type ProviderSyncHandler struct {
	log  *zap.Logger
	jobs JobsEnqueuer
}

// NewProviderSyncHandler builds a ProviderSyncHandler. log nil → nop.
// jobs nil means drive|youtube events return an error (no silent ack).
func NewProviderSyncHandler(log *zap.Logger, jobs JobsEnqueuer) *ProviderSyncHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &ProviderSyncHandler{log: log.Named("provider_sync"), jobs: jobs}
}

// EventType implements outboxevents.Handler.
func (h *ProviderSyncHandler) EventType() string {
	return outboxevents.EventProviderSyncRequested
}

// IdempotencyKey declares the canonical handler-level idempotency
// identity for provider.sync.requested events. Static — derived from
// the schema_version literal — so the HandlerRegistry.Register
// fail-closed panic can fire at init time if a future refactor
// forgets the declaration. Mirrors IndexingHandler.IdempotencyKey
// (godlike/06 SSOT — one canonical owner per fact).
func (h *ProviderSyncHandler) IdempotencyKey() string {
	return outboxevents.EventProviderSyncRequested + "." + providerSyncSchemaVersion
}

func (h *ProviderSyncHandler) validate(r *providerSyncRequest) error {
	if r.SchemaVersion != providerSyncSchemaVersion {
		return fmt.Errorf("provider.sync.requested: schema_version mismatch (got %q, want %q)", r.SchemaVersion, providerSyncSchemaVersion)
	}
	if r.EventID == "" {
		return fmt.Errorf("provider.sync.requested: event_id is required")
	}
	if r.Provider == "" {
		return fmt.Errorf("provider.sync.requested: provider is required")
	}
	switch r.Provider {
	case providerSyncProviderDrive, providerSyncProviderYouTube, providerSyncProviderStock:
		// OK
	default:
		return fmt.Errorf("%w (got %q, allowed: drive|youtube|stock)", ErrUnknownProvider, r.Provider)
	}
	if r.Mode == "" {
		r.Mode = providerSyncModeIncremental
	}
	switch r.Mode {
	case providerSyncModeIncremental, providerSyncModeFull:
		// OK
	default:
		return fmt.Errorf("%w (got %q, allowed: incremental|full)", ErrInvalidMode, r.Mode)
	}
	if r.Scope.Resource == "" {
		return fmt.Errorf("provider.sync.requested: scope.resource is required")
	}
	switch r.Scope.Resource {
	case "assets", "channels", "deliveries", "folders":
		// OK
	default:
		return fmt.Errorf("provider.sync.requested: scope.resource=%q not in allowlist (assets|channels|deliveries|folders)", r.Scope.Resource)
	}
	if r.IdempotencyKey == "" {
		return fmt.Errorf("provider.sync.requested: idempotency_key is required")
	}
	return nil
}

// Handle parses the v1 envelope, dispatches by provider, and returns
// nil only when the per-provider action has completed.
func (h *ProviderSyncHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var req providerSyncRequest
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		h.log.Warn("provider.sync.requested payload parse failed (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.Error(err),
		)
		return fmt.Errorf("provider.sync.requested: parse: %s", err.Error())
	}
	if err := h.validate(&req); err != nil {
		h.log.Warn("provider.sync.requested envelope validation failed",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Error(err),
		)
		return err
	}

	h.log.Info("provider.sync.requested received",
		zap.String("provider", req.Provider),
		zap.String("mode", req.Mode),
		zap.String("resource", req.Scope.Resource),
		zap.String("account_id", req.Scope.AccountID),
		zap.String("idempotency_key", req.IdempotencyKey),
		zap.String("event_id", req.EventID),
		zap.Bool("dry_run", req.DryRun),
		zap.String("requested_by", req.RequestedBy),
		zap.Int64("outbox_id", evt.ID),
		zap.Int("attempt", evt.AttemptCount),
	)

	switch req.Provider {
	case providerSyncProviderStock:
		// Stock has no inbound sync path. Per dictate (2): ack nil with
		// reason=", do NOT enqueue, do NOT retry. Duplicate events
		// collapse via outbox event_key.
		h.log.Info("provider.sync.stock acknowledged — fetch_only_by_design (no inbound sync)",
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.String("event_id", req.EventID),
			zap.String("reason", "fetch_only_by_design"),
			zap.Int64("outbox_id", evt.ID),
		)
		return nil

	case providerSyncProviderDrive, providerSyncProviderYouTube:
		return h.dispatchToJobs(ctx, evt, &req)

	default:
		// defensive — validate already screens this
		return fmt.Errorf("%w (got %q)", ErrUnknownProvider, req.Provider)
	}
}

// dispatchToJobs enqueues a real sync job onto jobs.Service. If jobs
// is not wired OR the enqueue fails, the handler returns an error so
// the outbox pool retries per its backoff. NEVER mark Completed when
// we don't know whether the work actually started — that's how sync
// requests silently disappear in production.
func (h *ProviderSyncHandler) dispatchToJobs(ctx context.Context, evt outboxevents.Event, req *providerSyncRequest) error {
	if h.jobs == nil {
		h.log.Warn("provider.sync.requested has no jobs.Service wired — refusing as retryable",
			zap.String("provider", req.Provider),
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.Int64("outbox_id", evt.ID),
		)
		return fmt.Errorf("provider.sync.requested: jobs.Service not wired (provider=%s)", req.Provider)
	}
	jobType := ProviderSyncJobTypeDrive
	if req.Provider == providerSyncProviderYouTube {
		jobType = ProviderSyncJobTypeYouTube
	}

	// Build the canonical payload. Credentials NEVER appear here; they
	// are resolved by the job's handler in internal/application/jobs.
	payload := map[string]any{
		"idempotency_key":  req.IdempotencyKey,
		"event_id":         req.EventID,
		"requested_at":     req.RequestedAt,
		"provider":         req.Provider,
		"mode":             req.Mode,
		"resource":         req.Scope.Resource,
		"account_id":       req.Scope.AccountID,
		"resource_ids":     req.Scope.ResourceIDs,
		"cursor":           req.Cursor,
		"dry_run":          req.DryRun,
		"requested_by":     req.RequestedBy,
		"trace_id":         req.TraceID,
		"outbox_event_id":  evt.ID,
		"aggregate_id":     evt.AggregateID,
		"requested_at_now": time.Now().UTC().Format(time.RFC3339),
	}
	if req.RequestedAt == "" {
		payload["requested_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	project := req.Scope.AccountID
	if project == "" {
		project = req.Provider
	}
	enqReq := &job.EnqueueRequest{
		Type:          jobType,
		Project:       project,
		VideoName:     req.IdempotencyKey,
		Payload:       payload,
		ActiveKey:     "provider-sync:" + req.IdempotencyKey,
		CorrelationID: req.TraceID,
		MaxRetries:    3,
	}
	enqueuedJob, err := h.jobs.Enqueue(ctx, enqReq)
	if err != nil {
		h.log.Warn("provider.sync.requested enqueue failed — will retry",
			zap.String("provider", req.Provider),
			zap.String("job_type", jobType),
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(err),
		)
		return fmt.Errorf("provider.sync.requested enqueue(%s): %w", jobType, err)
	}
	var jobID string
	if enqueuedJob != nil {
		jobID = enqueuedJob.ID
	}
	h.log.Info("provider.sync.requested dispatched onto jobs.Service",
		zap.String("provider", req.Provider),
		zap.String("job_type", jobType),
		zap.String("job_id", jobID),
		zap.String("idempotency_key", req.IdempotencyKey),
		zap.Int64("outbox_id", evt.ID),
	)
	return nil
}
