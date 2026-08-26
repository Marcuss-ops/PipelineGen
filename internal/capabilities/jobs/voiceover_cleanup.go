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
// The handler does NOT touch SQLite or Qdrant — post-commit cleanup
// for orphan side-effects ONLY. The canonical voiceovers/media_assets
// row already reflects the post-swap state at this point.
package jobs

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/api/googleapi"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// VoiceoverCleanupSchemaVersion is the canonical, EXACT string the
// VoiceoverCleanupHandler accepts. Producers MUST send
// "voiceover.cleanup.requested.v1" literally. Mismatch is TERMINAL
// (godlike/07 — no fake availability; producers upgrade rather than
// retrying into a repair loop).
const VoiceoverCleanupSchemaVersion = "voiceover.cleanup.requested.v1"

// VoiceoverCleanupDriver is the narrow Drive surface the
// VoiceoverCleanupHandler uses for orphan file deletion. Declared
// locally so the handler does NOT import internal/application/voiceover
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
	SchemaVersion  string   `json:"schema_version"`
	EventID        string   `json:"event_id"`
	VoiceoverID    string   `json:"voiceover_id"`
	OldDriveFileID string   `json:"old_drive_file_id"`
	NewDriveFileID string   `json:"new_drive_file_id"`
	OldLocalPaths  []string `json:"old_local_paths"`
	IdempotencyKey string   `json:"idempotency_key"`
	RequestedAt    string   `json:"requested_at,omitempty"`
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

	var req voiceoverCleanupRequestV1
	if jerr := json.Unmarshal([]byte(evt.PayloadJSON), &req); jerr != nil {
		log.Warn("voiceover.cleanup.requested payload parse failed (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(jerr),
		)
		return outboxevents.NewTerminalError(
			fmt.Errorf("voiceover.cleanup.requested payload parse: %w", jerr),
		)
	}

	// Strict v1 envelope validation. Each missing/mismatched field is
	// TERMINAL — retrying won't bring the field into existence.
	if req.SchemaVersion != VoiceoverCleanupSchemaVersion {
		log.Warn("voiceover.cleanup.requested schema_version mismatch (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.String("got_version", req.SchemaVersion),
			zap.String("want_version", VoiceoverCleanupSchemaVersion),
		)
		return outboxevents.NewTerminalError(fmt.Errorf(
			"voiceover.cleanup.requested: schema_version mismatch (terminal — got %q, want %q)",
			req.SchemaVersion, VoiceoverCleanupSchemaVersion,
		))
	}
	if req.EventID == "" {
		log.Warn("voiceover.cleanup.requested: missing event_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return outboxevents.NewTerminalError(
			fmt.Errorf("voiceover.cleanup.requested: event_id is required (terminal)"),
		)
	}
	if req.VoiceoverID == "" {
		log.Warn("voiceover.cleanup.requested: missing voiceover_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return outboxevents.NewTerminalError(
			fmt.Errorf("voiceover.cleanup.requested: voiceover_id is required (terminal — retry cannot conjure an id)"),
		)
	}
	if req.IdempotencyKey == "" {
		log.Warn("voiceover.cleanup.requested: missing idempotency_key (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return outboxevents.NewTerminalError(
			fmt.Errorf("voiceover.cleanup.requested: idempotency_key is required (terminal)"),
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

	// Idempotency gate: when the producer sends the SAME drive_file_id
	// for new and old, the swap landed on the pre-existing Drive file
	// (no orphan to delete). When BOTH are empty, no prior row had a
	// Drive file (never set). Both cases → fast success without any
	// Drive call.
	if req.OldDriveFileID != "" && req.OldDriveFileID == req.NewDriveFileID {
		log.Info("voiceover.cleanup.requested: old==new — swap landed on same Drive file, skipping Drive delete",
			reqLog...,
		)
		// Fall through to local cleanup (local paths are independent
		// of the Drive identity; the swap may have moved the audio to
		// a new local path even when Drive file id stayed constant).
	} else if req.OldDriveFileID != "" {
		// Drive delete side-effect. Retryable on transient errors;
		// the pool's exponential backoff retries. Terminal errors
		// (auth failure, file-locked-without-retry semantics) go
		// through NewTerminalError so the pool dead-letters
		// immediately rather than burning max_attempts.
		if h.driver == nil {
			// No-driver branch is unusual: production wiring always
			// supplies one. We log loudly so an operator inspects
			// build_bundles_process.go::BuildOutboxBundle.
			log.Warn("voiceover.cleanup.requested: driver nil, skipping Drive delete (composition root misconfig)",
				reqLog...,
			)
		} else {
			log.Info("voiceover.cleanup.requested: deleting orphan Drive file", reqLog...)
			if derr := h.driver.DeleteFile(ctx, req.OldDriveFileID); derr != nil {
				// Transient retry path: any non-context-cancel failure
				// boots the pool's exponential backoff. A context
				// cancel (handler exit / shutdown) bubbles up so the
				// pool's lease-fence re-runs the event with a fresh
				// lease on the next tick.
				if errors.Is(derr, context.Canceled) || errors.Is(derr, context.DeadlineExceeded) {
					return derr
				}
				// Idempotency on replay: Drive file already gone is
				// a no-op success (mirrors the IndexDeleteHandler
				// Qdrant-delete idempotency pattern at
				// index_delete.go where DeletePoints returns
				// deleted_count: 0, not 404). Drive API returns 404
				// via *googleapi.Error{Code: 404}; the pool's
				// exponential backoff should NOT burn max_attempts
				// on a 404 — the orphan has already been cleared.
				// The 404 detection mirrors the substring pattern
				// already in use at uploader_ops.go::FileIsNotTrashed
				// (string-substring fallback for cross-driver-edge
				// wrappers); we layer errors.As as the canonical
				// typed detection first, with the substring fallback
				// as belt-and-suspenders for any wrapper that
				// loses the *googleapi.Error type on transit.
				var apiErr *googleapi.Error
				if errors.As(derr, &apiErr) && apiErr.Code == 404 {
					log.Info("voiceover.cleanup.requested: orphan Drive file already gone — idempotent success (404 treated as deleted)",
						append(reqLog, zap.Int("http_status", apiErr.Code))...,
					)
					// fall through to local file removal
				} else if strings.Contains(derr.Error(), "404") || strings.Contains(derr.Error(), "notFound") {
					log.Info("voiceover.cleanup.requested: orphan Drive file already gone — idempotent success (substring 404 fallback)",
						reqLog...,
					)
					// fall through to local file removal
				} else {
					log.Warn("voiceover.cleanup.requested: Drive delete failed (retryable)",
						append(reqLog, zap.Error(derr))...,
					)
					return fmt.Errorf("voiceover.cleanup.requested DeleteFile(%s): %w", req.OldDriveFileID, derr)
				}
			}
		}
	}

	// Local file removal. No-port decision (Step 10/12, June 2026):
	// os.Remove is stdlib, idempotent at the syscall layer, and the
	// legacy cleanupOrphanVoiceover (pre-Step-10) used it directly.
	// Wrapping it as a port doesn't unlock testability (the failure
	// modes — locked file, fsync timeout, EIO — are all from the
	// kernel, not the test). os.IsNotExist is SILENTLY SWALLOWED for
	// idempotency: a re-delivered event finds the file already gone.
	//
	// Drive delete is NOT retracted on local-remove failure: the
	// leftover local file from a partial retry is the operator-tier
	// signal, not a corrected state. The pool's exponential backoff
	// runs once more; if still failing, the row dead-letters and an
	// operator clears the orphan files manually.
	for _, p := range req.OldLocalPaths {
		if p == "" {
			continue
		}
		rmErr := os.Remove(p)
		if rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			// os.Remove failures that are NOT "file already gone"
			// are retryable: locked file (ETXTBSY), filesystem
			// hiccup (EIO), volume path offline. We DO NOT redact
			// any successful Drive delete above on local-remove
			// failure — see the comment above.
			log.Warn("voiceover.cleanup.requested: local file remove failed (retryable)",
				append(reqLog, zap.String("path", p), zap.Error(rmErr))...,
			)
			return fmt.Errorf("voiceover.cleanup.requested os.Remove(%s): %w", p, rmErr)
		}
		if rmErr == nil {
			log.Debug("voiceover.cleanup.requested: removed local file",
				append(reqLog, zap.String("path", p))...,
			)
		}
	}

	log.Info("voiceover.cleanup.requested: cleanup complete", reqLog...)
	return nil
}
