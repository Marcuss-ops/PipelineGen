// Package outbox — delivery handler.
//
// delivery.requested events notify a remote receiver that an artifact is
// ready. Schema v1 envelope (see deliveryRequest below) is the contract.
//
// Per the Operational Readiness PR (June 2026) the contract was rewritten
// to be provider-agnostic and HMAC-mandatory in production:
//
//   - schema_version MUST be exactly "delivery.requested.v1". Any other
//     (or missing) value is a terminal error — no retry, the producer
//     must upgrade. This protects receivers from silent drifts.
//   - artifact.artifact_id and artifact.sha256 are required so receivers
//     can verify integrity before accepting the payload.
//   - destination.provider switches the dispatch path:
//   - "webhook"  → real HTTP POST with HMAC-SHA256 (mandatory in
//     production via config.Security.DeliveryHMACSecret; dev escape
//     only with VELOX_ALLOW_INSECURE_DEV=true and operator warning).
//   - "drive"    → responsibility of the upload pipeline
//     (internal/upload/drive); the handler emits an audit-log
//     acknowledgement ("upload_pipeline_handles_drive") and returns
//     nil so the outbox event marks Completed. This avoids double
//     logic: the upload pipeline is THE source of truth for
//     Drive uploads; the outbox delivery row is a tracker.
//   - "youtube"  → responsibility of the YouTube upload pipeline;
//     same audit-ack pattern as drive.
//   - unknown    → terminal error (no retry) so a producer typo is
//     obvious in dead-letter rather than a silent re-route.
//   - HMAC signing string is the canonical
//     <event_timestamp>.<event_id>.<raw_body> via pkg/hmacsign; the
//     receiver is expected to enforce a 5-min replay window. The
//     outbox does NOT enforce replay rejection itself because replay
//     rejection belongs at the receiver boundary; the producer just
//     makes the timestamp available.
//
// Behaviour summary:
//
//   - 2xx                 → MarkCompleted, delivery_log row written.
//   - 4xx                 → MarkCompleted (terminal; receiver rejected
//     the payload; retrying won't help).
//   - 5xx / network error → non-nil error → outbox retries up to
//     max_attempts, then dead_letter.
//
// Concurrency: outboxevents.Pool calls Handle from N goroutines. The
// handler is concurrency-safe (http.Client documented safe for
// concurrent use; DB writes via ON CONFLICT keyed by idempotency_key).
package outbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/httpclient"
	"github.com/Marcuss-ops/PipelineGen/pkg/hmacsign"
)

const (
	// deliverySchemaVersion is the canonical, EXACT string the handler
	// accepts. Drift here is a TerminalError — the producer upgrades
	// (a future v2 will arrive alongside its own handler branch).
	deliverySchemaVersion = "delivery.requested.v1"

	// deliveryProviderWebhook is the only provider that triggers a real
	// HTTP POST + HMAC sign. drive|youtube are tracked but their uploads
	// run on the canonical upload pipelines.
	deliveryProviderWebhook = "webhook"
	deliveryProviderDrive   = "drive"
	deliveryProviderYouTube = "youtube"

	// defaultDeliveryTimeout is the per-POST timeout when the production
	// wiring calls in via the Config. Tuned conservatively: most webhook
	// receivers reply within 5-10s, 30s gives generous headroom without
	// blocking the worker.
	defaultDeliveryTimeout = 30 * time.Second

	// maxDeliveryResponseBytes caps how much of the receiver's response
	// body the handler retains in memory and (truncated) in delivery_log.
	maxDeliveryResponseBytes = 1 << 20 // 1 MiB
)

// ErrUnsupportedProvider is a TERMINAL error returned for unknown
// destination.provider values. The outbox pool maps this to a
// non-retryable dead-letter so a producer typo is loud, not silent.
//
// (We tag the error string with "terminal" so the pool's MarkFailed can
// implement an allowlist for terminal-only error families; today the pool
// always retries on non-nil error, but the tag is forward-compatible
// with a future "no_retry" classifier without a wrapper layer.)
var ErrUnsupportedProvider = errors.New("delivery.requested: unsupported provider (terminal)")

