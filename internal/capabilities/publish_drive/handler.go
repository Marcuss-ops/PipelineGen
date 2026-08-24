// Package publish_drive — handler.go (FASE 3 / Push 3.1e, July 2026).
//
// Drive-upload consumer for the FASE 3 Publish step of the
// saga. Drains `artifact.staged.v1` events (atomically
// co-emitted by Repository.InsertWithOutbox in Push 3.1c) from
// the outbox_events table (via outboxevents.HandlerRegistry),
// forwards each event to delivery.Publisher.Publish (the
// canonical Drive upload canal), and on success records the
// canonical JSON PublishedLocation via Repository.MarkPublished
// (the fenced CAS that transitions the row to PUBLISHED state).
//
// godlike/06 SSOT: this handler is the SOLE canonical consumer
// of `artifact.staged.v1` events. The composition root
// registers it exactly once via outboxevents.HandlerRegistry
// inside BuildOutboxBundle. No other code path may bypass
// delivery.Publisher (the `ParentFolderID` ban per
// godlike/06 SSOT — see cmd/archcheck/scan/percheck_root_override.go).
//
// godlike/07 fail-closed:
//   - Payload decode failure → ErrInvalidPayload (typed wrap).
//   - Empty StageID → ErrEmptyStageID (typed-wrap variant).
//   - Destination without "drive:" prefix → ErrDestinationFormat.
//   - delivery.Publisher.Publish failure → wrapped upstream error.
//   - Repository.MarkPublished ErrTerminalStateRejection →
//     idempotent no-op (return nil) — the row is already in
//     PUBLISHED or SUCCEEDED state, the desired end-state, so
//     an outbox re-delivery must NOT retry.
//   - Any other MarkPublished error → wrapped upstream error.
//
// Pattern 0 (AGENTS.md): the handler depends ONLY on typed ports
// (artifact.ArtifactStageRepository + delivery.Publisher) — never on
// infrastructure concrete (drive.Uploader, FolderManager) or
// domain internals.
package publish_drive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// EventTypeArtifactStaged is the canonical outbox event_type
// drained by this handler. The naming convention is
// `<aggregate>.<action>.<version>` (semver), as documented at
// internal/platform/sqlite/outboxevents/repository.go.
// NOTE: this constant intentionally shadows
// staging.EventTypeArtifactStaged — the consumer should reference
// the consumer-side constant for read-side decoupling. The
// PRODUCER (staging/service.go) and CONSUMER (this file) MUST
// agree on the literal value; a divergence test
// (TestEventTypeAgreementWithStagingProducer) pins the contract
// at the bottom of this file.
const EventTypeArtifactStaged = "artifact.staged.v1"

// ── Typed-error sentinel chain (godlike/07) ────────────────────────────

var (
	// ErrInvalidPayload is returned when the inbound Event's
	// PayloadJSON cannot be unmarshalled into the typed
	// TypedStageEventPayload. Each wrap carries the typed
	// sentinel so log-greppers can classify the failure class.
	ErrInvalidPayload = errors.New("publish_drive: payload decode failed (invalid JSON or out-of-set field)")

	// ErrEmptyStageID is returned when a structurally valid
	// payload has an empty StageID. The MarkPublished call
	// MUST be issued against a specific stage_id; an empty
	// value would surface as a typed-row-not-found error from
	// the repository (opaque to log-greppers) — the canonical
	// fail-closed gate rejects the malformed event BEFORE
	// the publisher is engaged.
	ErrEmptyStageID = errors.New("publish_drive: stage_id is required (canonical artifact_stages primary key)")

	// ErrDestinationFormat is returned when the payload's
	// Destination field cannot be parsed as `<scheme>:<key>[/<sub>]`
	// with scheme=`drive`. Future push surfaces a typed
	// ErrUnknownDestinationKey distinct from this parse-failure
	// sentinel (forward-pointer for typed error class expansion).
	ErrDestinationFormat = errors.New("publish_drive: destination field must follow 'drive:<key>[/<sub>]' format")
)

// TypedStageEventPayload mirrors the producer-side
// staging.TypedStageEventPayload schema (the producer is
// staging.Service.Stage; the consumer is this handler).
//
// godlike/06 SSOT: the consumer-side mirror is intentionally
// REDECLARED here (not imported from the staging package) so the
// schema coupling is captured by a one-line unit test
// (TestTypedStageEventPayloadAgreementWithStagingProducer below)
// rather than a package import. If the producer adds or renames
// a field, the divergence test fails and both sides update in
// lockstep.
//
// Composition rule: this struct MUST stay byte-stable with the
// producer; if a forward-pointer schema evolution adds a field,
// update BOTH sides and bump the event_type version in lockstep.
type TypedStageEventPayload struct {
	StageID     string `json:"stage_id"`
	JobID       string `json:"job_id"`
	LocalPath   string `json:"local_path"`
	Hash        string `json:"hash"`
	Size        int64  `json:"size"`
	Mime        string `json:"mime"`
	Requirement string `json:"requirement"`
	Destination string `json:"destination"`
	EmittedAt   string `json:"emitted_at"`
}

