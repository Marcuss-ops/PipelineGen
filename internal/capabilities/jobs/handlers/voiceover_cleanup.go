// Package outbox — voiceover_cleanup.go carries the consumer handler
// for `voiceover.cleanup.requested` events (P0.7 Wave 21 Step 10/12,
// June 2026).
//
// The handler is the durable replacement for the pre-fix
// `Service.cleanupOrphanVoiceover(driveFileID, oldLocalPath,
// oldCleanedPath)` fire-and-forget goroutine that detached via
// context.Background() right after the finalize commit. The
// background goroutine contract had two failure modes that the durable
// event eliminates:
//
//   - Lost on handler cancel: a client disconnect between Commit and
//     the goroutine pickup window dropped the cleanup silently
//     (orphan Drive file + orphan local audio files).
//   - Lost on server restart: a stop/restart cycle between Commit
//     and the goroutine pickup window dropped the cleanup forever
//     (no visible regression-test signal, no audit row).
//
// The producer (voiceover.finalizeStage, Step 10/12) now enqueues this
// event INSIDE the same SQL tx as the voiceovers UPSERT + media_assets
// projection UPSERT + asset.index.requested outbox, so a tx rollback
// discards the entire finalize surface (no half-state orphans); a tx
// commit durably records the cleanup intent. The outbox pool retries
// transient failures per its exponential backoff.
//
// Handler contract:
//
//  1. Parse v1 envelope (terminal on parse error).
//  2. Validate required fields (terminal on missing/mismatched).
//  3. Idempotency gate: old == new or both empty → no-op success.
//  4. Drive delete: only when old != new AND old != "" — retryable on
//     transient Drive failures (HTTP 502/503/504, timeouts); the
//     pool's exponential backoff retries. Terminal schema errors
//     dead-letter.
//  5. Local file remove: os.Remove per path; os.IsNotExist swallowed
//     for idempotency. Retryable on transient filesystem failures
//     (locked file, EIO, fsync timeout).
//
// The handler does NOT touch SQLite or pgvector — post-commit cleanup
// for orphan side-effects ONLY. The canonical voiceovers/media_assets
// row already reflects the post-swap state at this point.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/api/googleapi"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// VoiceoverCleanupSchemaVersion is the canonical, EXACT string the
// VoiceoverCleanupHandler accepts. Producers MUST send
// "voiceover.cleanup.requested.v1" literally. Mismatch is TERMINAL
// (godlike/07 — no fake availability; producers upgrade rather than
// retrying into a repair loop).
const VoiceoverCleanupSchemaVersion = event.VoiceoverCleanupRequestedV1Schema

// VoiceoverCleanupDriver is the narrow Drive surface the
// VoiceoverCleanupHandler uses for orphan file deletion. Declared
// locally so the handler does NOT import internal/capabilities/voiceover
// directly (per AGENTS.md Pattern 0 — port abstraction layer, June
// 2026): sibling application-layer packages communicate through
// narrow ports, not cross-package concrete sharing.
//
// Production concrete: drive.Admin from the composition root (it
// satisfies VoiceoverCleanupDriver.DeleteFile structurally — the bare
// DeleteFile method here has the exact same signature, so Go's
// implicit-interface rule pins conformance at wire time).
// DeleteFile method here is the same signature, so Go's
// implicit-interface rule pins conformance at wire time).
//
// Nil-safe: handler guards nil via the `driver != nil` branch so
// partial wiring degrades to "skip Drive delete, log+drop local
// files" rather than crashing. Production wiring in
// BuildOutboxBundle always supplies a non-nil adapter when
// cfg.Drive is enabled.
type VoiceoverCleanupDriver interface {
	DeleteFile(ctx context.Context, fileID string) error
}

