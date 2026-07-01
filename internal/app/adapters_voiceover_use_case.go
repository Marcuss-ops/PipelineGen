// Package app — voiceover use case adapters (P0.1, June 2026).
//
// Bridges production concretes under internal/infrastructure/* to the
// 7 canonical narrow ports declared in
// internal/application/voiceover/ports.go. Per AGENTS.md Pattern 0
// (port abstraction layer, June 2026) each adapter is a thin
// bridge; production wiring lives here, NOT inside the voiceover
// package, so voiceover stays free of *infrastructure and *lifecycle
// imports.
//
// Adapters populate:
//
//	TTSProvider                   ← *audioasset.Processor
//	AudioPostProcessor            ← pkg-level ffmpeg.RemoveSilence closure
//	AssetLifecycle                ← *lifecycle.Service (ProcessAsset adapter)
//	VoiceoverRepository           ← direct DB adapter (tx.ExecContext for
//	                                 InsertTx / DeleteByIDTx; PreReadByID stub;
//	                                 column schema mirrors the canonical
//	                                 VoiceoversRepository.Upsert)
//	DestinationResolver           ← *asset.Resolver (forward Group + StyleGroup,
//	                                 mirror StyleGroup verbatim back)
//	VoiceoverDefaultFolderResolver ← cfg.Drive.VoiceoverFolder() (resolved at
//	                                 composition time; PR 6 P0.2 fallback for
//	                                 cmd.Destination == nil)
//
// Two remaining ports (TransactionalOutbox, DB) are passed directly
// from composition:
//
//	*outbox.Dispatcher         struct-satisfies voiceover.TxOutboxEnqueuer
//	                             (= voiceover.TransactionalOutbox) — the
//	                             legacy Service already relies on this
//	                             structural conformance (see
//	                             internal/infrastructure/database/sqlite/
//	                             outbox/repository.go:418).
//	*sql.DB                    injection direct, no adapter.
//
// Compile-time assertions follow the canonical structural-conformance
// convention: each adapter struct on the right side declares
// `var _ voiceover.<Port> = (*<AdapterStruct>)(nil)` so drift between
// the adapter signature and the port contract surfaces as a compile
// error here, NOT at the use case Execute call site.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

var _ persistence.Repository = (*useCaseRepoAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// TTSProvider adapter.
//
// Bridges *audioasset.Processor → voiceover.TTSProvider. The use case
// only ever supplies well-formed inputs (no path-traversal payloads
// past cmd.Validate), so the lower-level AudioInput fields EscapeHook
// semantics — UseStdin defaults to false. AudioResult carries
// LocalPath + CleanedPath + Voice + FileHash which map 1-a-1 to
// TTSOutput.
// ─────────────────────────────────────────────────────────────────────

type useCaseTTSAdapter struct {
	proc *audioasset.Processor
}

func newUseCaseTTSAdapter(proc *audioasset.Processor) *useCaseTTSAdapter {
	if proc == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseTTSAdapter: proc is required (*audioasset.Processor)")
	}
	return &useCaseTTSAdapter{proc: proc}
}

func (a *useCaseTTSAdapter) Synthesize(ctx context.Context, in voiceover.TTSInput) (voiceover.TTSOutput, error) {
	res, err := a.proc.Generate(ctx, &audioasset.AudioInput{
		Text:          in.Text,
		Language:      in.Language,
		Voice:         in.Voice,
		Filename:      in.Filename,
		OutputDir:     in.OutputDir,
		RemoveSilence: in.RemoveSilence,
		// UseStdin defaults to false: the use case path delivers
		// bounded, validated text inputs (POST /generate path →
		// command.Validate path-traversal rejection before field
		// access, mirrors TestGenerateBatch_RejectsPathTraversalPayload).
	})
	if err != nil {
		return voiceover.TTSOutput{}, err
	}
	return voiceover.TTSOutput{
		LocalPath:   res.LocalPath,
		CleanedPath: res.CleanedPath,
		Voice:       res.Voice,
		FileHash:    res.FileHash,
	}, nil
}