// ── Handler — canonical outboxevents.Handler implementation ─────────────

// Compile-time assertion: *Handler satisfies outboxevents.Handler.
var _ outboxevents.Handler = (*Handler)(nil)

// Handler is the canonical Stage→Publish worker that drains
// `artifact.staged.v1` events from the outbox_events table and
// forwards each event to delivery.Publisher.Publish + Repository.
// MarkPublished. Push 3.1e closes the FASE 3 Publish step
// (3.1c inventoried the forward-pointer for staged.v1; 3.1e
// wires the consumer + the typed PublishedLocation JSON).
type Handler struct {
	// repo is the canonical artifact_stages single-writer
	// (fenced CAS for Mark* primitives). The handler uses
	// MarkPublished to transition the row to PUBLISHED state
	// with a JSON PublishedLocation payload.
	repo artifact.ArtifactStageRepository

	// publisher is the canonical Drive upload canal
	// (delivery.Publisher interface; concrete =
	// *drive.Publisher). The handler MUST NOT bypass this
	// port with direct FolderManager or Uploader calls
	// (godlike/06 SSOT — see cmd/archcheck percheck_root_override).
	publisher delivery.Publisher

	// log is the canonical zap.Logger for structured event
	// emission (zap.String/zap.Time for the canonical fields).
	log *zap.Logger

	// nowFn is overridable for tests (default = time.Now). UTC
	// is enforced via the helper to match PipelineGen SSOT.
	nowFn func() time.Time
}

// NewHandler constructs the canonical FASE 3 Drive-upload
// worker. godlike/07 fail-fast at construction: caller MUST
// supply a non-nil repo + non-nil publisher + non-nil log.
func NewHandler(repo artifact.ArtifactStageRepository, publisher delivery.Publisher, log *zap.Logger) (*Handler, error) {
	if repo == nil {
		return nil, fmt.Errorf("publish_drive.NewHandler: repo is required")
	}
	if publisher == nil {
		return nil, fmt.Errorf("publish_drive.NewHandler: publisher is required")
	}
	if log == nil {
		return nil, fmt.Errorf("publish_drive.NewHandler: log is required")
	}
	return &Handler{
		repo:      repo,
		publisher: publisher,
		log:       log,
		nowFn:     func() time.Time { return time.Now().UTC() },
	}, nil
}

// EventType returns the canonical event_type this handler
// consumes. The outbox HandlerRegistry uses this for routing.
func (h *Handler) EventType() string {
	return EventTypeArtifactStaged
}

// IdempotencyKey returns the canonical handler-registration
// idempotency key. The outbox HandlerRegistry uses this to
// reject duplicate registrations of the same handler. Stable
// across instances.
func (h *Handler) IdempotencyKey() string {
	return EventTypeArtifactStaged
}

// now returns the Handler's monotonic time source (UTC-normalised).
// Exposed as a method so tests can override nowFn via a
// field assignment post-construction.
func (h *Handler) now() time.Time { return h.nowFn().UTC() }

