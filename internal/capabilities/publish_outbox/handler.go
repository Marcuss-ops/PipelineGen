// Package publish_outbox — handler.go (FASE 3 / Push 3.1c, July 2026).
//
// Publisher worker for the FASE 3 Promote→Publish step of the
// saga. Drains `artifact.publish_requested.v1` events from the
// outbox_events table (via outboxevents.HandlerRegistry) and
// forwards each event to staging.Store.Stage (the application's
// canonical staging port). Store.Stage internally performs the
// TX-aware Repository.InsertWithOutbox (Push 3.1c primitive)
// which co-emits an `artifact.staged.v1` follow-up event for
// the Drive-upload handler (forward-pointer).
//
// godlike/06 SSOT: this handler is the SOLE canonical consumer
// of `artifact.publish_requested.v1` events. The composition
// root registers it exactly once via outboxevents.HandlerRegistry
// inside BuildOutboxBundle.
//
// godlike/07 fail-closed: every failure mode surfaces a typed
// sentinel (ErrInvalidPayload, ErrMissingFields, ErrSourceOpen).
// The outbox pool receives the returned error and decides
// retry-vs-drop based on the typed chain; the handler does NOT
// silently swallow failures.
package publish_outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// EventTypeArtifactPublishRequested is the canonical outbox
// event_type drained by this handler. The naming convention
// is `<aggregate>.<action>.<version>` (semver), as documented
// at internal/platform/sqlite/outboxevents/repository.go.
const EventTypeArtifactPublishRequested = "artifact.publish_requested.v1"

// ── Typed-error sentinel chain (godlike/07) ────────────────────────────

var (
	// ErrInvalidPayload is returned when the inbound Event's
	// PayloadJSON cannot be unmarshalled into a
	// PublishRequestPayload OR when a typed field has a
	// non-canonical value (out-of-set Requirement). Each
	// variant carries a typed wrap so log-greppers can
	// classify the failure.
	ErrInvalidPayload = errors.New("publish_outbox: payload decode failed (invalid JSON or out-of-set field)")

	// ErrMissingFields is returned when a structurally valid
	// payload is missing one of the required fields (JobID,
	// Mime, SourceURI). godlike/NO-FAKE-AVAILABILITY: a
	// half-specified publish request is harder to diagnose
	// than a rejected one, so the canonical fail-closed gate
	// rejects early.
	ErrMissingFields = errors.New("publish_outbox: required payload field empty (job_id, mime, or source_uri)")

	// ErrSourceOpen is returned when the SourceURI cannot be
	// opened as an io.Reader. Today only `file://` (or a
	// plain absolute path) is supported; future push adds
	// network schemes (http, s3, gs).
	ErrSourceOpen = errors.New("publish_outbox: source_uri could not be opened (file:// required; verify path exists)")
)

// PublishRequestPayload is the canonical JSON shape of an
// `artifact.publish_requested.v1` event's payload. Upstream
// producers (voiceover pipeline, scripts pipeline, ...) emit
// events with this schema; the handler decodes + validates
// each event against this struct before invoking Store.Stage.
//
// godlike/06 SSOT: the payload shape is the SOLE canonical
// interface between producer (upstream pipeline) and consumer
// (this handler). Adding a field is backward-compatible;
// renaming a field is a breaking schema change requiring
// producer + consumer coordinated re-release.
type PublishRequestPayload struct {
	// JobID identifies the parent job this publish request
	// belongs to. FK-by-convention to `jobs.id`.
	JobID string `json:"job_id"`

	// Mime is the canonical IANA media type for the staged
	// artifact (audited by stage store's Validate gate).
	Mime string `json:"mime"`

	// Requirement is the per-artifact "required vs optional"
	// policy (FASE 3 (b)). Defaults to "optional" when
	// unspecified (conservative default). Must be in the
	// canonical artifact.Requirement set.
	Requirement string `json:"requirement,omitempty"`

	// Destination is the canonical delivery.DestinationKey
	// (e.g., "drive:voiceover/test"). The downstream Drive-
	// upload handler uses this to resolve the destination
	// folder + retry policy.
	Destination string `json:"destination"`

	// SourceURI is the canonical location of the source bytes
	// to read. Today only file:// (or a plain absolute path)
	// is implemented; the handler opens it as *os.File and
	// passes the io.Reader to Store.Stage.
	SourceURI string `json:"source_uri"`

	// EventKey is the canonical idempotency key the upstream
	// producer attached to the publish request. Forward-
	// pointer: the handler does NOT use evt.EventKey directly
	// (the outbox pool dedupes via the event_key column), but
	// the field is preserved in the payload so a downstream
	// correlator (logs + dashboards) can trace the
	// producer→consumer chain.
	EventKey string `json:"event_key,omitempty"`
}

// ── Handler — canonical outboxevents.Handler implementation ─────────────

// Compile-time assertion: *Handler satisfies outboxevents.Handler.
var _ outboxevents.Handler = (*Handler)(nil)

// Handler is the canonical Promote→Publish worker that
// drains `artifact.publish_requested.v1` events from the
// outbox_events table and forwards each event to
// staging.Store.Stage. Push 3.1c closes the FASE 3 Promote
// step (3.1b inventoried the forward-pointer; 3.1c wires the
// consumer + the TX-aware emit).
type Handler struct {
	// store is the canonical FASE 3 staging port. The Handler
	// composes on top: it does NOT re-implement hashing / file
	// management — Store.Stage owns those (Store.Stage then
	// internally uses Repository.InsertWithOutbox).
	store staging.Store

	// log is the canonical zap.Logger for structured event
	// emission (zap.String for the canonical fields).
	log *zap.Logger

	// nowFn is overridable for tests (default = time.Now). UTC
	// is enforced via the helper to match PipelineGen SSOT.
	nowFn func() time.Time
}

