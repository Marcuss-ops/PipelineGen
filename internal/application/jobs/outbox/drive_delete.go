// Package outbox — drive_delete.go (Blocco 3.1 commit 2/3, June 2026)
//
// DriveDeleteHandler is the application-layer consumer of
// asset.drive.delete_requested.v1 events. It is the FIRST side-effect
// hop of the deletion state machine:
//
//	ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING → DELETED
//
// Flow per event:
//
//   1. Parse + validate v1 envelope (TERMINAL on malformed JSON,
//      schema mismatch, missing fields).
//
//   2. Pre-flight read of the asset's current lifecycle_state.
//      - {INDEX_DELETE_PENDING, DELETED} → idempotent skip (early
//        return nil). The earlier hop already happened — this
//        handler is being re-invoked after a lease-fence or after
//        a previous worker crashed past the Drive side.
//      - {DELETE_REQUESTED, DELETE_PENDING, DRIVE_DELETE_PENDING} →
//        continue: the row is in a state DriveDeleteHandler is
//        authorised to advance.
//      - {} (asset row missing) → idempotent skip.
//      - other → terminal-error (avoid silent retried-handler
//        side-effects on rows that don't own this event).
//
//   3. Stamp lifecycle_state = DRIVE_DELETE_PENDING via
//      ClipsLifecycleStateWriter.SetLifecycleState — the BEFORE-Drive
//      operator-visibility stamp.
//
//   4. Resolve the Drive fileID from the asset's metadata
//      (drive_file_id, drive_link, download_link). Empty fileID is
//      handled by skipping the Drive side-effect entirely and
//      advancing the state machine directly to INDEX_DELETE_PENDING
//      (the row may have been ingested without a Drive upload;
//      orphan-Source assets still need the rest of the chain).
//
//   5. Call DriveDeleter.Trash(fileID) (or .Delete(fileID) when
//      Permanently=true). Re-delete (404) is treated as idempotent
//      success.
//
//   6. On success: AtomicFlip via StateAdvancer.AdvanceAndEmit:
//      lifecycle_state := INDEX_DELETE_PENDING AND emits
//      EventAssetIndexDeleteRequested in a single tx so a worker
//      crash mid-flow is recoverable (the next worker re-enqueues
//      the event; the state-machine layer absorbs the re-enqueue
//      as a no-op because the row is already in INDEX_DELETE_PENDING).
//
// Pattern 0 (AGENTS.md): the handler depends on narrow ports
// (DriveDeleter, StateAdvancer, LifecycleStateReader,
// ClipsLifecycleStateWriter) declared in ports.go. Production
// wiring satisfies each port with the canonical concrete:
//
//   - DriveDeleter       ← drive.FileLifecycle (concrete: *drive.FileLifecycleAdapter)
//   - StateAdvancer      ← *outbox.Dispatcher (AdvanceAndEmit)
//   - LifecycleStateReader / ClipsLifecycleStateWriter
//                        ← *assets.ClipsRepository
//
// Satisfies outboxevents.Handler — the production worker pool
// dispatches by EventType lookup.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/api/googleapi"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// DriveDeleteRequestSchemaVersion is the canonical, EXACT string the
// handler accepts. Producers MUST send "asset.drive.delete_requested.v1"
// literally. Mismatch is TERMINAL — no retry — so producers upgrade
// instead of silently retrying on what looks like a routine failure.
const DriveDeleteRequestSchemaVersion = "asset.drive.delete_requested.v1"

// DriveDeleteEventType is the outbox event-type constant that
// declares which event this handler consumes.
//
// NOTE: production wiring registers this handler against the
// outboxevents registry at composition time. The
// (DriveDeleteEventType, outboxevents.EventAssetDriveDeleteRequested)
// pair must stay in lockstep — see the compile-time assertion at the
// bottom of this file.
const DriveDeleteEventType = "asset.drive.delete_requested"

