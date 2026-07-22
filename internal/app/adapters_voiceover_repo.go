// Package app — voiceover VoiceoverRepository adapter
// (PR-VO-ADAPTERS-SPLIT, July 2026).
//
// Azione #8 (July 2026): DestinationResolver + DefaultFolderResolver
// extracted into adapters_voiceover_resolver.go per AGENTS.md Pattern 5
// (capability-split: repository ↔ resolver). This file now owns ONLY
// the SQL-bound persistence surface.
//
// VoiceoverRepository                ← *sqassets.VoiceoversRepository +
// │                                    *sql.DB (for BeginTx in P1-2)
//
// The persistence.Repository compile-time pin lives at the top of
// this file because useCaseRepoAdapter is the sole structural
// conformer to that package-surface port (the persistence sub-package
// in internal/application/voiceover/persistence is unimported from
// voiceover but applies the canonical Repository contract).
//
// Fail-closed: nil deps panic at construction (fail-fast per
// AGENTS.md WireUp pattern).
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

var _ persistence.Repository = (*useCaseRepoAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// VoiceoverRepository adapter.
//
// Implements persistence.Repository.InsertTx + DeleteByIDTx via
// tx.ExecContext on the canonical voiceovers table schema (mirrors
// the column set in *assets.VoiceoversRepository.Upsert to avoid
// schema drift). PreReadByID surfaces real (non-stub) rows to the
// use case so the post-commit cleanup goroutine can capture orphan
// paths.
//
// Schema source-of-truth: internal/infrastructure/database/sqlite/
// assets/voiceovers_repository.go. Adding a column here without a
// SQLite migration will fail at INSERT time, NOT at compile time.
// ─────────────────────────────────────────────────────────────────────

type useCaseRepoAdapter struct {
	repo *sqassets.VoiceoversRepository
	db   *sql.DB
}

func newUseCaseRepoAdapter(repo *sqassets.VoiceoversRepository, db *sql.DB) *useCaseRepoAdapter {
	if repo == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseRepoAdapter: repo is required (*sqassets.VoiceoversRepository)")
	}
	if db == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseRepoAdapter: db is required (*sql.DB, used by BeginTx in P1-2)")
	}
	return &useCaseRepoAdapter{repo: repo, db: db}
}

// BeginTx opens a new SQLite transaction on the production database.
// P1-2 (June 2026): the useCaseRepoAdapter previously only owned
// InsertTx / DeleteByIDTx / PreReadByID. P1-2 added BeginTx so the
// voiceover Service can thread the PR-VO-A2 atomic swap tx through
// the canonical persistence.Repository port instead of holding a
// bare *sql.DB handle.
func (a *useCaseRepoAdapter) BeginTx(ctx context.Context) (*sql.Tx, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("useCaseRepoAdapter.BeginTx: db not wired")
	}
	return a.db.BeginTx(ctx, nil)
}

