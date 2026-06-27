// Package cleanup — assets.cleanup is the paginated async cleanup job
// that replaces the synchronous 10K-ListClipsPaged scan previously
// living in ClipOpsService.Cleanup (PR 5, June 2026 — branch
// codex/clips-cleanup-job).
//
// Why this exists:
//
// The pre-PR5 Cleanup flow ran every per-clip Verify + DeleteClip
// inside the HTTP handler goroutine, loading up to 10,000 clips in a
// single ListClipsPaged call. That violated three invariants the new
// spec pinned:
//
//   1. Eliminate synchronous scans → the work must move to the jobs
//      system.
//   2. Eliminate the 10K physical limit → pagination is mandatory;
//      the batch max is 250.
//   3. Cursor/checkpoint persisted → the handler iterates in batches
//      of 250 and persists cursor + per-batch metadata so a
//      cancellation or worker crash can be recovered from
//      (cursor emits as a structural field in the payload AND as a
//      "checkpoint" event for observability).
//
// Phase separation:
//
//   scan →  ListClipsPaged(source, batch=250, cursor.LastOffset, "")
//   classify →  per-clip status (orphan | coherent | hash_missing |
//                drive_trashed | local_missing)
//   repair →  if CheckDrive && Repair && status==hash_missing →
//              driveUploader.GetFileMD5(fileID) + repo.Upsert(clip)
//   delete → if !DryRun && Delete && status==orphan (or trashed) →
//              cleanup.DeleteClip(source, clipID, false)
//
// Spec exclusions:
//
//   ✓ The previous `synchronous CleanupOrphanFiles` fallback in
//     ClipOpsService is REMOVED — every path goes through this job
//     handler.
//   ✓ The 10,000-record ListClipsPaged is REPLACED with batch=250 +
//     cursor pagination.
//   ✓ The per-clip verifyClip call inside the handler is REMOVED —
//     classification is in-place, no extra DB hits per clip.
//   ✓ `delete` runs ONLY in the delete sub-phase; the scan phase does
//     not mutate rows.
//
// Resume semantics:
//
//   The handler checks `tools.IsCancelled()` between batches and
//   emits a "checkpoint" Event after each batch with the new cursor.
//   A subsequent Enqueue with the same ActiveKey creates a fresh
//   job (FindActiveByKey returns nil once the prior job is
//   terminal) inheriting the original payload + ActiveKey, which
//   the caller can pre-populate with cursor.LastOffset to resume.
package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"go.uber.org/zap"
)

// DefaultBatchSize is the per-batch row cap for asset scans. The spec
// pins 250; tests must exercise with a smaller cap (10–50) to keep
// fixtures compact while pinning the same pagination invariants.
const DefaultBatchSize = 250

// CheckpointEventType is the canonical event type emitted after every
// batch. Operators reading the job's events table can recover the
// last completed batch from the last "checkpoint" event.
const CheckpointEventType = "checkpoint"

// ── Typed payload + cursor (PR 5 cleanup shape) ──────────────────────────

// CleanupPayload is the JSON shape sent with JobTypeAssetsCleanup.
// Mirrors the spec list: source, dry_run, check_local, check_drive,
// repair, delete. Cursor lives inside the payload so a resume can be
// pre-populated by an operator via jobs.Service.Get → patch Payload →
// Enqueue (same ActiveKey).
type CleanupPayload struct {
	Source     string        `json:"source"`
	DryRun     bool          `json:"dry_run,omitempty"`
	CheckLocal bool          `json:"check_local,omitempty"`
	CheckDrive bool          `json:"check_drive,omitempty"`
	Repair     bool          `json:"repair,omitempty"`
	Delete     bool          `json:"delete,omitempty"`
	Cursor     CleanupCursor `json:"cursor"`
}

