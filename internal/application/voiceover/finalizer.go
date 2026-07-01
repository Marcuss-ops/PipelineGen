// Package voiceover — finalizer.go (P0.4 Fase 3a, unified finalization, July 2026).
//
// voiceoverFinalizer is the SINGLE canonical implementation of the
// VoiceoverFinalizer port. It replaces the two divergent finalization
// paths:
//
//   Path A (child pipeline, process_voiceover_item.go Stage 4):
//     BeginTx → DeleteByIDTx → InsertTx → outbox EnqueueIndexEvent → Commit
//     (MISSING: dedupe gate, media_assets projection, cleanup event)
//
//   Path B (legacy batch, stages.go finalizeStage):
//     BeginTx → dedupe gate → DeleteByIDTx → InsertTx →
//     UpsertVoiceoverProjectionTx → outbox EnqueueIndexEvent →
//     outbox EnqueueCleanupEvent → Commit
//
// Post-Fase-3a BOTH paths call the same Finalize(ctx, tx, cmd) method.
// The caller owns the transaction (BeginTx + Commit); the finalizer
// owns the 6 logical steps inside the tx.
//
// Optional steps are nil-safe:
//   - dedupe: skipped when DriveFileID is empty
//   - media_assets projection: skipped when lifecycleService is nil
//   - index outbox: skipped when outbox is nil or FileHash is empty
//   - cleanup outbox: skipped when ShouldSwap is false or outbox is nil
package voiceover

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────────
// FinalizeCommand — canonical DTO for both finalization paths
// ─────────────────────────────────────────────────────────────────────

// FinalizeCommand carries all data needed for the 6-step finalization
// inside a caller-owned transaction. Every field is populated by the
// caller; the finalizer reads them but never mutates the command.
type FinalizeCommand struct {
	// Identity & Content
	ID        string
	RequestID string
	TextHash  string
	Text      string // Full text; truncated to 100 chars for preview column
	Language  string
	Voice     string
	Filename  string
	Strategy  string
	MetaJSON  []byte // Canonical JSON metadata map

	// Audio Asset State
	LocalPath   string
	CleanedPath string
	FileHash    string // Index outbox skipped when empty

	// Destination & Drive State
	FolderID     string
	FolderPath   string
	DriveFileID  string // Dedupe gate skipped when empty
	DriveLink    string
	DownloadLink string

	// Replace-Mode Cleanup Context
	// When ShouldSwap is true AND outbox is wired, the finalizer
	// enqueues a voiceover.cleanup.requested event for the old
	// Drive file + local paths. Nil-safe when false.
	ShouldSwap     bool
	OldDriveFileID string
	OldLocalPath   string
	OldCleanedPath string
}

// ─────────────────────────────────────────────────────────────────────
// FinalizeResult — dedupe outcome
// ─────────────────────────────────────────────────────────────────────

// FinalizeResult is returned by VoiceoverFinalizer.Finalize so the
// caller can observe whether the dedupe gate matched an existing row
// and update the canonical ID accordingly.
type FinalizeResult struct {
	// ID is the canonical voiceover ID after finalization.
	// Equal to cmd.ID except when Reused is true (then it's the
	// matched existing row ID from the dedupe gate).
	ID string

	// Reused is true when the dedupe gate matched an existing row
	// and the finalizer short-circuited persistence (no INSERT,
	// no projection, no outbox). The caller should use the
	// returned ID as the canonical identifier.
	Reused bool
}

// ─────────────────────────────────────────────────────────────────────
// voiceoverFinalizer — concrete implementation
// ─────────────────────────────────────────────────────────────────────

// voiceoverFinalizerDeps holds the external dependencies for the
// concrete finalizer. All ports are optional (nil-safe) except
// voiceoverRepo which is mandatory (INSERT/DELETE are always needed).
type voiceoverFinalizerDeps struct {
	VoiceoverRepo    VoiceoverRepository // mandatory
	Outbox           TxOutboxEnqueuer    // nil-safe (skip index + cleanup)
	LifecycleService LifecycleProjectionUpserter // nil-safe (skip media_assets)
	Logger           *zap.Logger         // nil-safe via zap.NewNop()
}

// LifecycleProjectionUpserter is the narrow port for writing the
// media_assets projection row. Extracted so the finalizer doesn't
// depend on the full lifecycle.Service surface. The production
// concrete is *lifecycle.Service.UpsertVoiceoverProjectionTx.
type LifecycleProjectionUpserter interface {
	UpsertVoiceoverProjectionTx(ctx context.Context, tx *sql.Tx, input *VoiceoverProjectionInput) error
}