// driveDeleteRequestV1 is the canonical envelope for the
// producer/consumer pair (mirror of driveDeleteRequestV1 in
// drive_delete_envelope.go). Required fields (handler fails-fast
// on missing-or-malformed):
//   - schema_version   (literal DriveDeleteRequestSchemaVersion)
//   - event_id         (RFC4122 UUID or producer-chosen opaque token)
//   - asset_id         (canonical media_assets.id)
//   - idempotency_key  (operational audit + dedup at the outbox level)
//
// Optional:
//   - permanently      (true → Drive.Delete, false → Drive.Trash)
//   - requested_at     (RFC3339 UTC; logged for audit only).
type driveDeleteRequestV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	Permanently    bool   `json:"permanently,omitempty"`
	RequestedAt    string `json:"requested_at,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

// driveLifecycleTerminalErr is the typed-terminal error sentinel
// raised on envelope failures. The wrap path stamps every string
// with a recognisable prefix so the production pool's
// IsTerminal classifier (outboxevents.NewTerminalError consumer)
// dead-letters immediately rather than spinning through
// max_attempts. Every Handle-returned terminal error wraps this
// via outboxevents.NewTerminalError.
//
// Blocco 3.1 commit 2/3 invariant: any return path that detects
// a malformed envelope, schema mismatch, missing field, or
// lifecycle-state inconsistency MUST route through this sentinel
// + NewTerminalError wrap. Pragmatic envelope-validation loops
// (e.g. "if empty asset_id") belong here, NOT in Drive-side
// transient error returns.
var driveLifecycleTerminalErr = errors.New("drive_delete: terminal envelope error")

// terminalWrap is the helper that converts a typed-terminal error
// into the canonical outboxevents.NewTerminalError shape. Centralises
// the wrap path so swap-outs (e.g. a future in-package typed
// terminal error) don't have to find the wrap call at every
// Handle-return site.
func terminalWrap(err error) error {
	return outboxevents.NewTerminalError(err)
}

// driveLinkFileIDPattern extracts the Drive fileID from a Drive
// link of the form https://drive.google.com/file/d/<id>/view or
// https://docs.google.com/.../d/<id>/... .
var driveLinkFileIDPattern = regexp.MustCompile(`/d/([A-Za-z0-9_-]+)`)

// extractDriveFileID resolves the canonical Drive fileID from an
// asset row's metadata. Returns empty string when no fileID can be
// resolved; the caller skips Drive and advances the state machine
// directly to INDEX_DELETE_PENDING.
func extractDriveFileID(clip *asset.Asset) string {
	if clip == nil {
		return ""
	}
	if id := clip.DriveFileID(); id != "" {
		return id
	}
	for _, link := range []string{clip.DriveLink(), clip.DownloadLink()} {
		if link == "" {
			continue
		}
		m := driveLinkFileIDPattern.FindStringSubmatch(link)
		if len(m) >= 2 && m[1] != "" {
			return m[1]
		}
	}
	return ""
}

// DriveDeleteHandler is the application-layer implementation of the
// asset.drive.delete_requested.v1 consumer. Mirrors IndexDeleteHandler
// in structure: EventType() returns the canonical event-type string,
// Handle(ctx, evt outboxevents.Event) error is the pool-driven entry.
type DriveDeleteHandler struct {
	log         *zap.Logger
	drive       DriveDeleter
	stateReader LifecycleStateReader
	stateWriter ClipsLifecycleStateWriter
	advancer    StateAdvancer
}

// NewDriveDeleteHandler wires the producer-side dependencies. log
// nil → nop logger. Other deps nil → caller has a programming
// error; the handler guards each call site so partial wiring
// degrades to a typed error rather than a runtime panic.
//
// Production wiring satisfies each port:
//   - drive       ← drive.FileLifecycle (concrete: *drive.FileLifecycleAdapter)
//   - stateReader + stateWriter ← *assets.ClipsRepository
//   - advancer    ← *outbox.Dispatcher (AdvanceAndEmit)
func NewDriveDeleteHandler(
	log *zap.Logger,
	drive DriveDeleter,
	stateReader LifecycleStateReader,
	stateWriter ClipsLifecycleStateWriter,
	advancer StateAdvancer,
) *DriveDeleteHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &DriveDeleteHandler{
		log:         log.Named("drive_delete"),
		drive:       drive,
		stateReader: stateReader,
		stateWriter: stateWriter,
		advancer:    advancer,
	}
}

// EventType returns the canonical outbox event-type string for
// DriveDeleteHandler.
func (h *DriveDeleteHandler) EventType() string {
	return DriveDeleteEventType
}

// Handle parses the v1 envelope, performs the BEFORE-Drive stamp +
// the Drive API call + the AFTER-Drive advance-and-emit. Returns
// nil on success (including idempotent skip). Returns a typed
// error wrapped with driveLifecycleTerminalErr on invalid envelopes
// (the production pool's IsTerminal classifier will dead-letter
// these after Blocco 3.1 commit 3+ lands the NewTerminalError path).
// Returns a non-terminal error on transient Drive failures so the
// outbox pool retries with exponential backoff.
func (h *DriveDeleteHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	log := h.log
	if log == nil {
		log = zap.NewNop()
	}

	// 1. Parse v1 envelope.
	var req driveDeleteRequestV1
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		log.Warn("drive_delete payload parse failed",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Error(err),
		)
		return terminalWrap(fmt.Errorf("%w: %v", driveLifecycleTerminalErr, err))
	}

	// 2. Strict envelope validation.
	if req.SchemaVersion != DriveDeleteRequestSchemaVersion {
		log.Warn("drive_delete schema_version mismatch",
			zap.Int64("event_id", evt.ID),
			zap.String("got_version", req.SchemaVersion),
			zap.String("want_version", DriveDeleteRequestSchemaVersion),
		)
		return terminalWrap(fmt.Errorf("%w: schema_version %q != %q", driveLifecycleTerminalErr,
			req.SchemaVersion, DriveDeleteRequestSchemaVersion))
	}
	if req.AssetID == "" {
		log.Warn("drive_delete: empty asset_id",
			zap.Int64("event_id", evt.ID),
		)
		return terminalWrap(fmt.Errorf("%w: asset_id is required", driveLifecycleTerminalErr))
	}
	if req.IdempotencyKey == "" {
		log.Warn("drive_delete: missing idempotency_key",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", req.AssetID),
		)
		return terminalWrap(fmt.Errorf("%w: idempotency_key is required", driveLifecycleTerminalErr))
	}

	reqLog := []zap.Field{
		zap.String("asset_id", req.AssetID),
		zap.Int64("event_id", evt.ID),
		zap.String("outbox_event_id", req.EventID),
		zap.String("idempotency_key", req.IdempotencyKey),
		zap.Int("attempt", evt.AttemptCount),
		zap.Bool("permanently", req.Permanently),
	}

	// 3. Pre-flight: read current lifecycle_state.
	if h.stateReader == nil {
		return errors.New("drive_delete: stateReader not wired (production wiring must supply *assets.ClipsRepository)")
	}
	clip, err := h.stateReader.GetClip(ctx, req.AssetID)
	if err != nil {
		log.Warn("drive_delete: pre-flight GetClip failed (retryable)",
			append(reqLog, zap.Error(err))...,
		)
		return fmt.Errorf("drive_delete GetClip(%s): %w", req.AssetID, err)
	}
	if clip == nil {
		log.Info("drive_delete: asset row absent — idempotent skip", reqLog...)
		return nil
	}
	switch string(clip.LifecycleState) {
	case string(asset.StateLifecycleIndexDeletePending), string(asset.StateDeleted), "deleted":
		log.Info("drive_delete: already past Drive hop — idempotent skip",
			append(reqLog, zap.String("lifecycle_state", string(clip.LifecycleState)))...,
		)
		return nil
	case string(asset.StateDeleteRequested),
		string(asset.StateDeletePending),
		string(asset.StateDriveDeletePending):
		// authorised to advance
	default:
		log.Warn("drive_delete: asset in unexpected lifecycle_state — terminal",
			append(reqLog, zap.String("lifecycle_state", string(clip.LifecycleState)))...,
		)
		return terminalWrap(fmt.Errorf("%w: unexpected lifecycle_state %q for %s",
			driveLifecycleTerminalErr, clip.LifecycleState, req.AssetID))
	}

	// 4. BEFORE-Drive stamp.
	log.Info("drive_delete: stamping DRIVE_DELETE_PENDING", reqLog...)
	if err := h.stateWriter.SetLifecycleState(ctx, req.AssetID, asset.StateDriveDeletePending); err != nil {
		log.Warn("drive_delete: SetLifecycleState(DRIVE_DELETE_PENDING) failed (retryable)",
			append(reqLog, zap.Error(err))...,
		)
		return fmt.Errorf("drive_delete SetLifecycleState(DRIVE_DELETE_PENDING, %s): %w", req.AssetID, err)
	}

	// 5. Drive API call.
	fileID := extractDriveFileID(clip)
	if h.drive == nil {
		return errors.New("drive_delete: drive port not wired (production wiring must supply DriveDeleter)")
	}
	if fileID == "" {
		log.Info("drive_delete: no Drive fileID — skipping Drive side-effect, advancing directly", reqLog...)
	} else {
		log.Info("drive_delete: invoking Drive API",
			append(reqLog, zap.String("file_id", fileID))...)
		var driveErr error
		if req.Permanently {
			driveErr = h.drive.Delete(ctx, fileID)
			if driveErr != nil && isDriveNotFoundError(driveErr) {
				log.Info("drive_delete: Drive.Delete returned 404, treating as idempotent success",
					append(reqLog, zap.String("file_id", fileID))...)
				driveErr = nil
			}
		} else {
			driveErr = h.drive.Trash(ctx, fileID)
			if driveErr != nil && isDriveNotFoundError(driveErr) {
				// Trash is normally idempotent at the Drive API
				// level, but the rare Trash→404 (file permanently
				// deleted between Trash attempts) ALSO folds to
				// success — the Blocco 3.1 semantic "the file is
				// already gone" is terminal regardless of intent.
				log.Info("drive_delete: Drive.Trash returned 404, treating as idempotent success",
					append(reqLog, zap.String("file_id", fileID))...)
				driveErr = nil
			}
		}
		if driveErr != nil {
			log.Warn("drive_delete: Drive API failed (retryable — row stays in DRIVE_DELETE_PENDING)",
				append(reqLog, zap.Error(driveErr))...,
			)
			return fmt.Errorf("drive_delete Drive API for %s: %w", req.AssetID, driveErr)
		}
	}

	// 6. Atomic advance + emit next event.
	nextPayload, err := buildIndexDeletePayloadForDrive(req.AssetID)
	if err != nil {
		return fmt.Errorf("drive_delete build index-delete payload: %w", err)
	}
	nextEventKey := "delete:" + req.AssetID
	log.Info("drive_delete: advancing to INDEX_DELETE_PENDING + emitting index.delete_requested event",
		append(reqLog, zap.String("next_event_type", outboxevents.EventAssetIndexDeleteRequested))...)
	if err := h.advancer.AdvanceAndEmit(
		ctx,
		req.AssetID,
		asset.StateDriveDeletePending,
		asset.StateLifecycleIndexDeletePending,
		outboxevents.EventAssetIndexDeleteRequested,
		nextPayload,
		nextEventKey,
	); err != nil {
		log.Warn("drive_delete: AdvanceAndEmit failed (retryable)",
			append(reqLog, zap.Error(err))...,
		)
		return fmt.Errorf("drive_delete AdvanceAndEmit(%s): %w", req.AssetID, err)
	}

	log.Info("drive_delete: deletion complete", reqLog...)
	return nil
}

// indexDeletePayloadV1 is the consumer-mirror struct used by
// DriveDeleteHandler to emit the next-hop EVENT event. The
// canonical producer-side declaration lives in
// internal/infrastructure/database/sqlite/outbox/delete_envelope.go
// as `deleteRequestV1`, but Go's package-identity rules make it
// unreachable from this application-layer handler file (cross-
// directory, even with the shared lowercase segment in the path).
// Re-declaring the struct here is the canonical Pattern-0 fix:
// application-layer producers are NOT allowed to import from
// internal/infrastructure/* (godlike/06 "one owner per fact" +
// AGENTS.md Pattern 0).
//
// Required fields (consumer is fail-fast on missing-or-malformed):
//   - schema_version   (literal DeleteRequestSchemaVersion)
//   - event_id         (RFC4122 UUID)
//   - asset_id         (canonical media_assets.id)
//   - idempotency_key  (operational audit + dedup at outbox level)
//
// Optional: requested_at (RFC3339 UTC; logged for audit only).
type indexDeletePayloadV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	RequestedAt    string `json:"requested_at,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