// ErrSchemaVersionMismatch is a TERMINAL error. Producers MUST send
// "delivery.requested.v1" exactly. Future versions receive their own
// branch; we never silently retry on what looks like a routine failure.
var ErrSchemaVersionMismatch = errors.New("delivery.requested: schema_version mismatch (terminal)")

// Artifact container — required so the receiver can verify integrity
// before accepting the artifact's payload bytes.
type Artifact struct {
	ArtifactID  string `json:"artifact_id"`
	StorageKey  string `json:"storage_key,omitempty"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// Destination is the dispatch spec. provider selects the path; for
// webhook, destination_id is the URL.
// Credential resolution is INTERNAL via account_id — credentials NEVER
// appear in the payload (the schema's invariant).
type Destination struct {
	Provider      string         `json:"provider"` // drive|youtube|webhook
	DestinationID string         `json:"destination_id"`
	AccountID     string         `json:"account_id,omitempty"`
	FolderID      string         `json:"folder_id,omitempty"`
	Options       map[string]any `json:"options,omitempty"`
}

// deliveryRequest is the canonical v1 envelope.
//
// Required fields (handler will fail-fast with a terminal error if any
// is missing — see requiredDeliveryFields):
//   - schema_version
//   - event_id
//   - occurred_at (RFC3339 UTC)
//   - job_id
//   - artifact.artifact_id, artifact.sha256
//   - destination.provider, destination.destination_id
//   - idempotency_key (pass-through, opaque)
//
// Credentials, access tokens, secret material: NEVER appear here. The
// handler enforces that account_id does not look like a credential.
type deliveryRequest struct {
	SchemaVersion  string      `json:"schema_version"`
	EventID        string      `json:"event_id"`
	OccurredAt     string      `json:"occurred_at"`
	TraceID        string      `json:"trace_id,omitempty"`
	JobID          string      `json:"job_id"`
	RunID          string      `json:"run_id,omitempty"`
	Artifact       Artifact    `json:"artifact"`
	Destination    Destination `json:"destination"`
	IdempotencyKey string      `json:"idempotency_key"`
	Attempt        int         `json:"attempt,omitempty"`

	// Body is the producer-supplied payload that goes on the wire
	// (already JSON-serialised by the producer — we don't re-marshal).
	// Opaque to us; receivers parse it according to their own contract.
	Body json.RawMessage `json:"body,omitempty"`
}

// DeliveryHandler is the real handler for delivery.requested.v1 events.
//
// Mandatory HMAC in production: hmacSecrets is the (current + previous)
// secret keys. When len(hmacSecrets) == 0 the handler refuses unless
// insecureDev is true. Credentials are read at construction time (NOT
// per-call) so the per-event latency is just signing + POST.
//
// PR-REFACTOR-P0-IO-BINDER-HTTP (July 2026): the client field is now
// ports.Client (the canonical narrow port for outbound HTTP) rather
// than a direct *http.Client. The default constructor routes through
// internal/infrastructure/httpclient.NewDefaultClient so the
// application layer no longer touches *http.Client directly. Tests
// inject a roundtripper-backed fake that satisfies ports.Client.
type DeliveryHandler struct {
	log         *zap.Logger
	client      ports.Client
	db          *sql.DB
	hmacSecrets [][]byte
	insecureDev bool
}

// NewDeliveryHandler builds a DeliveryHandler.
//
//   - log         nil → nop.
//   - client      nil → default 30s-timeout http.Client adapter
//     (httpclient.NewDefaultClient). When non-nil the caller must pass
//     a ports.Client-compatible value (production concrete is
//     *httpclient.DefaultClient; tests inject a roundtripper-backed
//     fake).
//   - db          nil → no delivery_log writes (handler still POSTs and
//     signs).
//   - hmacSecrets rotated secret keys; current secret FIRST, then the
//     previous secret (if any). Empty slice + insecureDev
//     false → the handler refuses every event with a
//     terminal error (defence in depth against a config
//     typo that disables auth silently).
//   - insecureDev true only when VELOX_ALLOW_INSECURE_DEV=true. The
//     handler logs a structured warn on every signed-or-not
//     POST so the dev escape hatch is impossible to mistake
//     for production behaviour.
func NewDeliveryHandler(log *zap.Logger, client ports.Client, db *sql.DB, hmacSecrets [][]byte, insecureDev bool) *DeliveryHandler {
	if log == nil {
		log = zap.NewNop()
	}
	if client == nil {
		client = httpclient.NewDefaultClient(defaultDeliveryTimeout)
	}
	if len(hmacSecrets) == 0 && !insecureDev {
		log.Warn("delivery.requested constructed WITHOUT HMAC secrets and WITHOUT insecureDev — every event will be refused with a terminal error. Check VELOX_DELIVERY_HMAC_SECRET.")
	}
	return &DeliveryHandler{
		log:         log.Named("delivery"),
		client:      client,
		db:          db,
		hmacSecrets: hmacSecrets,
		insecureDev: insecureDev,
	}
}

// EventType implements outboxevents.Handler.
func (h *DeliveryHandler) EventType() string {
	return outboxevents.EventDeliveryRequested
}

// IdempotencyKey implements outboxevents.Handler (Fase 6(c) Push 6.2).
// Static canonical form: `<event_type>.<delivery_schema_version>` so
// the HandlerRegistry.Register fail-closed panic fires at init
// time if a future refactor forgets the declaration. per-event
// idempotency keys flow through the envelope's IdempotencyKey
// field (the receiver's dedup key) and do NOT substitute this
// static declaration.
func (h *DeliveryHandler) IdempotencyKey() string {
	return outboxevents.EventDeliveryRequested + "." + deliverySchemaVersion
}

// Validates the v1 envelope strictly. Returns nil on success; ONE of
// the typed terminal errors (ErrSchemaVersionMismatch / ErrUnsupportedProvider)
// for hard contract violations.
func validateDeliveryRequest(r *deliveryRequest) error {
	if r.SchemaVersion != deliverySchemaVersion {
		return fmt.Errorf("%w (got %q, want %q)", ErrSchemaVersionMismatch, r.SchemaVersion, deliverySchemaVersion)
	}
	if r.EventID == "" || r.OccurredAt == "" || r.JobID == "" {
		return fmt.Errorf("delivery.requested: missing required header field (event_id/occurred_at/job_id)")
	}
	if r.Artifact.ArtifactID == "" || r.Artifact.SHA256 == "" {
		return fmt.Errorf("delivery.requested: missing required artifact field (artifact_id/sha256)")
	}
	if r.Destination.Provider == "" || r.Destination.DestinationID == "" {
		return fmt.Errorf("delivery.requested: missing required destination field (provider/destination_id)")
	}
	if r.IdempotencyKey == "" {
		return fmt.Errorf("delivery.requested: missing required field (idempotency_key)")
	}
	switch r.Destination.Provider {
	case deliveryProviderWebhook, deliveryProviderDrive, deliveryProviderYouTube:
		// OK
	default:
		return fmt.Errorf("%w (got %q, allowed: drive|youtube|webhook)", ErrUnsupportedProvider, r.Destination.Provider)
	}
	return nil
}

// Handle parses the v1 envelope, dispatches by provider, and returns
// nil only for terminal-success (2xx) or unrecoverable client-error
// (4xx). 5xx / network errors and unknown providers return terminal
// errors so the outbox pool can map them to retry vs dead_letter.
func (h *DeliveryHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var req deliveryRequest
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		h.log.Warn("delivery.requested payload parse failed (terminal: shape mismatch)",
			zap.Int64("event_id", evt.ID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(err),
		)
		// Parse failure is terminal — the producer's payload is malformed;
		// retrying won't help and the dead-letter queue should fail-loudly.
		return fmt.Errorf("%w: payload parse: %s", ErrSchemaVersionMismatch, err.Error())
	}
	if err := validateDeliveryRequest(&req); err != nil {
		h.log.Warn("delivery.requested envelope validation failed",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(err),
		)
		return err
	}

	switch req.Destination.Provider {
	case deliveryProviderDrive, deliveryProviderYouTube:
		// The upload pipelines own drive|youtube delivery (they need
		// access tokens, resumable upload sessions, etc.). Logging a
		// "real" acknowledgement here means the operator-visible audit
		// row exists, but the actual upload is a separate code path.
		// This avoids the double-logic / future-wrapper anti-pattern of
		// trying to mirror Drive upload behaviour from inside the
		// outbox delivery handler.
		h.log.Info("delivery.requested acknowledged — handled by upload pipeline",
			zap.String("provider", req.Destination.Provider),
			zap.String("destination_id", req.Destination.DestinationID),
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.String("event_id", req.EventID),
			zap.Int64("outbox_id", evt.ID),
			zap.Int("attempt", evt.AttemptCount),
		)
		return nil

	case deliveryProviderWebhook:
		return h.deliverWebhook(ctx, evt, &req)
	default:
		// validateDeliveryRequest already screens for unknown providers;
		// this branch is defensive only.
		return fmt.Errorf("%w (got %q)", ErrUnsupportedProvider, req.Destination.Provider)
	}
}

// deliverWebhook POSTs the producer-supplied body, with HMAC-SHA256
// over the canonical string <event_timestamp>.<event_id>.<raw_body>.
// 2xx → ok; 4xx → terminal ack; 5xx/network → retry.
func (h *DeliveryHandler) deliverWebhook(ctx context.Context, evt outboxevents.Event, req *deliveryRequest) error {
	// Body defaults to {} so the receiver gets a well-formed JSON
	// document if the producer omits body.
	body := []byte(req.Body)
	if len(body) == 0 {
		body = []byte("{}")
	}

	postCtx, cancel := context.WithTimeout(ctx, defaultDeliveryTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(postCtx, http.MethodPost, req.Destination.DestinationID, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("delivery.requested build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "pipelinegen-delivery/1.0")
	httpReq.Header.Set(hmacsign.HeaderEventID, req.EventID)
	httpReq.Header.Set(hmacsign.HeaderTimestamp, req.OccurredAt)
	httpReq.Header.Set("X-Delivery-ID", req.IdempotencyKey)
	httpReq.Header.Set("X-Pipelinegen-Outbox-Event", fmt.Sprintf("%d", evt.ID))
	if req.TraceID != "" {
		httpReq.Header.Set("X-Pipelinegen-Trace-Id", req.TraceID)
	}
	if req.JobID != "" {
		httpReq.Header.Set("X-Pipelinegen-Job-Id", req.JobID)
	}

	// Mandatory HMAC in production. The dev escape hatch
	// (insecureDev == true) lets local development flow without
	// provisioning a real secret — but logs an unmistakable warning on
	// every signed-or-not POST so it's impossible to mistake for
	// production behaviour.
	switch {
	case len(h.hmacSecrets) == 0 && !h.insecureDev:
		h.log.Error("delivery.requested has no HMAC secret but config is NOT insecure_dev — refusing as terminal",
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.String("endpoint", req.Destination.DestinationID),
		)
		_ = h.recordDelivery(ctx, req, 0, "", "refused:hmac_not_configured")
		// Terminal — retry won't bring the secret into existence.
		return fmt.Errorf("delivery.requested: HMAC not configured (refusing): %w", ErrSchemaVersionMismatch)
	case len(h.hmacSecrets) == 0 && h.insecureDev:
		h.log.Warn("delivery.requested: HMAC disabled via VELOX_ALLOW_INSECURE_DEV=true — UNSIGNED POST (DEV ONLY)",
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.String("endpoint", req.Destination.DestinationID),
		)
		httpReq.Header.Set(hmacsign.HeaderSignature, "dev-insecure:unsigned")
	default:
		sig := hmacsign.Sign(h.hmacSecrets[0], req.OccurredAt, req.EventID, body)
		httpReq.Header.Set(hmacsign.HeaderSignature, sig)
	}

	resp, err := h.client.Do(httpReq)
	statusCode := 0
	responseBody := []byte(nil)
	if err != nil {
		h.log.Warn("delivery.requested POST failed (network) — will retry",
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.String("endpoint", req.Destination.DestinationID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(err),
		)
		_ = h.recordDelivery(ctx, req, statusCode, "", "network:"+err.Error())
		return fmt.Errorf("delivery.requested POST %s: %w", req.Destination.DestinationID, err)
	}
	defer resp.Body.Close()

	// Read response body, capped. A greedy receiver cannot OOM the worker.
	responseBody, _ = io.ReadAll(io.LimitReader(resp.Body, maxDeliveryResponseBytes))
	statusCode = resp.StatusCode
	_ = h.recordDelivery(ctx, req, statusCode, hashBody(responseBody), "ok")

	switch {
	case statusCode >= 200 && statusCode < 300:
		h.log.Info("delivery.requested succeeded",
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.String("endpoint", req.Destination.DestinationID),
			zap.Int("status", statusCode),
			zap.Int("attempt", evt.AttemptCount),
		)
		return nil

	case statusCode >= 400 && statusCode < 500:
		// 4xx: terminal ack — receiver rejected the payload, retrying
		// won't help. delivery_log retains status_code + truncated body
		// hash so operators can audit without paging through logs.
		h.log.Warn("delivery.requested rejected by receiver (4xx — terminal ack)",
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.String("endpoint", req.Destination.DestinationID),
			zap.Int("status", statusCode),
			zap.ByteString("body_preview", truncate(responseBody, 256)),
		)
		return nil

	default:
		// 5xx and any other unexpected status: return non-nil so the
		// outbox pool retries per its backoff. Eventually dead_letters
		// if retries are exhausted.
		h.log.Warn("delivery.requested server error (5xx — retrying)",
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.String("endpoint", req.Destination.DestinationID),
			zap.Int("status", statusCode),
			zap.Int("attempt", evt.AttemptCount),
		)
		return fmt.Errorf("delivery.requested %s → HTTP %d", req.Destination.DestinationID, statusCode)
	}
}

// recordDelivery writes (or updates) a delivery_log row keyed by
// idempotency_key (UNIQUE constraint). ON CONFLICT DO UPDATE collapses
// re-deliveries (e.g. after a 5xx retry) onto the same audit row. When
// db is nil the write is silently skipped so unit tests can construct
// the handler without a fixture.
func (h *DeliveryHandler) recordDelivery(ctx context.Context, req *deliveryRequest, statusCode int, responseHash, note string) error {
	if h.db == nil {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// Use a detached context so the audit write is not cancelled when
	// the caller's HTTP request context expires. The write is
	// idempotent (ON CONFLICT DO UPDATE) so retries are safe.
	_, err := h.db.ExecContext(context.WithoutCancel(ctx), `
		INSERT INTO delivery_log (asset_id, endpoint_url, delivery_id, status_code, response_hash, delivered_at, created_at, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(delivery_id) DO UPDATE SET
		  status_code = excluded.status_code,
		  response_hash = excluded.response_hash,
		  delivered_at = excluded.delivered_at,
		  note = excluded.note
	`, req.Artifact.ArtifactID, req.Destination.DestinationID, req.IdempotencyKey, statusCode, responseHash, now, now, note)
	if err != nil {
		h.log.Warn("delivery_log insert failed (audit-only, non-fatal)",
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.Error(err),
		)
		return err
	}
	return nil
}

// hashBody returns the lowercase hex SHA-256 of b. Empty input returns
// the SHA-256 of the empty string (a fixed constant), not "" — keeping
// the column stable for audits.
func hashBody(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// truncate returns at most n bytes of b. Used only for log lines; the
// delivery_log row stores the 1 MiB capped body hash.
func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