// NewHandler constructs the canonical FASE 3 Publisher worker.
// godlike/07 fail-fast at construction: caller MUST supply a
// non-nil store + non-nil log.
func NewHandler(store staging.Store, log *zap.Logger) (*Handler, error) {
	if store == nil {
		return nil, fmt.Errorf("publish_outbox.NewHandler: store is required")
	}
	if log == nil {
		return nil, fmt.Errorf("publish_outbox.NewHandler: log is required")
	}
	return &Handler{
		store: store,
		log:   log,
		nowFn: func() time.Time { return time.Now().UTC() },
	}, nil
}

// EventType returns the canonical event_type this handler
// consumes. The outbox HandlerRegistry uses this for routing.
func (h *Handler) EventType() string {
	return EventTypeArtifactPublishRequested
}

// IdempotencyKey returns the canonical handler-registration
// idempotency key. The outbox HandlerRegistry uses this to
// reject duplicate registrations of the same handler. Stable
// across instances.
func (h *Handler) IdempotencyKey() string {
	return EventTypeArtifactPublishRequested
}

// Handle decodes the inbound event's PayloadJSON as a
// PublishRequestPayload, validates required fields, opens the
// SourceURI as an io.Reader, and forwards the request to
// Store.Stage. Store.Stage then performs the FASE 3 (a) "Stage
// verified" step + co-emits the `artifact.staged.v1` follow-up
// event via Repository.InsertWithOutbox (the canonical atomic
// commit primitive).
//
// godlike/07 fail-closed: every failure mode surfaces a typed
// sentinel — the handler does NOT swallow errors. The outbox
// pool decides retry-vs-drop based on the typed chain.
func (h *Handler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var req PublishRequestPayload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		return fmt.Errorf("%w: json decode (raw=%q): %v", ErrInvalidPayload, evt.PayloadJSON, err)
	}

	// Validate required fields. Empty-after-trim means "field
	// is unspecified"; reject BEFORE filesystem access.
	if strings.TrimSpace(req.JobID) == "" {
		return fmt.Errorf("%w: job_id (raw=%q)", ErrMissingFields, req.JobID)
	}
	if strings.TrimSpace(req.Mime) == "" {
		return fmt.Errorf("%w: mime (raw=%q)", ErrMissingFields, req.Mime)
	}
	if strings.TrimSpace(req.SourceURI) == "" {
		return fmt.Errorf("%w: source_uri (raw=%q)", ErrMissingFields, req.SourceURI)
	}

	// Validate Requirement canonical-set membership. Empty
	// defaults to "optional" (conservative default for new
	// payloads); any other out-of-set value is FAIL-CLOSED.
	requirement := artifact.Requirement(strings.TrimSpace(req.Requirement))
	if requirement == "" {
		requirement = artifact.RequirementOptional
	}
	if !requirement.IsValid() {
		return fmt.Errorf("%w: requirement=%q not in canonical set (allowed: optional, required)", ErrInvalidPayload, req.Requirement)
	}

	// Open the source URI as an io.Reader. Today only local
	// files are supported (file:// stripped; a plain absolute
	// path passes through unchanged).
	sourcePath := strings.TrimPrefix(strings.TrimSpace(req.SourceURI), "file://")
	f, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("%w: path=%q: %v", ErrSourceOpen, sourcePath, err)
	}
	defer f.Close()

	// Forward to Store.Stage. The staging service does the
	// file-write + hash + Repository.InsertWithOutbox (which
	// atomically co-emits the artifact.staged.v1 follow-up
	// event). The handler stays thin: it does not re-implement
	// any of those steps.
	receipt, stageErr := h.store.Stage(ctx, staging.StageRequest{
		Content:     f,
		JobID:       req.JobID,
		Mime:        req.Mime,
		Requirement: requirement,
		Destination: req.Destination,
	})
	if stageErr == nil && receipt == nil {
		return fmt.Errorf("%w: Store.Stage returned nil receipt for job_id=%s", ErrInvalidPayload, req.JobID)
	}
	if stageErr != nil {
		// godlike/07: surface the FULL typed chain so the
		// outbox pool can decide retry vs drop. We do NOT
		// swallow errors.
		return fmt.Errorf("publish_outbox.Handle: Store.Stage (job_id=%s source_uri=%s): %w", req.JobID, sourcePath, stageErr)
	}

	// Structured observation: a successful stage emits the
	// canonical log line for downstream telemetry. Field
	// names are stable across releases (dashboard pin).
	h.log.Info("publish_outbox: stage materialized",
		zap.String("event_type", EventTypeArtifactPublishRequested),
		zap.String("job_id", req.JobID),
		zap.String("stage_id", receipt.ID),
		zap.String("stage_event_key", receipt.EventKey),
		zap.String("request_event_key", evt.EventKey),
		zap.String("hash", receipt.Hash),
		zap.Int64("size", receipt.Size),
		zap.String("requirement", string(requirement)),
		zap.String("destination", req.Destination),
	)
	return nil
}