// CleanupCursor is the per-batch pagination checkpoint. Persisted
// inside the payload JSON; advanced by the handler after every
// completed batch.
type CleanupCursor struct {
	Source     string    `json:"source"`
	BatchSize  int       `json:"batch_size"`
	LastOffset int       `json:"last_offset"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

// ── Ports (mirror the established ClipRepositoryPort + adapters) ──────────

// ClipReader is the narrowed surface used by the scan phase. The full
// appclips.ClipRepositoryPort has many methods; this interface
// declares only ListClipsPaged + Get + Upsert which the cleaner needs.
// Code lives in ports.go; the composition root adapts the canonical
// *assets.ClipsRepository, *assets.VoiceoversRepository, and
// *assets.ImagesRepository into compatible ports via existing
// internal/app/clips_ops_adapters.go helpers.
type ClipReader interface {
	ListClipsPaged(ctx context.Context, source string, limit, offset int, query string) ([]*ClipAsset, error)
	Get(ctx context.Context, clipID string) (*ClipAsset, error)
	Upsert(ctx context.Context, clip *ClipAsset) error
}

// VoiceoverReader narrows the voiceover-side surface to ListAll +
// Upsert for the voiceover path of the cleanup scan (PR 5 converts
// voiceover "ListAll" into an offset-paginated loop so the same
// batch=250 invariant applies).
type VoiceoverReader interface {
	ListPaged(ctx context.Context, source string, limit, offset int) ([]*ClipVoiceoverEntry, error)
	GetByID(ctx context.Context, id string) (*ClipVoiceoverEntry, error)
	Upsert(ctx context.Context, rec *ClipVoiceoverEntry) error
}

// VoiceoverPaged is the adapter-friendly alias so callers can pass
// the existing `*appclips.ClipVoiceoverRecordDTO` directly. The
// cleaner does NOT need it; this file leaves the type as a thin
// indirection for future test mocks.
type VoiceoverPaged = ClipVoiceoverEntry

// DriveChecker is the narrowed Drive-side port used by repair +
// classify phases. If the Repair flag is true, GetFileMD5 is called
// for clips missing hashes; if the CheckDrive flag is true,
// FileIsNotTrashed is checked for the scan batch.
//
// The existing `appclips.ClipDriveUploaderPort` already declares
// GetFileMD5 + 11 other methods; the cleaner adapts to it via a
// shim inside internal/app/cleanup_adapters.go.
type DriveChecker interface {
	GetFileMD5(ctx context.Context, fileID string) (string, error)
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// DeletionPort is the narrow deletion surface — only DeleteClip is
// honored (trash, not permanent-delete) so the cleanup job can never
// accidentally permanently remove a clip from Drive.
type DeletionPort interface {
	DeleteClip(ctx context.Context, source, clipID string, hardDelete bool) error
}

// ── Cleaning service ──────────────────────────────────────────────────────

// Cleaner owns the assets.cleanup job handler. Construction via
// NewCleaner; every required port is passed in. Methods below mirror
// the canonical pipeline: HandleJob is registered against the
// `appjobs.TypeAssetsCleanup` job type at boot.
type Cleaner struct {
	clips          ClipReader
	voiceover      VoiceoverReader
	drive          DriveChecker
	deletion       DeletionPort
	log            *zap.Logger
	batchSize      int
	voiceoverBatch int
}

// NewCleaner constructs the canonical service. The clips/voiceover/
// drive/deletion ports are required; the cleaner downward-defaults
// batch sizes to the spec canonical values (250/250) and silences
// the logger to a no-op.
func NewCleaner(
	clips ClipReader,
	voiceover VoiceoverReader,
	drive DriveChecker,
	deletion DeletionPort,
	log *zap.Logger,
	batchSize int,
) *Cleaner {
	if log == nil {
		log = zap.NewNop()
	}
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	if batchSize > DefaultBatchSize {
		log.Warn("requested batch size exceeds canonical limit; capping at 250",
			zap.Int("requested", batchSize))
		batchSize = DefaultBatchSize
	}
	return &Cleaner{
		clips:          clips,
		voiceover:      voiceover,
		drive:          drive,
		deletion:       deletion,
		log:            log,
		batchSize:      batchSize,
		voiceoverBatch: batchSize,
	}
}

// Register wires this Cleaner into the jobs service. Idempotent: a
// second call returns the dispatcher error without re-registering.
func (c *Cleaner) Register(svc *appjobs.Service) error {
	if svc == nil {
		return errors.New("cleanup.Cleaner.Register: nil service")
	}
	if err := svc.RegisterHandler(appjobs.TypeAssetsCleanup, c.HandleJob); err != nil {
		return fmt.Errorf("cleanup.Cleaner.Register: %w", err)
	}
	return nil
}

// ── Handler ───────────────────────────────────────────────────────────────

// HandleJob runs the assets.cleanup job. Single-invocation model:
// the handler iterates batches of DefaultBatchSize until ListClipsPaged
// returns fewer rows than the batch OR cancellation is observed OR
// the source has been fully scanned. The cursor is advanced after
// every completed batch (in-memory) and emitted as both a payload
// event checkpoint and a progress message.
func (c *Cleaner) HandleJob(ctx context.Context, j *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if c.log != nil {
		c.log.Info("assets.cleanup: handler invoked",
			zap.String("job_id", j.ID))
	}

	// 1. Unmarshal payload (typed).
	var payload CleanupPayload
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &payload); err != nil {
			return nil, fmt.Errorf("assets.cleanup: unmarshal payload: %w", err)
		}
	}
	if payload.Source == "" {
		return nil, fmt.Errorf("assets.cleanup: source is required")
	}
	if payload.Cursor.BatchSize <= 0 {
		payload.Cursor.BatchSize = c.batchSize
	}

	// Snapshot initial cursor for resume-display.
	startOffset := payload.Cursor.LastOffset

	// Aggregated report (persisted on completion).
	report := map[string]any{
		"ok":            true,
		"source":        payload.Source,
		"dry_run":       payload.DryRun,
		"check_local":   payload.CheckLocal,
		"check_drive":   payload.CheckDrive,
		"repair":        payload.Repair,
		"delete":        payload.Delete,
		"batch_size":    payload.Cursor.BatchSize,
		"start_offset":  startOffset,
		"scanned":       0,
		"missing_local": 0,
		"missing_drive": 0,
		"trashed_drive": 0,
		"repaired":      0,
		"deleted":       0,
		"errors":        []string{},
	}

	if tools != nil && tools.Progress != nil {
		tools.Progress(0, fmt.Sprintf("assets.cleanup: scanning %s from offset=%d", payload.Source, startOffset))
	}

	// 2. Loop: scan → classify → (optionally) repair → (optionally) delete.
	source := strings.ToLower(payload.Source)
	for {
		// Cancellation gate at every batch boundary.
		if tools != nil && tools.IsCancelled != nil && tools.IsCancelled() {
			if c.log != nil {
				c.log.Info("assets.cleanup: cancellation observed; returning context.Canceled",
					zap.String("job_id", j.ID),
					zap.Int("last_offset", payload.Cursor.LastOffset))
			}
			return nil, context.Canceled
		}

		batch, err := c.nextBatch(ctx, source, payload.Cursor)
		if err != nil {
			return nil, fmt.Errorf("assets.cleanup: scan batch at offset=%d: %w", payload.Cursor.LastOffset, err)
		}
		if len(batch) == 0 {
			// Pagination exhausted.
			break
		}

		// Process the batch: classify + optionally repair + optionally delete.
		for _, item := range batch {
			scanned, added := c.processItem(ctx, &payload, item, report)
			if scanned {
				report["scanned"] = report["scanned"].(int) + 1
			}
			if added != "" {
				errs := report["errors"].([]string)
				report["errors"] = append(errs, added)
			}
		}

		// Advance the cursor in memory + persist a checkpoint event for
		// operators who want to resume from a partial run.
		payload.Cursor.LastOffset += len(batch)
		payload.Cursor.UpdatedAt = time.Now()

		if tools != nil && tools.Progress != nil {
			pct := c.progressPct(payload.Cursor.LastOffset, payload.Cursor.BatchSize)
			tools.Progress(pct, fmt.Sprintf("scanned %d (source=%s offset=%d)",
				report["scanned"].(int), payload.Source, payload.Cursor.LastOffset))
		}
		if tools != nil && tools.Event != nil {
			tools.Event(CheckpointEventType, fmt.Sprintf("scanned batch end @ offset=%d", payload.Cursor.LastOffset), map[string]any{
				"source":   payload.Source,
				"offset":   payload.Cursor.LastOffset,
				"batch":    payload.Cursor.BatchSize,
				"scanned":  report["scanned"].(int),
				"repaired": report["repaired"].(int),
				"deleted":  report["deleted"].(int),
			})
		}

		// Stop early if the batch was short — natural end of pagination.
		if len(batch) < payload.Cursor.BatchSize {
			break
		}
	}

	// 3. Final report.
	if tools != nil && tools.Progress != nil {
		tools.Progress(100, fmt.Sprintf("assets.cleanup: completed (scanned=%d deleted=%d repaired=%d errors=%d)",
			report["scanned"].(int), report["deleted"].(int), report["repaired"].(int), len(report["errors"].([]string))))
	}
	report["end_offset"] = payload.Cursor.LastOffset
	report["cursor"] = map[string]any{
		"source":     payload.Cursor.Source,
		"batch_size": payload.Cursor.BatchSize,
		"last_offset": payload.Cursor.LastOffset,
		"updated_at": payload.Cursor.UpdatedAt,
	}
	return report, nil
}

// ── Internal helpers ──────────────────────────────────────────────────────

// nextBatch returns the next batch of clips for the given source
// starting from cursor.LastOffset. Voiceover uses ListPaged (a
// future PR will add this to voiceoverRepositoryPort); other sources
// use the canonical ListClipsPaged.
func (c *Cleaner) nextBatch(ctx context.Context, source string, cursor CleanupCursor) ([]ClipAsset, error) {
	if source == "voiceover" {
		if c.voiceover == nil {
			return nil, fmt.Errorf("assets.cleanup: voiceover reader not wired")
		}
		recs, err := c.voiceover.ListPaged(ctx, source, cursor.BatchSize, cursor.LastOffset)
		if err != nil {
			return nil, err
		}
		out := make([]ClipAsset, 0, len(recs))
		for _, rec := range recs {
			if rec == nil {
				continue
			}
			out = append(out, ClipAsset{
				ID:           rec.ID,
				Source:       "voiceover",
				Name:         rec.Filename,
				LocalPath:    rec.LocalPath,
				DriveLink:    rec.DriveLink,
				DriveFileID:  rec.DriveFileID,
				DownloadLink: rec.DownloadLink,
				FileHash:     rec.FileHash,
				FolderID:     rec.FolderID,
				FolderPath:   rec.FolderPath,
			})
		}
		return out, nil
	}
	if c.clips == nil {
		return nil, fmt.Errorf("assets.cleanup: clips reader not wired")
	}
	raw, err := c.clips.ListClipsPaged(ctx, source, cursor.BatchSize, cursor.LastOffset, "")
	if err != nil {
		return nil, err
	}
	out := make([]ClipAsset, 0, len(raw))
	for _, c := range raw {
		if c == nil {
			continue
		}
		out = append(out, *c)
	}
	return out, nil
}

// processItem classifies + (optionally) repairs + (optionally)
// deletes a single clip. Returns:
//   - scanned: true when the clip was evaluated (not skipped).
//   - errStr: a non-empty string if a phase failed mid-batch (NOT a
//     hard abort — the batch continues; the error is appended to
//     the report.errors slice).
func (c *Cleaner) processItem(ctx context.Context, payload *CleanupPayload, clip ClipAsset, report map[string]any) (scanned bool, errStr string) {
	if clip.ID == "" {
		return false, ""
	}
	scanned = true

	class := classifyClip(clip, payload)

	// Repair phase: hash_missing + drive link → fetch MD5 + persist.
	if payload.Repair && class == classHashMissing && payload.CheckDrive && clip.DriveFileID != "" && c.drive != nil {
		md5, err := c.drive.GetFileMD5(ctx, clip.DriveFileID)
		if err != nil {
			errStr = fmt.Sprintf("repair GetFileMD5(clip=%s, file=%s): %v", clip.ID, clip.DriveFileID, err)
			return
		}
		if md5 != "" {
			clip.FileHash = md5
			if err := c.persistHash(ctx, payload.Source, &clip); err != nil {
				errStr = fmt.Sprintf("repair Upsert(clip=%s): %v", clip.ID, err)
				return
			}
			report["repaired"] = report["repaired"].(int) + 1
		}
	}

	// Drive check phase: trashed → flag as missing_drive / trashed_drive.
	if payload.CheckDrive && clip.DriveFileID != "" && c.drive != nil {
		ok, err := c.drive.FileIsNotTrashed(ctx, clip.DriveFileID)
		if err == nil && !ok {
			report["trashed_drive"] = report["trashed_drive"].(int) + 1
			class = classDriveTrashed
		}
	}

	// Delete phase: orphan → soft-delete (Trash). DryRun disables.
	if payload.Delete && !payload.DryRun && (class == classOrphan || class == classDriveTrashed) && c.deletion != nil {
		if err := c.deletion.DeleteClip(ctx, payload.Source, clip.ID, false); err != nil {
			errStr = fmt.Sprintf("delete DeleteClip(clip=%s, source=%s): %v", clip.ID, payload.Source, err)
			return
		}
		report["deleted"] = report["deleted"].(int) + 1
	}

	// Tally classification counts.
	switch class {
	case classLocalMissing:
		report["missing_local"] = report["missing_local"].(int) + 1
	case classDriveMissing:
		report["missing_drive"] = report["missing_drive"].(int) + 1
	}
	return scanned, errStr
}

// persistHash writes back the recovered MD5 to either the clips
// repo (a generic source) or the voiceover repo (voiceover-only).
func (c *Cleaner) persistHash(ctx context.Context, source string, clip *ClipAsset) error {
	if strings.EqualFold(source, "voiceover") && c.voiceover != nil {
		rec, err := c.voiceover.GetByID(ctx, clip.ID)
		if err != nil || rec == nil {
			return fmt.Errorf("voiceover upsert: %w", err)
		}
		rec.FileHash = clip.FileHash
		return c.voiceover.Upsert(ctx, rec)
	}
	if c.clips == nil {
		return nil
	}
	return c.clips.Upsert(ctx, clip)
}

// progressPct maps an offset to a 0-100 progress percent. Pages of
// unknown total default to a coarse map (offset/batch_size — capped).
// When the offset grows large, this saturates near 95 to leave headroom
// for the wrap-up phase + report aggregation.
func (c *Cleaner) progressPct(offset, batch int) int {
	if batch <= 0 {
		return 0
	}
	batches := offset / batch
	if batches <= 0 {
		return 5
	}
	pct := batches * 10
	if pct >= 95 {
		return 95
	}
	return pct
}

// ── Clip classification ───────────────────────────────────────────────────

// classifyClip classifies a clip into one of four buckets based on
// the request flags (CheckLocal + CheckDrive) and the clip's
// attributes. Buckets:
//
//   classCoherent       — DB row + (local file OR drive link) + hash
//   classOrphan         — DB row missing BOTH local file AND drive link
//   classLocalMissing   — DB row + drive link, but local_path not on disk
//   classDriveMissing   — DB row + local file, but no drive link
//   classHashMissing    — DB row + everything else, but no file_hash
//   classDriveTrashed   — set by HandleJob after FileIsNotTrashed=false
type clipClass string

const (
	classCoherent     clipClass = "coherent"
	classOrphan       clipClass = "orphan"
	classLocalMissing clipClass = "local_missing"
	classDriveMissing clipClass = "drive_missing"
	classHashMissing  clipClass = "hash_missing"
	classDriveTrashed clipClass = "drive_trashed"
)

func classifyClip(clip ClipAsset, payload *CleanupPayload) clipClass {
	hasLocal := clip.LocalPath != ""
	if payload.CheckLocal && hasLocal {
		if _, err := os.Stat(clip.LocalPath); err != nil {
			hasLocal = false
		}
	}
	hasDrive := clip.DriveFileID != "" || clip.DriveLink != "" || clip.DownloadLink != ""
	hasHash := clip.FileHash != ""

	switch {
	case !hasLocal && !hasDrive:
		return classOrphan
	case hasLocal && !hasDrive:
		return classDriveMissing
	case !hasLocal && hasDrive:
		return classLocalMissing
	case hasLocal && hasDrive && !hasHash:
		return classHashMissing
	default:
		return classCoherent
	}
}

// ── Reconciling with the existing app-level types ────────────────────────

// ClipAsset is the cleaner-scoped projection of *appclips.ClipAsset
// (the canonical clip object) with the fields the cleaner inspects.
// It is intentionally narrower than the full domain asset.Asset so
// the cleaner does NOT import domain/asset and stays application-port
// friendly. The composition root constructs newClipsAdapter to map
// between ClipAsset and the canonical domain/asset.Asset.
type ClipAsset = appclips.CleanupClip

// ClipVoiceoverEntry is the cleaner-scoped projection of
// *appclips.ClipVoiceoverRecordDTO.
type ClipVoiceoverEntry = appclips.CleanupVoiceoverRecord