// buildIndexDeletePayloadForDrive constructs the canonical
// producer-side payload for EventAssetIndexDeleteRequested. The
// outbox pool picks up this event and runs IndexDeleteHandler.Handle
// to complete the chain with Qdrant DeletePoints + SQLite
// SoftDelete + lifecycle_state = DELETED.
func buildIndexDeletePayloadForDrive(assetID string) ([]byte, error) {
	payload := indexDeletePayloadV1{
		SchemaVersion:  DeleteRequestSchemaVersion,
		EventID:        uuid.NewString(),
		AssetID:        assetID,
		RequestedAt:    timeutil.FormatRFC3339(time.Now()),
		IdempotencyKey: "delete:" + assetID,
	}
	return json.Marshal(payload)
}

// isDriveNotFoundError returns true when err wraps a *googleapi.Error
// with Code == http.StatusNotFound. DriveDeleteHandler uses this to
// fold the NOT-FOUND case into idempotent success: a Drive.Delete
// (or, defensively, Drive.Trash) on an already-deleted fileID
// returns 404, and the handler treats that as the desired terminal
// state rather than a transient retry.
//
// Why errors.As + Code, not substring matching: a substring matcher
// would fold any error message containing "404" / "not found"
// into success (false positive when a non-Drive error happens to
// contain those). errors.As + ge.Code == 404 is the canonical
// google.golang.org/api/googleapi error-classification idiom.
//
// Both Trash AND Delete are folded when the API returns 404 — the
// handler applies the fold conditionally on Permanently to keep
// the intent visible, but a Trash→404 (file permanently deleted
// between Trash attempts) ALSO deserves the same idempotent
// treatment per the Blocco 3.1 state machine semantic ("the file
// is already gone" is the same terminal regardless of intent).
func isDriveNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		return false
	}
	return ge.Code == http.StatusNotFound
}

// Compile-time assertion: *DriveDeleteHandler must implement
// outboxevents.Handler. Drift in any of the method signatures
// becomes a build failure here rather than a runtime panic at
// first dispatch.
var _ outboxevents.Handler = (*DriveDeleteHandler)(nil)

// Compile-time assertion that the production constants stay in
// lockstep with outboxevents.EventAssetDriveDeleteRequested —
// a future drift is a build failure.
var _ = outboxevents.EventAssetDriveDeleteRequested == DriveDeleteEventType