// voiceoverCleanupRequestV1 is the canonical envelope the
// VoiceoverCleanupHandler consumes.
//
// Required fields (handler fails-fast with TerminalError on
// missing-or-malformed):
//
//   - schema_version    (literal VoiceoverCleanupSchemaVersion)
//   - event_id          (RFC4122 UUID or producer-chosen opaque token)
//   - voiceover_id      (canonical voiceovers.id — shared with
//     media_assets.id primary key)
//
// Conditional fields:
//   - old_drive_file_id + new_drive_file_id: when old is non-empty
//     and differs from new, the handler schedules a Drive delete on
//     old_drive_file_id. For a post-publish orphan, old is the orphan
//     and new is empty; this is intentionally distinct from a normal
//     first-time finalize, which never emits cleanup with old set.
//     Equal values → no-op (the swap landed on the same Drive file;
//     there is no orphan). Both empty → no-op (no prior row existed).
//   - old_local_paths: list of OLD audio paths (LocalPath +
//     CleanedPath). The handler attempts os.Remove on each;
//     os.IsNotExist is silently swallowed for idempotency; the
//     pool's retry path re-fires on transient filesystem errors.
//
// Optional:
//   - idempotency_key  (mirrors event_key for audit; empty is terminal)
//   - requested_at     (RFC3339 UTC; logged for audit only)
//
// Producers MUST NOT embed raw file content, base64-encoded blobs, or
// any payload that would make the event row bloat to MBs. The
// handler is single-row; old/new Drive file ids are at most 64 chars
// each, and old_local_paths holds short filesystem paths.
type voiceoverCleanupRequestV1 struct {
	// Envelope carries schema_version/event_id/idempotency_key/requested_at;
	// the v1 envelope contract is decoded and validated once, in
	// outboxevents.DecodeV1, not once per handler.
	outboxevents.Envelope
	VoiceoverID    string   `json:"voiceover_id"`
	OldDriveFileID string   `json:"old_drive_file_id"`
	NewDriveFileID string   `json:"new_drive_file_id"`
	OldLocalPaths  []string `json:"old_local_paths"`
}

// VoiceoverCleanupHandler is the canonical handler for
// voiceover.cleanup.requested.v1.
//
// driver is required for production wiring (BuildOutboxBundle
// populates it from drive.Admin — the same instance that already
// satisfies VoiceoverCleanupDriver.DeleteFile structurally, so a
// single adapter instance can be shared across both port
// surfaces). Nil-safe: handler logs+skips when driver is nil (test
// path only; production wires non-nil).
//
// log nil → nop logger. The handler never blocks on logging; failures
// are reported via the returned error.
type VoiceoverCleanupHandler struct {
	driver VoiceoverCleanupDriver
	log    *zap.Logger
}

// NewVoiceoverCleanupHandler wires the producer-side dependencies.
// log nil → nop logger. driver MAY be nil in tests; production wiring
// MUST supply a non-nil adapter (BuildOutboxBundle asserts at wire time).
func NewVoiceoverCleanupHandler(driver VoiceoverCleanupDriver, log *zap.Logger) *VoiceoverCleanupHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &VoiceoverCleanupHandler{
		driver: driver,
		log:    log.Named("voiceover_cleanup"),
	}
}

// EventType returns the canonical outboxevents constant.
func (h *VoiceoverCleanupHandler) EventType() string {
	return outboxevents.EventVoiceoverCleanupRequested
}

// IdempotencyKey implements outboxevents.Handler (Fase 6(c) Push 6.2).
// Static canonical form: `<event_type>.<schema_version>` so the
// HandlerRegistry.Register fail-closed panic fires at init time if
// a future refactor forgets the declaration.
func (h *VoiceoverCleanupHandler) IdempotencyKey() string {
	return outboxevents.EventVoiceoverCleanupRequested + "." + VoiceoverCleanupSchemaVersion
}

// Handle parses the v1 envelope, performs the no-op gate
// (old==new|both-empty), then deletes the OLD/target Drive file (only
// when old != new AND old != "") and removes the OLD local audio paths.
// The pool retries transient errors per its exponential backoff.
// Terminal schema errors dead-letter via outboxevents.NewTerminalError
// (godlike/07 — retry cannot conjure a missing field).
//
// Idempotency contract:
//
//   - Same event delivered twice (pool retry or replay) is a no-op
//     the second time: the Drive file is already deleted (idempotent
//     404 at the API layer), and the local files are already gone
//     (os.IsNotExist swallowed).
//   - The "old == new" case is intentionally a no-op: the swap
//     landed on the same Drive file (no orphan), so no Drive delete
//     is needed. The local cleanup still runs because local paths
//     are independent of the Drive identity.
//
// Outcome label propagation: the function does NOT use named returns
// — every branch returns its error explicitly — so future edits
// adding a branch cannot accidentally overwrite an earlier `err =`
// assignment via a bare `return` (a maintenance landmine named
// returns introduce when mixed with bare returns). A local
// `outcome` string is reassigned in each branch and captured by the
// deferred audit-log closure.

func (h *VoiceoverCleanupHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	log := h.log
	req, reqLog, err := decodeVoiceoverCleanupRequest(evt, log)
	if err != nil {
		return err
	}

	terminalErr, err := h.cleanupDriveOrphan(ctx, req, reqLog, log)
	if err != nil {
		return err
	}
	if err := removeVoiceoverLocalPaths(req, reqLog, log); err != nil {
		return err
	}
	if terminalErr != nil {
		return terminalErr
	}
	log.Info("voiceover.cleanup.requested: cleanup complete", reqLog...)
	return nil
}