// CountByDriveFileIDTx runs the PR-VO-B3 post-upload dedupe gate
// INSIDE the caller-owned tx. Returns the matched-row id, the
// total match count, and any error.
//
// P1-2 (June 2026): the application-layer helper
// applyDedupeByDriveFileID that lived in
// internal/application/voiceover/dedupe.go and consumed raw
// *sql.DB + *sql.Tx is NOT re-implemented here. The port
// method takes the tx parameter from the caller (which is
// already inside the PR-VO-A2 atomic-swap transaction) so the
// count runs against the same visibility boundary as the
// upcoming INSERT.
//
// Empty driveFileID short-circuits to (matchedID="", count=0,
// err=nil) so the Stage 3 caller can detect "no gate" via the
// empty id without a separate sentinel.
func (a *useCaseRepoAdapter) CountByDriveFileIDTx(
	ctx context.Context,
	tx *sql.Tx,
	currentID string,
	driveFileID string,
) (string, int, error) {
	if driveFileID == "" || tx == nil {
		return "", 0, nil
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	row := tx.QueryRowContext(ctx, `
		SELECT id, COALESCE(drive_link,''), COALESCE(local_path,''), COALESCE(file_hash,'')
		  FROM voiceovers
		 WHERE drive_file_id = ? AND id != ?
		 LIMIT 1
	`, driveFileID, currentID)
	var matchedID, driveLink, localPath, fileHash string
	if scanErr := row.Scan(&matchedID, &driveLink, &localPath, &fileHash); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return "", 0, nil
		}
		return "", 0, fmt.Errorf("CountByDriveFileIDTx: scan: %w", scanErr)
	}
	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM voiceovers WHERE drive_file_id = ? AND id != ?`,
		driveFileID, currentID,
	).Scan(&count); err != nil {
		// Count failed but we DID find the row: degrade to count=1
		// so the gate still reports the match without ambiguity
		// inflation (matches the pre-P1-2 applyDedupeByDriveFileID
		// graceful-degrade contract).
		return matchedID, 1, nil
	}
	return matchedID, count, nil
}

// toInfraRecord converts the application-layer VoiceoverRecord
// (string-form timestamps for JSON round-trip via job.Payload) to
// the infrastructure-layer Record (time.Time for SQLite-native + a
// DurationSeconds field for forward-compatibility). The two struct
// shapes are NOT identical: persistence.VoiceoverRecord is the wire
// shape (string timestamps), assets.Record is the SQLite shape
// (time.Time). Keeping the conversion localized here means a future
// schema migration does NOT require touching the converter again —
// the assets.Record surface IS the canonical column set.
func (a *useCaseRepoAdapter) toInfraRecord(rec *persistence.VoiceoverRecord) *sqassets.Record {
	if rec == nil {
		return nil
	}
	return &sqassets.Record{
		ID:        rec.ID,
		RequestID: rec.RequestID,
		// PR-VO-TYPED-PRIMITIVES (July 2026): the typed envelopes
		// (TextHash + Language) are converted to the underlying
		// string for the sqassets.Record wire shape (infrastructure
		// layer stays un-typed per the audit scope discipline).
		TextHash:        string(rec.TextHash),
		TextPreview:     rec.TextPreview,
		Language:        string(rec.Language),
		Voice:           rec.Voice,
		Filename:        rec.Filename,
		LocalPath:       rec.LocalPath,
		CleanedPath:     rec.CleanedPath,
		FolderID:        rec.FolderID,
		FolderPath:      rec.FolderPath,
		DriveFileID:     rec.DriveFileID,
		DriveLink:       rec.DriveLink,
		DownloadLink:    rec.DownloadLink,
		FileHash:        rec.FileHash,
		Status:          rec.Status,
		Error:           rec.Error,
		Strategy:        rec.Strategy,
		Metadata:        rec.Metadata,
		DurationSeconds: rec.DurationSeconds,
		// FASE 3 (July 2026): thread the deterministic idempotency
		// key and the producing job ID through to the SQLite row.
		IdempotencyKey: rec.IdempotencyKey,
		JobID:          rec.JobID,
		CreatedAt:      parseRFC3339OrNow(rec.CreatedAt),
		UpdatedAt:      parseRFC3339OrNow(rec.UpdatedAt),
	}
}

// fromInfraRecord is the inverse of toInfraRecord — used by
// PreReadByID to surface a real (non-stub) row to the use case so
// the post-commit cleanup goroutine can capture orphan paths.
func (a *useCaseRepoAdapter) fromInfraRecord(r *sqassets.Record) *persistence.VoiceoverRecord {
	if r == nil {
		return nil
	}
	createdAt := ""
	if !r.CreatedAt.IsZero() {
		createdAt = timeutil.FormatRFC3339(r.CreatedAt)
	}
	updatedAt := ""
	if !r.UpdatedAt.IsZero() {
		updatedAt = timeutil.FormatRFC3339(r.UpdatedAt)
	}
	return &persistence.VoiceoverRecord{
		ID:        r.ID,
		RequestID: r.RequestID,
		// PR-VO-TYPED-PRIMITIVES (July 2026): the persistence layer
		// (VoiceoverRecord) carries raw string fields for TextHash
		// and Language (per the Go-circular-import constraint — the
		// persistence sub-package cannot import the parent voiceover
		// package). The raw strings from the DB are forwarded verbatim
		// — the persistence layer IS the canonical source of truth.
		TextHash:     r.TextHash,
		TextPreview:  r.TextPreview,
		Language:     r.Language,
		Voice:        r.Voice,
		Filename:     r.Filename,
		LocalPath:    r.LocalPath,
		CleanedPath:  r.CleanedPath,
		FolderID:     r.FolderID,
		FolderPath:   r.FolderPath,
		DriveFileID:  r.DriveFileID,
		DriveLink:    r.DriveLink,
		DownloadLink: r.DownloadLink,
		FileHash:     r.FileHash,
		Status:       r.Status,
		Error:        r.Error,
		Strategy:     r.Strategy,
		Metadata:     r.Metadata,
		// FASE 3 (July 2026): round-trip through the infra layer.
		IdempotencyKey: r.IdempotencyKey,
		JobID:          r.JobID,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
}

// parseRFC3339OrNow parses an RFC3339 timestamp string into time.Time,
// returning time.Now() as a defensive fallback (matches the legacy
// pattern in *assets.VoiceoversRepository.Upsert). Keeps the helper
// private to the adapter to avoid spreading date-parsing helpers
// across packages.
func parseRFC3339OrNow(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	t := timeutil.ParseRFC3339(s)
	if !t.IsZero() {
		return t
	}
	return time.Now()
}

func (a *useCaseRepoAdapter) InsertTx(ctx context.Context, tx *sql.Tx, rec *persistence.VoiceoverRecord) error {
	if rec == nil {
		return fmt.Errorf("useCaseRepoAdapter.InsertTx: nil record")
	}
	return a.repo.InsertTx(ctx, tx, a.toInfraRecord(rec))
}

func (a *useCaseRepoAdapter) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	if id == "" {
		return fmt.Errorf("useCaseRepoAdapter.DeleteByIDTx: empty id")
	}
	return a.repo.DeleteByIDTx(ctx, tx, id)
}

// FindByIdempotencyKeyTx runs the FASE 3 idempotency gate (July 2026)
// INSIDE the caller-owned tx. Scans voiceovers for an existing row
// with the same idempotency_key. Returns (matchedID, nil) when a
// match is found (idempotency gate fires); returns ("", sql.ErrNoRows)
// when no match exists (first-time run).
//
// Empty idempotencyKey short-circuits to ("", sql.ErrNoRows, nil) —
// the gate is intentionally skipped for pre-FASE-3 callers.
func (a *useCaseRepoAdapter) FindByIdempotencyKeyTx(
	ctx context.Context,
	tx *sql.Tx,
	idempotencyKey string,
) (string, error) {
	if idempotencyKey == "" {
		return "", sql.ErrNoRows
	}
	if tx == nil {
		return "", fmt.Errorf("useCaseRepoAdapter.FindByIdempotencyKeyTx: nil tx")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var matchedID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM voiceovers WHERE idempotency_key = ? LIMIT 1`,
		idempotencyKey,
	).Scan(&matchedID)
	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows
	}
	if err != nil {
		return "", fmt.Errorf("FindByIdempotencyKeyTx: scan: %w", err)
	}
	return matchedID, nil
}

func (a *useCaseRepoAdapter) PreReadByID(ctx context.Context, id string) (*persistence.VoiceoverRecord, error) {
	if id == "" {
		return nil, fmt.Errorf("useCaseRepoAdapter.PreReadByID: empty id")
	}
	r, err := a.repo.PreReadByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("useCaseRepoAdapter.PreReadByID: %w", err)
	}
	if r == nil {
		return nil, nil
	}
	return a.fromInfraRecord(r), nil
}

// timeutil import is used here for FormatRFC3339 fallback in
// InsertTx; the canonical timeutil location avoids bringing in
// time.UTC().Format() boilerplate per Adapter struct.