// VoiceoverProjectionInput is the canonical input shape for the
// media_assets projection UPSERT.
type VoiceoverProjectionInput struct {
	ID           string
	Source       string
	Name         string
	Filename     string
	FolderID     string
	FolderPath   string
	MediaType    string
	LocalPath    string
	DriveFileID  string
	DriveLink    string
	DownloadLink string
	FileHash     string
	Language     string
	Status       string
	Metadata     string
}

// voiceoverFinalizer is the concrete implementation of VoiceoverFinalizer.
type voiceoverFinalizer struct {
	deps voiceoverFinalizerDeps
}

// newVoiceoverFinalizer constructs the finalizer. Only voiceoverRepo is
// mandatory (panic on nil — fail-fast per AGENTS.md WireUp pattern).
func newVoiceoverFinalizer(deps voiceoverFinalizerDeps) *voiceoverFinalizer {
	if deps.VoiceoverRepo == nil {
		panic("voiceover.newVoiceoverFinalizer: VoiceoverRepo is required")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &voiceoverFinalizer{deps: deps}
}

// NewVoiceoverFinalizer is the exported constructor for the unified
// VoiceoverFinalizer (P0.4 Fase 3a, July 2026). The composition root
// (internal/app/build_bundles_voiceover.go) calls this to construct a
// SINGLE finalizer instance shared by both the child pipeline
// (ProcessVoiceoverItemUseCase) and the legacy batch pipeline (Service).
//
// Parameters:
//   - voRepo: persistence.Repository (BeginTx, InsertTx, DeleteByIDTx,
//     CountByDriveFileIDTx) — mandatory, panics on nil.
//   - outbox: TxOutboxEnqueuer (EnqueueIndexEvent, EnqueueCleanupEvent)
//     — nil-safe (skip outbox steps when nil).
//   - lifecycleSvc: LifecycleProjectionUpserter (UpsertVoiceoverProjectionTx)
//     — nil-safe (skip media_assets projection when nil).
//   - log: *zap.Logger — nil-safe (zap.NewNop() fallback).
//
// Returns VoiceoverFinalizer so the composition root can inject an
// interface — test doubles swap the concrete without churn.
func NewVoiceoverFinalizer(
	voRepo VoiceoverRepository,
	outbox TxOutboxEnqueuer,
	lifecycleSvc LifecycleProjectionUpserter,
	log *zap.Logger,
) VoiceoverFinalizer {
	return newVoiceoverFinalizer(voiceoverFinalizerDeps{
		VoiceoverRepo:    voRepo,
		Outbox:           outbox,
		LifecycleService: lifecycleSvc,
		Logger:           log,
	})
}

// Compile-time assertion (AGENTS.md Pattern 0).
var _ VoiceoverFinalizer = (*voiceoverFinalizer)(nil)

// Finalize runs the canonical 6-step atomic commit sequence inside the
// caller-owned transaction. The caller opens the tx, calls Finalize,
// then commits. Steps 2-6 may be skipped based on zero-value guards.
func (f *voiceoverFinalizer) Finalize(ctx context.Context, tx *sql.Tx, cmd *FinalizeCommand) (*FinalizeResult, error) {
	if cmd == nil {
		return nil, fmt.Errorf("voiceoverFinalizer.Finalize: nil cmd")
	}
	if tx == nil {
		return nil, fmt.Errorf("voiceoverFinalizer.Finalize: nil tx (caller must open before calling Finalize)")
	}

	// ── Step 1: Dedupe Gate (PR-VO-B3) ──
	// Runs INSIDE the tx so the count query is consistent with the
	// upcoming INSERT. Skipped when DriveFileID is empty.
	if cmd.DriveFileID != "" {
		matchedID, count, _ := f.deps.VoiceoverRepo.CountByDriveFileIDTx(ctx, tx, cmd.ID, cmd.DriveFileID)
		switch DecideDedupe(count) {
		case DedupeConflict:
			return nil, fmt.Errorf("voiceoverFinalizer: PR-VO-B3 ambiguous dedupe (count=%d, dedupe_id=%s) — refusing to insert duplicate row against established DriveFileID",
				count, matchedID)
		case DedupeReuse:
			f.deps.Logger.Info("voiceoverFinalizer: dedupe reuse — matched single prior row, skipping insert",
				zap.String("item_id", cmd.ID),
				zap.String("dedupe_id", matchedID))
			return &FinalizeResult{ID: matchedID, Reused: true}, nil
		case DedupeContinue:
			// Proceed with insert.
		}
	}

	// ── Step 2: Atomic Swap — DELETE old row ──
	if err := f.deps.VoiceoverRepo.DeleteByIDTx(ctx, tx, cmd.ID); err != nil {
		return nil, fmt.Errorf("voiceoverFinalizer: DeleteByIDTx: %w", err)
	}

	// ── Step 3: INSERT new row ──
	now := time.Now().UTC().Format(time.RFC3339)
	textPreview := cmd.Text
	if len(textPreview) > 100 {
		textPreview = textPreview[:100]
	}

	rec := &VoiceoverRecord{
		ID:           cmd.ID,
		RequestID:    cmd.RequestID,
		TextHash:     cmd.TextHash,
		TextPreview:  textPreview,
		Language:     cmd.Language,
		Voice:        cmd.Voice,
		Filename:     cmd.Filename,
		LocalPath:    cmd.LocalPath,
		CleanedPath:  cmd.CleanedPath,
		FolderID:     cmd.FolderID,
		FolderPath:   cmd.FolderPath,
		DriveFileID:  cmd.DriveFileID,
		DriveLink:    cmd.DriveLink,
		DownloadLink: cmd.DownloadLink,
		FileHash:     cmd.FileHash,
		Status:       string(StatusGenerated),
		Strategy:     cmd.Strategy,
		Metadata:     string(cmd.MetaJSON),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := f.deps.VoiceoverRepo.InsertTx(ctx, tx, rec); err != nil {
		return nil, fmt.Errorf("voiceoverFinalizer: InsertTx: %w", err)
	}

	// ── Step 4: media_assets projection UPSERT ──
	// P0.4 Fase 3a (July 2026): this step was MISSING in the child
	// pipeline (Path A). The unified finalizer now writes the
	// media_assets row for BOTH paths.
	if f.deps.LifecycleService != nil {
		if err := f.deps.LifecycleService.UpsertVoiceoverProjectionTx(ctx, tx, &VoiceoverProjectionInput{
			ID:           cmd.ID,
			Source:       "voiceover",
			Name:         textPreview,
			Filename:     cmd.Filename,
			FolderID:     cmd.FolderID,
			FolderPath:   cmd.FolderPath,
			MediaType:    "audio",
			LocalPath:    cmd.LocalPath,
			DriveFileID:  cmd.DriveFileID,
			DriveLink:    cmd.DriveLink,
			DownloadLink: cmd.DownloadLink,
			FileHash:     cmd.FileHash,
			Language:     cmd.Language,
			Status:       string(StatusGenerated),
			Metadata:     string(cmd.MetaJSON),
		}); err != nil {
			return nil, fmt.Errorf("voiceoverFinalizer: UpsertVoiceoverProjectionTx (media_assets): %w", err)
		}
	}

	// ── Step 5: Outbox — asset.index.requested ──
	if f.deps.Outbox != nil && cmd.FileHash != "" {
		if err := f.deps.Outbox.EnqueueIndexEvent(ctx, tx, cmd.ID, cmd.FileHash); err != nil {
			return nil, fmt.Errorf("voiceoverFinalizer: EnqueueIndexEvent: %w", err)
		}
	}

	// ── Step 6: Outbox — voiceover.cleanup.requested (replace mode) ──
	if cmd.ShouldSwap && f.deps.Outbox != nil {
		var oldLocalPaths []string
		if cmd.OldLocalPath != "" {
			oldLocalPaths = append(oldLocalPaths, cmd.OldLocalPath)
		}
		if cmd.OldCleanedPath != "" && cmd.OldCleanedPath != cmd.OldLocalPath {
			oldLocalPaths = append(oldLocalPaths, cmd.OldCleanedPath)
		}
		if cmd.OldDriveFileID != "" || len(oldLocalPaths) > 0 {
			if err := f.deps.Outbox.EnqueueCleanupEvent(ctx, tx,
				cmd.ID,
				cmd.OldDriveFileID,
				cmd.DriveFileID,
				oldLocalPaths,
			); err != nil {
				return nil, fmt.Errorf("voiceoverFinalizer: EnqueueCleanupEvent: %w", err)
			}
		}
	}

	return &FinalizeResult{ID: cmd.ID, Reused: false}, nil
}