var _ voiceover.TTSProvider = (*useCaseTTSAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// AudioPostProcessor adapter.
//
// Implements voiceover.AudioPostProcessor.Process by wrapping the
// package-level ffmpeg.RemoveSilence closure. The cleaned-path
// convention is deterministic: <OutputDir>/cleaned_<Filename> so
// filename uniqueness rules (P1.3) keep the call surface predictable.
// Nil-safe at the use case boundary (only invoked when
// cmd.RemoveSilence == true).
// ─────────────────────────────────────────────────────────────────────

type useCaseAudioAdapter struct {
	log *zap.Logger
}

func newUseCaseAudioAdapter(log *zap.Logger) *useCaseAudioAdapter {
	return &useCaseAudioAdapter{log: log}
}

func (a *useCaseAudioAdapter) Process(ctx context.Context, in voiceover.AudioPostInput) (voiceover.AudioPostOutput, error) {
	if in.LocalPath == "" {
		return voiceover.AudioPostOutput{}, fmt.Errorf("voiceover.audio_post: empty LocalPath (use case passed a missing local path)")
	}
	if in.OutputDir == "" || in.Filename == "" {
		return voiceover.AudioPostOutput{}, fmt.Errorf("voiceover.audio_post: empty OutputDir/Filename (use case contract violation)")
	}
	// clean file goes to <OutputDir>/cleaned_<basename>; matches the
	// canonical convention used by audioasset.Processor (processor.go:103).
	cleaned := in.OutputDir + "/cleaned_" + in.Filename
	if a.log != nil {
		a.log.Info("voiceover.audio_post: stripping silence",
			zap.String("input", in.LocalPath),
			zap.String("output_dir", in.OutputDir),
			zap.String("filename", in.Filename))
	}
	if err := ffmpeg.RemoveSilence(ctx, "", in.LocalPath, cleaned); err != nil {
		if a.log != nil {
			a.log.Warn("voiceover.audio_post: RemoveSilence failed (caller decides whether to fail-fast)",
				zap.String("input", in.LocalPath),
				zap.String("output", cleaned),
				zap.Error(err))
		}
		return voiceover.AudioPostOutput{}, err
	}
	return voiceover.AudioPostOutput{CleanedPath: cleaned}, nil
}

var _ voiceover.AudioPostProcessor = (*useCaseAudioAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// VoiceoverPublisher adapter (E1 cutover, June 2026).
//
// Bridges *drive.Uploader (which satisfies drive.Admin via the
// compile-time assertion in internal/infrastructure/drive/ports.go)
// → voiceover.VoiceoverPublisher.Publish. The upload-only port
// replaces the pre-E1 voiceover.AssetLifecycle.Upload (which
// delegated to lifecycle.Service.ProcessAsset and bundled Drive
// upload + dedupe + asset-record persistence). The new Publisher
// is upload-only:
//   - no SQLite write (process_voiceover_item.go::Execute owns
//     the canonical row INSERT inside its atomic-swap tx),
//   - no dedupe gate (Executor paths already pre-resolve the
//     canonical voiceover ID via buildVoiceoverID),
//   - no asset-record projection (media_assets projection is
//     written by lifecycle.Service.UpsertVoiceoverProjectionTx
//     inside the same tx).
//
// Publish returns the canonical Drive file ID; downstream callers
// (process_voiceover_item.go::Execute + usecase.go::processOneLanguage)
// reconstruct DriveLink + DownloadLink via the canonical
// CanonicalDriveWebURL / CanonicalDriveDownloadURL helpers in
// voiceover/ports.go.
//
// Fail-closed: nil admin panics at construction (fail-fast per
// AGENTS.md WireUp pattern). The UploadFile retry policy itself
// is owned by the production drive.Uploader (3-attempt exponential
// backoff via pkg/retry; see internal/infrastructure/drive/uploader.go).
// ─────────────────────────────────────────────────────────────────────

type useCasePublisherAdapter struct {
	admin drive.Admin
}

func newUseCasePublisherAdapter(admin drive.Admin) *useCasePublisherAdapter {
	if admin == nil {
		panic("app.adapters_voiceover_use_case: newUseCasePublisherAdapter: admin is required (*drive.Uploader implementing drive.Admin)")
	}
	return &useCasePublisherAdapter{admin: admin}
}

func (a *useCasePublisherAdapter) Publish(ctx context.Context, cmd voiceover.VoiceoverPublishCommand) (string, error) {
	if cmd.LocalPath == "" {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: empty LocalPath (use case supplied no local payload)")
	}
	if cmd.FolderID == "" {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: empty FolderID (use case supplied no destination folder)")
	}
	if cmd.Filename == "" {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: empty Filename (use case supplied no display name)")
	}
	res, err := a.admin.UploadFile(ctx, cmd.LocalPath, cmd.FolderID, cmd.Filename)
	if err != nil {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: drive.UploadFile: %w", err)
	}
	if res == nil {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: drive.UploadFile returned nil UploadResult")
	}
	return res.FileID, nil
}

var _ voiceover.VoiceoverPublisher = (*useCasePublisherAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// VoiceoverRepository adapter.
//
// Implements voiceover.VoiceoverRepository.InsertTx + DeleteByIDTx via
// tx.ExecContext on the canonical voiceovers table schema (mirrors
// the column set in *assets.VoiceoversRepository.Upsert to avoid
// schema drift). PreReadByID is a no-op stub: the use case at
// usecase.go:188 explicitly ignores the return via `_, _ = ...`, so a
// zero-value response is consistent with the documented behavior.
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
		ID:              rec.ID,
		RequestID:       rec.RequestID,
		TextHash:        rec.TextHash,
		TextPreview:     rec.TextPreview,
		Language:        rec.Language,
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
		ID:           r.ID,
		RequestID:    r.RequestID,
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
		StyleGroup:      dest.StyleGroup,
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
		StyleGroup:    dest.StyleGroup,     // MIRROR verbatim (NOT from resolver result).
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

// ─────────────────────────────────────────────────────────────────────
// voiceoverDriveAdapter - Drive port adapter for voiceover (moved from
// voiceover_adapters_drive.go, Phase 5 consolidation, June 2026).
// Wraps drive.Admin to satisfy voiceover.DriveUploaderPort.
// ─────────────────────────────────────────────────────────────────────

type voiceoverDriveAdapter struct {
	drive drive.Admin
}

var _ voiceover.DriveUploaderPort = (*voiceoverDriveAdapter)(nil)

func newVoiceoverDriveAdapter(admin drive.Admin) voiceover.DriveUploaderPort {
	if admin == nil {
		return nil
	}
	return &voiceoverDriveAdapter{drive: admin}
}

func (a *voiceoverDriveAdapter) DeleteFile(ctx context.Context, fileID string) error {
	if fileID == "" {
		return fmt.Errorf("voiceoverDriveAdapter.DeleteFile: fileID is required")
	}
	if a == nil || a.drive == nil {
		return fmt.Errorf("voiceoverDriveAdapter: drive not wired")
	}
	return a.drive.DeleteFile(ctx, fileID)
}

// timeutil import is used here for FormatRFC3339 fallback in
// InsertTx; the canonical timeutil location avoids bringing in
// time.UTC().Format() boilerplate per Adapter struct.

// ─────────────────────────────────────────────────────────────────────
// LifecycleProjectionUpserter adapter (P0.4 Fase 3a, July 2026).
//
// Bridges *lifecycle.Service → voiceover.LifecycleProjectionUpserter.
// The two VoiceoverProjectionInput types (voiceover.VoiceoverProjectionInput
// and lifecycle.VoiceoverProjectionInput) have identical field sets but
// are separate types by design (domain separation — godlike/06 §one-
// owner-per-fact). The adapter translates between them so the
// voiceover.Finalizer stays free of any lifecycle package import.
// ─────────────────────────────────────────────────────────────────────

type voiceoverProjectionAdapter struct {
	svc *lifecycle.Service
}

func newVoiceoverProjectionAdapter(svc *lifecycle.Service) *voiceoverProjectionAdapter {
	if svc == nil {
		panic("app.adapters_voiceover_use_case: newVoiceoverProjectionAdapter: svc is required (*lifecycle.Service)")
	}
	return &voiceoverProjectionAdapter{svc: svc}
}

func (a *voiceoverProjectionAdapter) UpsertVoiceoverProjectionTx(ctx context.Context, tx *sql.Tx, in *voiceover.VoiceoverProjectionInput) error {
	return a.svc.UpsertVoiceoverProjectionTx(ctx, tx, &lifecycle.VoiceoverProjectionInput{
		ID:           in.ID,
		Source:       in.Source,
		Name:         in.Name,
		Filename:     in.Filename,
		FolderID:     in.FolderID,
		FolderPath:   in.FolderPath,
		MediaType:    in.MediaType,
		LocalPath:    in.LocalPath,
		DriveFileID:  in.DriveFileID,
		DriveLink:    in.DriveLink,
		DownloadLink: in.DownloadLink,
		FileHash:     in.FileHash,
		Language:     in.Language,
		Status:       in.Status,
		Metadata:     in.Metadata,
	})
}

var _ voiceover.LifecycleProjectionUpserter = (*voiceoverProjectionAdapter)(nil)
