// Package app — voiceover VoiceoverRepository + DestinationResolver +
// VoiceoverDefaultFolderResolver adapters (PR-VO-ADAPTERS-SPLIT,
// July 2026).
//
// Capability cluster: REPOSITORY + RESOLVER (SQL-bound persistence +
// folder/path resolution). This is the canonical home for the heavy
// *sql.DB / *sql.Tx ownership surface per godlike/06 SSOT
// (one-canonical-owner-per-fact).
//
// VoiceoverRepository                ← *sqassets.VoiceoversRepository +
// │                                    *sql.DB (for BeginTx in P1-2)
// DestinationResolver                ← asset.Resolver (forward all 7 fields)
// VoiceoverDefaultFolderResolver     ← cfg.Drive.VoiceoverFolder() (PR 6 P0.2)
//
// The persistence.Repository compile-time pin lives at the top of
// this file because useCaseRepoAdapter is the sole structural
// conformer to that package-surface port (the persistence sub-package
// in internal/application/voiceover/persistence is unimported from
// voiceover but applies the canonical Repository contract).
//
// Fail-closed: nil deps panic at construction (fail-fast per
// AGENTS.md WireUp pattern). The DefaultFolderResolver constructor is
// non-panicking (empty driveFolderID is the production case for
// deployments without configured voiceover_root_folder — adapter
// returns ("", "", false) and Execute maps that to the canonical
// missing_folder_id short-circuit).
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

var _ persistence.Repository = (*useCaseRepoAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// VoiceoverRepository adapter.
//
// Implements voiceover.VoiceoverRepository.InsertTx + DeleteByIDTx via
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
// shapes are NOT identical: voiceover.VoiceoverRecord is the wire
// shape (string timestamps), assets.Record is the SQLite shape
// (time.Time). Keeping the conversion localized here means a future
// schema migration does NOT require touching the converter again —
// the assets.Record surface IS the canonical column set.
func (a *useCaseRepoAdapter) toInfraRecord(rec *voiceover.VoiceoverRecord) *sqassets.Record {
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
		DurationSeconds: 0, // canonical use case does not track duration today
		CreatedAt:       parseRFC3339OrNow(rec.CreatedAt),
		UpdatedAt:       parseRFC3339OrNow(rec.UpdatedAt),
	}
}