func decodeVoiceoverCleanupRequest(evt outboxevents.Event, log *zap.Logger) (voiceoverCleanupRequestV1, []zap.Field, error) {
	var req voiceoverCleanupRequestV1
	if err := outboxevents.DecodeV1(outboxevents.DecodePolicy{
		EventName:         outboxevents.EventVoiceoverCleanupRequested,
		WantSchemaVersion: VoiceoverCleanupSchemaVersion,
		Log:               log,
	}, evt, &req); err != nil {
		return voiceoverCleanupRequestV1{}, nil, err
	}
	if req.VoiceoverID == "" {
		log.Warn("voiceover.cleanup.requested: missing voiceover_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return voiceoverCleanupRequestV1{}, nil, outboxevents.NewTerminalError(
			fmt.Errorf("voiceover.cleanup.requested: voiceover_id is required (terminal — retry cannot conjure an id)"),
		)
	}
	reqLog := []zap.Field{
		zap.String("voiceover_id", req.VoiceoverID),
		zap.Int64("event_id", evt.ID),
		zap.String("outbox_event_id", req.EventID),
		zap.String("idempotency_key", req.IdempotencyKey),
		zap.Int("attempt", evt.AttemptCount),
		zap.String("old_drive_file_id", req.OldDriveFileID),
		zap.String("new_drive_file_id", req.NewDriveFileID),
		zap.Int("old_local_paths_count", len(req.OldLocalPaths)),
	}
	if req.RequestedAt != "" {
		reqLog = append(reqLog, zap.String("requested_at", req.RequestedAt))
	}
	return req, reqLog, nil
}

// cleanupDriveOrphan returns terminalErr for a composition failure that must
// be surfaced only after local cleanup, and err for an immediate retryable
// failure.
func (h *VoiceoverCleanupHandler) cleanupDriveOrphan(ctx context.Context, req voiceoverCleanupRequestV1, reqLog []zap.Field, log *zap.Logger) (terminalErr error, err error) {
	if req.OldDriveFileID != "" && req.OldDriveFileID == req.NewDriveFileID {
		log.Info("voiceover.cleanup.requested: old==new — swap landed on same Drive file, skipping Drive delete", reqLog...)
		return nil, nil
	}
	if req.OldDriveFileID == "" {
		return nil, nil
	}
	if h.driver == nil {
		log.Warn("voiceover.cleanup.requested: Drive driver not wired — local cleanup runs, Drive orphan remains (composition root misconfig)",
			append(reqLog, zap.String("orphan_drive_file_id", req.OldDriveFileID))...,
		)
		return outboxevents.NewTerminalError(fmt.Errorf(
			"voiceover.cleanup.requested: Drive driver not wired; orphan file %s not deleted",
			req.OldDriveFileID)), nil
	}

	log.Info("voiceover.cleanup.requested: deleting orphan Drive file", reqLog...)
	derr := h.driver.DeleteFile(ctx, req.OldDriveFileID)
	if derr == nil {
		return nil, nil
	}
	if errors.Is(derr, context.Canceled) || errors.Is(derr, context.DeadlineExceeded) {
		return nil, derr
	}
	var apiErr *googleapi.Error
	if errors.As(derr, &apiErr) && apiErr.Code == 404 {
		log.Info("voiceover.cleanup.requested: orphan Drive file already gone — idempotent success (404 treated as deleted)",
			append(reqLog, zap.Int("http_status", apiErr.Code))...,
		)
		return nil, nil
	}
	if strings.Contains(derr.Error(), "404") || strings.Contains(derr.Error(), "notFound") {
		log.Info("voiceover.cleanup.requested: orphan Drive file already gone — idempotent success (substring 404 fallback)", reqLog...)
		return nil, nil
	}
	log.Warn("voiceover.cleanup.requested: Drive delete failed (retryable)",
		append(reqLog, zap.Error(derr))...,
	)
	return nil, fmt.Errorf("voiceover.cleanup.requested DeleteFile(%s): %w", req.OldDriveFileID, derr)
}

func removeVoiceoverLocalPaths(req voiceoverCleanupRequestV1, reqLog []zap.Field, log *zap.Logger) error {
	for _, p := range req.OldLocalPaths {
		if p == "" {
			continue
		}
		rmErr := os.Remove(p)
		if rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			log.Warn("voiceover.cleanup.requested: local file remove failed (retryable)",
				append(reqLog, zap.String("path", p), zap.Error(rmErr))...,
			)
			return fmt.Errorf("voiceover.cleanup.requested os.Remove(%s): %w", p, rmErr)
		}
		if rmErr == nil {
			log.Debug("voiceover.cleanup.requested: removed local file", append(reqLog, zap.String("path", p))...)
		}
	}
	return nil
}