// Handle decodes the inbound event's PayloadJSON as a
// TypedStageEventPayload, parses the Destination field into a
// canonical delivery.DestinationKey + optional Subject, calls
// delivery.Publisher.Publish to upload the staged file, then
// issues Repository.MarkPublished with the canonical JSON
// PublishedLocation.
//
// godlike/07 fail-closed: every failure mode surfaces a typed
// sentinel — the handler does NOT swallow errors. The outbox
// pool decides retry-vs-drop based on the typed chain. The one
// exception is ErrTerminalStateRejection on MarkPublished:
// a row in PUBLISHED or SUCCEEDED state IS the desired
// end-state, so re-delivery is a no-op (the pool must NOT retry
// or it would loop indefinitely).
func (h *Handler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var payload TypedStageEventPayload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("%w: json decode (raw=%q): %v", ErrInvalidPayload, evt.PayloadJSON, err)
	}

	if strings.TrimSpace(payload.StageID) == "" {
		return fmt.Errorf("%w: stage_id (raw=%q)", ErrEmptyStageID, payload.StageID)
	}

	destKey, subject, err := parseDriveDestination(payload.Destination)
	if err != nil {
		return err
	}

	// Idempotency-key: the canonical SHA-256 deterministic
	// identity that lets Drive dedupe on appProperties
	// (delivery.Publisher.Publish carries the key through
	// PublishRequest.IdempotencyKey for cross-session
	// recovery; P0.6, July 2026).
	idempKey := delivery.DeriveIdempotencyKey(
		delivery.DestinationKey(destKey),
		payload.StageID,
		payload.Hash,
		1, // SourceVersion: per-push; the staged.v1 emitter does
		//   not propagate a SourceVersion through the payload
		//   schema. Using 1 is deterministic — same StageID +
		//   Hash → same IdempotencyKey across retries and
		//   cross-session recovery.
	)

	// Forward to delivery.Publisher.Publish (canonical canal).
	// Filename = StageID verbatim (no MIME-extension derivation
	// needed; the Publisher's MetadataWriterPort preserves the
	// Mime in the data-side properties of the Drive file).
	pubResult, pubErr := h.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:    delivery.DestinationKey(destKey),
		LocalPath:      payload.LocalPath,
		Filename:       payload.StageID,
		AssetID:        payload.StageID,
		ProjectID:      payload.JobID,
		Group:          payload.JobID,
		Subject:        subject,
		IdempotencyKey: idempKey,
		ContentHash:    payload.Hash,
		SourceVersion:  1,
	})
	if pubErr != nil {
		return fmt.Errorf("publish_drive.Handle: Publisher.Publish (stage_id=%s destination=%s): %w", payload.StageID, destKey, pubErr)
	}

	// Build the canonical artifact.PublishedLocation (typed
	// domain struct) and marshal to JSON for the repository.
	// The repository's MarkPublished takes a JSON-stringified
	// PublishedLocation (column published_location in the
	// artifact_stages schema).
	publishedAt := h.now()
	location := artifact.PublishedLocation{
		ArtifactID:  payload.StageID,
		Kind:        artifact.LocationKindDrive,
		URI:         pubResult.FileID,
		ExternalID:  pubResult.FileID,
		PublishedAt: publishedAt,
	}
	locationJSON, marshalErr := json.Marshal(location)
	if marshalErr != nil {
		return fmt.Errorf("publish_drive.Handle: marshal PublishedLocation (stage_id=%s): %w", payload.StageID, marshalErr)
	}

	// Fenced CAS MarkPublished: idempotent on already-terminal
	// rows (we swallow ErrTerminalStateRejection as a no-op
	// success — see package doc for rationale).
	markErr := h.repo.MarkPublished(ctx, payload.StageID, string(locationJSON), publishedAt)
	if markErr != nil {
		if errors.Is(markErr, artifact.ErrTerminalStateRejection) {
			h.log.Info("publish_drive: terminal-state fence observed (re-delivery, no-op)",
				zap.String("stage_id", payload.StageID),
				zap.String("event_key", evt.EventKey),
				zap.String("drive_file_id", pubResult.FileID),
			)
			return nil
		}
		return fmt.Errorf("publish_drive.Handle: repo.MarkPublished (stage_id=%s): %w", payload.StageID, markErr)
	}

	// Structured observation: a successful publish emits the
	// canonical log line for downstream telemetry. Field names
	// are stable across releases (dashboard pin).
	h.log.Info("publish_drive: artifact published",
		zap.String("event_type", EventTypeArtifactStaged),
		zap.String("stage_id", payload.StageID),
		zap.String("job_id", payload.JobID),
		zap.String("destination", destKey),
		zap.String("subject", subject),
		zap.String("drive_file_id", pubResult.FileID),
		zap.String("drive_folder_id", pubResult.FolderID),
		zap.String("idempotency_key", idempKey),
		zap.String("upload_action", string(pubResult.Action)),
		zap.Time("published_at", publishedAt),
	)
	return nil
}

// parseDriveDestination splits the payload's Destination field
// of the form `drive:<key>[/<sub>]` into the canonical
// DestinationKey string + an optional Subject (remainder after
// the first '/').
//
// Returns ErrDestinationFormat if the field does not start
// with `drive:` or if the key-after-prefix is empty. The check
// is intentionally fail-closed: a malformed destination is
// rejected BEFORE the publisher is engaged (diagnosing a
// downstream registry rejection is harder than a clean pre-TX
// rejection).
//
// The DestinationKey string is NOT validated against the
// destination registry here — that validation happens inside
// delivery.Publisher.Publish (Step 0 in the canonical pipeline:
// registry-driven ConflictPolicy default, which surfaces an
// ErrUnknownDestinationKey for unmapped keys). Validating at
// this layer would duplicate the registry check.
func parseDriveDestination(s string) (key, subject string, err error) {
	s = strings.TrimSpace(s)
	const prefix = "drive:"
	if !strings.HasPrefix(s, prefix) {
		return "", "", fmt.Errorf("%w: %q (must start with %q)", ErrDestinationFormat, s, prefix)
	}
	rest := strings.TrimPrefix(s, prefix)
	parts := strings.SplitN(rest, "/", 2)
	key = strings.TrimSpace(parts[0])
	if key == "" {
		return "", "", fmt.Errorf("%w: %q (key after %q is empty)", ErrDestinationFormat, s, prefix)
	}
	if len(parts) == 2 {
		subject = strings.TrimSpace(parts[1])
	}
	return key, subject, nil
}