// fromInfraRecord is the inverse of toInfraRecord — used by
// PreReadByID to surface a real (non-stub) row to the use case so
// the post-commit cleanup goroutine can capture orphan paths.
func (a *useCaseRepoAdapter) fromInfraRecord(r *sqassets.Record) *voiceover.VoiceoverRecord {
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
	return &voiceover.VoiceoverRecord{
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
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
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

func (a *useCaseRepoAdapter) InsertTx(ctx context.Context, tx *sql.Tx, rec *voiceover.VoiceoverRecord) error {
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

func (a *useCaseRepoAdapter) PreReadByID(ctx context.Context, id string) (*voiceover.VoiceoverRecord, error) {
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

// ─────────────────────────────────────────────────────────────────────
// DestinationResolver adapter.
//
// Implements voiceover.DestinationResolver.Resolve over an
// asset.Resolver. Mirrors the production body in *Service.resolveDestination
// (metadata.go) so the legacy + use-case paths route identically:
//
//  1. FORWARD: ALL DestinationRequest fields (Group, FolderID, FolderPath,
//     SubfolderName, CreateSubfolder, StyleGroup) land in the
//     asset.ResolveRequest (Source = "voiceover" hardcoded).
//     P0.2 destination-adapter fix (July 2026): pre-fix only Group +
//     StyleGroup were forwarded; FolderID, FolderPath, SubfolderName,
//     and CreateSubfolder were silently dropped, so any explicit
//     routing intent (Kind="explicit" with FolderID) was ignored by
//     the resolver.
//  2. MIRROR:  dest.StyleGroup and dest.SubfolderName are mirrored
//     verbatim onto the returned ResolvedDestination. When dest.FolderID
//     or dest.FolderPath are explicitly set, they take precedence over
//     the resolver's returned values (explicit override).
//  3. Nil-safe: nil dest → error; nil resolver → panic at constructor
//     time (fail-fast per AGENTS.md WireUp pattern).
// ─────────────────────────────────────────────────────────────────────

type useCaseDestResolverAdapter struct {
	resolver asset.Resolver
}

func newUseCaseDestResolverAdapter(r asset.Resolver) *useCaseDestResolverAdapter {
	if r == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseDestResolverAdapter: resolver is required (asset.Resolver)")
	}
	return &useCaseDestResolverAdapter{resolver: r}
}

func (a *useCaseDestResolverAdapter) Resolve(ctx context.Context, dest *voiceover.DestinationRequest) (*voiceover.ResolvedDestination, error) {
	if dest == nil {
		return nil, fmt.Errorf("useCaseDestResolverAdapter.Resolve: nil DestinationRequest")
	}
	res, err := a.resolver.Resolve(ctx, &asset.ResolveRequest{
		Source:          "voiceover",
		Group:           dest.Group,
		FolderID:        dest.FolderID,
		FolderPath:      dest.FolderPath,
		SubfolderName:   dest.SubfolderName,
		CreateSubfolder: dest.CreateSubfolder,
		StyleGroup:      string(dest.StyleGroup),
	})
	if err != nil {
		return nil, fmt.Errorf("useCaseDestResolverAdapter.Resolve: resolver failed: %w", err)
	}
	if res == nil {
		// Defensive: a misbehaving resolver may return (nil, nil).
		// Fallback to an empty result so downstream code can proceed
		// with zero-valued folder fields (mirrors the legacy
		// *Service.resolveDestination behavior in metadata.go).
		res = &asset.ResolveResult{}
	}
	// P0.2 fix (July 2026): when dest.FolderID is explicitly set,
	// use it directly instead of the resolver's result (explicit
	// override). The resolver is a folder-resolution layer; an
	// explicit FolderID from the caller means "use this exact folder,
	// don't resolve through Group/SubfolderName".
	folderID := res.FolderID
	if dest.FolderID != "" {
		folderID = dest.FolderID
	}
	folderPath := res.FolderPath
	if dest.FolderPath != "" {
		folderPath = dest.FolderPath
	}
	return &voiceover.ResolvedDestination{
		Group:         dest.Group,
		FolderID:      folderID,
		FolderPath:    folderPath,
		DriveLink:     res.DriveLink,
		SubfolderName: dest.SubfolderName, // MIRROR verbatim (P0.2 fix)
		StyleGroup:    dest.StyleGroup,    // MIRROR verbatim (NOT from resolver result).
	}, nil
}

var _ voiceover.DestinationResolver = (*useCaseDestResolverAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// VoiceoverDefaultFolderResolver adapter.
//
// PR 6 P0.2 (June 2026): when a GenerateVoiceoversCommand arrives
// without cmd.Destination, the use case falls back to the configured
// default Voiceover folder — the same value the legacy
// *Service.processLanguage folds in via `req.Destination =
// &DestinationRequest{FolderID: s.cfg.Drive.VoiceoverFolder()}`
// (process.go:75-79). The adapter takes the resolved folder ID at
// composition time (one read, deterministic), rather than re-reading
// cfg on every call, so:
//   - the wire shape is identical to the legacy path;
//   - the value is visible to operators via buildVoiceoverService's
//     constructor call (audit-friendly);
//   - a future "live re-read" wiring would be a port-method addition,
//     not an adapter rewrite.
//
// Resolve semantics mirror the canonical PR 6 P0.2 contract:
//   ("<folderID>", true)  → Execute synthesises a ResolvedDestination
//                            with that FolderID and proceeds.
//   ("", false)            → Execute surfaces a cross-cutting failure
//                            mapping to HTTP 400 upstream semantics.
//
// Nil-safe: nil receiver returns ("", false) so a partially-wired
// composition root cannot crash the per-language fan-out.
// ─────────────────────────────────────────────────────────────────────

type useCaseDefaultFolderResolverAdapter struct {
	driveFolderID  string
	localOutputDir string
}

func newUseCaseDefaultFolderResolverAdapter(driveFolderID, localOutputDir string) *useCaseDefaultFolderResolverAdapter {
	// No panic: empty driveFolderID is the production case when the
	// deployment lacks a configured voiceover_root_folder. The
	// adapter's Resolve returns ("", "", false) in that case, Execute
	// maps that to the canonical missing_folder_id short-circuit.
	// Empty localOutputDir is OK (audio stage may fail differently,
	// but missing_folder_id is no longer the failure mode).
	return &useCaseDefaultFolderResolverAdapter{
		driveFolderID:  driveFolderID,
		localOutputDir: localOutputDir,
	}
}

func (a *useCaseDefaultFolderResolverAdapter) Resolve(_ context.Context) (string, string, bool) {
	if a == nil || a.driveFolderID == "" {
		return "", "", false
	}
	return a.driveFolderID, a.localOutputDir, true
}

// Compile-time assertion (AGENTS.md Pattern 0).
var _ voiceover.VoiceoverDefaultFolderResolver = (*useCaseDefaultFolderResolverAdapter)(nil)

// timeutil import is used here for FormatRFC3339 fallback in
// InsertTx; the canonical timeutil location avoids bringing in
// time.UTC().Format() boilerplate per Adapter struct.
