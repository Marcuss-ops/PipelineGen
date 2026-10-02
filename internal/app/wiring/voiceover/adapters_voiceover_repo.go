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
// this file because UseCaseRepoAdapter is the sole structural
// conformer to that package-surface port (the persistence sub-package
// in internal/capabilities/voiceover/persistence is unimported from
// voiceover but applies the canonical Repository contract).
//
// Fail-closed: nil deps panic at construction (fail-fast per
// AGENTS.md WireUp pattern).
package voiceover

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

var _ persistence.Repository = (*UseCaseRepoAdapter)(nil)

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
// Schema source-of-truth: internal/platform/sqlite/
// assets/voiceovers_repository.go. Adding a column here without a
// SQLite migration will fail at INSERT time, NOT at compile time.
// ─────────────────────────────────────────────────────────────────────

type UseCaseRepoAdapter struct {
	repo *sqassets.VoiceoversRepository
	db   *sql.DB
	// media is the media-SSOT read surface for the two media_assets queries this
	// adapter needs (MEDIA-SSOT P2-9 Phase 2). It is deliberately separate from db
	// above: the voiceovers table is operational while media_assets is owned by
	// PostgreSQL, and reading media_assets through the operational handle meant
	// the cache lookup and the dedupe gate graded a database that holds no
	// committed media rows.
	media VoiceoverMediaReader
}

// VoiceoverMediaReader is the narrow media-SSOT read surface this adapter needs.
// Declaring it here keeps the adapter from naming an engine for the media half;
// the composition root supplies a PostgreSQL-backed implementation.
type VoiceoverMediaReader interface {
	MediaAssetLocation(ctx context.Context, assetID string) (VoiceoverMediaLocation, bool, error)
	CountByDriveFileID(ctx context.Context, driveFileID, currentID string) (matchedID string, count int, err error)
}

// VoiceoverMediaLocation mirrors the media-SSOT location projection. It is
// declared here (rather than aliased) so this package owns its own types; the
// composition root maps the platform type into it, which keeps the capability
// free of any platform import.
type VoiceoverMediaLocation struct {
	DriveFileID  string
	DriveLink    string
	DownloadLink string
	LocalPath    string
	Name         string
}

func NewUseCaseRepoAdapter(repo *sqassets.VoiceoversRepository, db *sql.DB, media VoiceoverMediaReader) *UseCaseRepoAdapter {
	if repo == nil {
		panic("app.adapters_voiceover_use_case: NewUseCaseRepoAdapter: repo is required (*sqassets.VoiceoversRepository)")
	}
	if db == nil {
		panic("app.adapters_voiceover_use_case: NewUseCaseRepoAdapter: db is required (*sql.DB, used by BeginTx in P1-2)")
	}
	if media == nil {
		panic("app.adapters_voiceover_use_case: NewUseCaseRepoAdapter: media is required (VoiceoverMediaReader; media_assets is PostgreSQL-owned)")
	}
	return &UseCaseRepoAdapter{repo: repo, db: db, media: media}
}

// BeginTx opens a new SQLite transaction on the production database.
// P1-2 (June 2026): the UseCaseRepoAdapter previously only owned
// InsertTx / DeleteByIDTx / PreReadByID. P1-2 added BeginTx so the
// voiceover Service can thread the PR-VO-A2 atomic swap tx through
// the canonical persistence.Repository port instead of holding a
// bare *sql.DB handle.
func (a *UseCaseRepoAdapter) BeginTx(ctx context.Context) (*sql.Tx, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("UseCaseRepoAdapter.BeginTx: db not wired")
	}
	return a.db.BeginTx(ctx, nil)
}

// CountByDriveFileIDTx runs the PR-VO-B3 post-upload dedupe gate
// INSIDE the caller-owned tx. Returns the matched-row id, the
// total match count, and any error.
//
// P1-2 (June 2026): the application-layer helper
// applyDedupeByDriveFileID that lived in
// internal/capabilities/voiceover/dedupe.go and consumed raw
// *sql.DB + *sql.Tx is NOT re-implemented here. The port
// method takes the tx parameter from the caller (which is
// already inside the PR-VO-A2 atomic-swap transaction) so the
// count runs against the same visibility boundary as the
// upcoming INSERT.
//
// Empty driveFileID short-circuits to (matchedID="", count=0,
// err=nil) so the Stage 3 caller can detect "no gate" via the
// empty id without a separate sentinel.
func (a *UseCaseRepoAdapter) CountByDriveFileIDTx(
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
	// MEDIA-SSOT P2-9 Phase 2: location/hash facts live in media_assets, which
	// PostgreSQL owns, so the dedupe lookup resolves from the media SSOT through
	// the narrow media port instead of querying media_assets on this tx's engine.
	//
	// The tx parameter is intentionally no longer used for the media read. The
	// retired form ran the query INSIDE the finalizer's operational transaction,
	// which bought snapshot isolation over a NON-AUTHORITATIVE copy of the table
	// and could never have extended to the authoritative copy, because
	// media_assets sits on another engine and cannot join a SQLite transaction. It
	// was therefore reading the wrong database with stronger isolation. The
	// verdict is also advisory by contract (DecideDedupe projects the count into
	// Continue/Reuse/Conflict and the caller falls through on Continue), and no
	// media_assets write happens in this transaction — the projection step is a
	// fail-closed stub on the retired legacy branch — so there is no write for the
	// read to be atomic WITH. Signature retained deliberately: it is a
	// persistence.Repository port method.
	if a == nil || a.media == nil {
		return "", 0, fmt.Errorf("CountByDriveFileIDTx: no media-SSOT reader wired (media SSOT closed)")
	}
	return a.media.CountByDriveFileID(ctx, driveFileID, currentID)
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
func (a *UseCaseRepoAdapter) toInfraRecord(rec *persistence.VoiceoverRecord) *sqassets.Record {
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
		LegacyFileMD5:   rec.LegacyFileMD5,
		Status:          rec.Status,
		Error:           rec.Error,
		Strategy:        rec.Strategy,
		Metadata:        rec.Metadata,
		DurationSeconds: rec.DurationSeconds,
		// FASE 3 (July 2026): thread the deterministic idempotency
		// key and the producing job ID through to the SQLite row.
		IdempotencyKey: rec.IdempotencyKey,
		JobID:          rec.JobID,
		Fingerprint:    rec.Fingerprint,
		CreatedAt:      parseRFC3339OrNow(rec.CreatedAt),
		UpdatedAt:      parseRFC3339OrNow(rec.UpdatedAt),
	}
}

// fromInfraRecord is the inverse of toInfraRecord — used by
// PreReadByID to surface a real (non-stub) row to the use case so
// the post-commit cleanup goroutine can capture orphan paths.
func (a *UseCaseRepoAdapter) fromInfraRecord(r *sqassets.Record) *persistence.VoiceoverRecord {
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
		TextHash:      r.TextHash,
		TextPreview:   r.TextPreview,
		Language:      r.Language,
		Voice:         r.Voice,
		Filename:      r.Filename,
		LocalPath:     r.LocalPath,
		CleanedPath:   r.CleanedPath,
		FolderID:      r.FolderID,
		FolderPath:    r.FolderPath,
		DriveFileID:   r.DriveFileID,
		DriveLink:     r.DriveLink,
		DownloadLink:  r.DownloadLink,
		LegacyFileMD5: r.LegacyFileMD5,
		DurationSeconds: r.DurationSeconds,
		Status:        r.Status,
		Error:         r.Error,
		Strategy:      r.Strategy,
		Metadata:      r.Metadata,
		// FASE 3 (July 2026): round-trip through the infra layer.
		IdempotencyKey: r.IdempotencyKey,
		JobID:          r.JobID,
		Fingerprint:    r.Fingerprint,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
}

// FindByFingerprint is intentionally an optional read surface: the
// application Repository port remains stable for test doubles and legacy
// callers, while the production adapter exposes the SQLite projection for
// cross-run cache auditing.
func (a *UseCaseRepoAdapter) FindByFingerprint(ctx context.Context, fingerprint string) (*persistence.VoiceoverRecord, error) {
	if a == nil || a.repo == nil {
		return nil, fmt.Errorf("UseCaseRepoAdapter.FindByFingerprint: repository not wired")
	}
	rec, err := a.repo.FindByFingerprint(ctx, fingerprint)
	if err != nil || rec == nil {
		return nil, err
	}
	return a.fromInfraRecord(rec), nil
}

// voiceoverMediaAssetLocation is the subset of media_assets columns
// needed to hydrate a VoiceoverCacheHit after a fingerprint match.
// PR-VO-ASSET-ID (August 2026): after migration 232 dropped location
// columns from the voiceovers table, the canonical Drive and local
// path facts live in media_assets (same id = voiceover id).
type voiceoverMediaAssetLocation struct {
	DriveFileID  string
	DriveLink    string
	DownloadLink string
	LocalPath    string
	Name         string // media_assets.name → VoiceoverCacheHit.Filename
}

// findVoiceoverMediaAsset queries media_assets for the location columns
// needed to build a valid VoiceoverCacheHit. Returns nil when the
// media_assets row doesn't exist (legacy rows without the projection).
// findVoiceoverMediaAsset queries the media SSOT for the location columns needed
// to build a valid VoiceoverCacheHit. Returns nil when the media_assets row does
// not exist (legacy rows without the projection), which the cache lookup treats
// as a miss.
//
// MEDIA-SSOT P2-9 Phase 2: this used to run on a.db — the operational handle —
// so a committed voiceover's location could look absent and a real cache hit
// degraded into a full regeneration. The media fact now resolves from the media
// SSOT through the media port.
func (a *UseCaseRepoAdapter) findVoiceoverMediaAsset(ctx context.Context, assetID string) (*voiceoverMediaAssetLocation, error) {
	if a == nil || a.media == nil {
		return nil, nil
	}
	loc, found, err := a.media.MediaAssetLocation(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &voiceoverMediaAssetLocation{
		DriveFileID:  loc.DriveFileID,
		DriveLink:    loc.DriveLink,
		DownloadLink: loc.DownloadLink,
		LocalPath:    loc.LocalPath,
		Name:         loc.Name,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────
// VoiceoverCacheLookup adapter (cross-run voiceover cache, August 2026)
// ─────────────────────────────────────────────────────────────────────

// VoiceoverCacheAdapter implements voiceover.VoiceoverCacheLookup by
// wrapping the existing FindByFingerprint on the SQLite repository.
// It is the canonical production adapter for the cross-run voiceover
// cache — on a fingerprint hit, it verifies the row is reusable
// (completed/uploaded/generated status with a non-empty DriveFileID)
// and, when timing is required, checks that the metadata column
// carries a timing_json_link so the cached result includes the timing
// bundle references.
type VoiceoverCacheAdapter struct {
	repo         *UseCaseRepoAdapter
	log          *zap.Logger
	timingReader drive.Reader
}

var _ voiceover.VoiceoverCacheLookup = (*VoiceoverCacheAdapter)(nil)

func NewVoiceoverCacheAdapter(repo *UseCaseRepoAdapter, log *zap.Logger) *VoiceoverCacheAdapter {
	if repo == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &VoiceoverCacheAdapter{repo: repo, log: log}
}

// SetTimingArtifactReader installs the Drive read port used to hydrate the
// canonical per-word artifact on timing-bearing cache hits. Without it, lookup
// remains fail-closed and returns a miss for requests that require timing.
func (a *VoiceoverCacheAdapter) SetTimingArtifactReader(reader drive.Reader) {
	if a != nil {
		a.timingReader = reader
	}
}

// Lookup checks the voiceovers table for an existing row with the same
// content fingerprint. A cache HIT requires:
//
//  1. A row exists with the exact fingerprint
//  2. The row status is reusable (completed | uploaded | generated)
//  3. A media_assets row exists with a non-empty DriveFileID
//     (PR-VO-ASSET-ID: after migration 232, location columns were
//     dropped from voiceovers — the canonical source is media_assets)
//  4. When timingRequired is true, the metadata column carries
//     timing_json_link (the timing bundle was published)
//
// Any failure — missing row, non-reusable status, missing DriveFileID,
// missing timing links when required — returns (nil, nil) so the caller
// falls through to the full pipeline. Lookup errors (DB unavailable)
// return the error so the caller can decide whether to fail or retry.
func (a *VoiceoverCacheAdapter) Lookup(ctx context.Context, fingerprint string, timingRequired bool) (*voiceover.VoiceoverCacheHit, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}

	rec, err := a.repo.FindByFingerprint(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}

	// Check reusable status.
	if !voiceover.IsReusableStatus(voiceover.Status(rec.Status)) {
		a.log.Debug("voiceover cache: fingerprint match but status not reusable",
			zap.String("fingerprint", fingerprint),
			zap.String("status", rec.Status),
			zap.String("id", rec.ID))
		return nil, nil
	}
	durationMs := int64(rec.DurationSeconds * 1000)
	if durationMs <= 0 {
		// Older or interrupted TTS runs can leave a reusable status with a
		// zero-duration projection. Returning that row makes script generation
		// fail after a cache hit (and retry the same bad row forever); treat it
		// as a miss so synthesis can replace the incomplete projection.
		a.log.Warn("voiceover cache: ignoring reusable row with non-positive duration",
			zap.String("fingerprint", fingerprint),
			zap.String("id", rec.ID),
			zap.String("status", rec.Status),
			zap.Float64("duration_seconds", rec.DurationSeconds))
		return nil, nil
	}

	// PR-VO-ASSET-ID (August 2026): after migration 232 dropped location
	// columns from voiceovers, the canonical Drive and local-path facts
	// live in media_assets (same id). Query media_assets for the location
	// data — a missing row means the asset projection was never written
	// (legacy pre-migration row), so this is a cache MISS.
	loc, locErr := a.repo.findVoiceoverMediaAsset(ctx, rec.ID)
	if locErr != nil {
		a.log.Warn("voiceover cache: media_assets lookup error",
			zap.String("fingerprint", fingerprint),
			zap.String("id", rec.ID),
			zap.Error(locErr))
		return nil, locErr
	}

	// DriveFileID must be non-empty — the audio was uploaded.
	if loc == nil || loc.DriveFileID == "" {
		a.log.Debug("voiceover cache: fingerprint match but media_assets DriveFileID empty or missing",
			zap.String("fingerprint", fingerprint),
			zap.String("id", rec.ID))
		return nil, nil
	}

	var meta map[string]any
	if timingRequired {
		if err := json.Unmarshal([]byte(rec.Metadata), &meta); err != nil || meta["timing_json_link"] == nil || meta["timing_json_link"] == "" {
			a.log.Debug("voiceover cache: fingerprint match but timing not hydrated",
				zap.String("fingerprint", fingerprint),
				zap.String("id", rec.ID),
				zap.Bool("meta_parse_ok", err == nil))
			return nil, nil
		}
	}
	var timingArtifact *audio.SpeechTimingArtifact
	if timingRequired {
		artifact, loadErr := a.loadTimingArtifact(ctx, rec, meta)
		if loadErr != nil {
			a.log.Warn("voiceover cache: failed to hydrate timing artifact; falling through to synthesis",
				zap.String("fingerprint", fingerprint), zap.String("id", rec.ID), zap.Error(loadErr))
			return nil, loadErr
		}
		if artifact == nil {
			return nil, nil
		}
		timingArtifact = artifact
	}

	// Extract cleaned_path from voiceovers metadata (not in media_assets).
	cleanedPath := loc.LocalPath
	if rec.Metadata != "" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(rec.Metadata), &meta); err == nil {
			if cp, ok := meta["cleaned_path"].(string); ok && cp != "" {
				cleanedPath = cp
			}
		}
	}

	filename := loc.Name
	if filename == "" {
		// Fall back to voiceovers.filename column for pre-migration rows
		// that have the column still populated.
		filename = rec.Filename
	}

	a.log.Debug("voiceover cache HIT",
		zap.String("fingerprint", fingerprint),
		zap.String("id", rec.ID),
		zap.String("drive_file_id", loc.DriveFileID),
		zap.String("status", rec.Status),
		zap.Int64("duration_ms", durationMs))

	return &voiceover.VoiceoverCacheHit{
		ID:            rec.ID,
		Voice:         rec.Voice,
		Filename:      filename,
		DriveFileID:   loc.DriveFileID,
		DriveLink:     loc.DriveLink,
		DownloadLink:  loc.DownloadLink,
		LocalPath:     loc.LocalPath,
		CleanedPath:   cleanedPath,
		DurationMs:    durationMs,
		LegacyFileMD5: rec.LegacyFileMD5,
		MetaJSON:      []byte(rec.Metadata),
		Artifact:      timingArtifact,
	}, nil
}

func (a *VoiceoverCacheAdapter) loadTimingArtifact(ctx context.Context, rec *persistence.VoiceoverRecord, meta map[string]any) (*audio.SpeechTimingArtifact, error) {
	if a == nil || a.timingReader == nil || rec == nil {
		return nil, nil
	}
	link, _ := meta["timing_json_link"].(string)
	fileID, err := driveFileIDFromLink(link)
	if err != nil {
		return nil, err
	}
	reader, _, err := a.timingReader.DownloadFile(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("download timing artifact %s: %w", fileID, err)
	}
	defer reader.Close()
	var artifact audio.SpeechTimingArtifact
	if err := json.NewDecoder(io.LimitReader(reader, 8<<20)).Decode(&artifact); err != nil {
		return nil, fmt.Errorf("decode timing artifact %s: %w", fileID, err)
	}
	if err := artifact.Validate(); err != nil {
		a.log.Warn("voiceover cache: cached timing artifact failed validation", zap.String("id", rec.ID), zap.Error(err))
		return nil, nil
	}
	if artifact.TextSHA256 != strings.TrimSpace(rec.TextHash) ||
		artifact.Language != strings.TrimSpace(rec.Language) || artifact.Voice != strings.TrimSpace(rec.Voice) {
		a.log.Warn("voiceover cache: cached timing artifact identity mismatch", zap.String("id", rec.ID))
		return nil, nil
	}
	if audioSHA, _ := meta["audio_sha256"].(string); audioSHA == "" || artifact.AudioSHA256 != audioSHA {
		a.log.Warn("voiceover cache: cached timing artifact audio hash mismatch", zap.String("id", rec.ID))
		return nil, nil
	}
	if durationUS, ok := meta["timing_duration_us"].(float64); !ok || int64(durationUS) != artifact.DurationUS {
		a.log.Warn("voiceover cache: cached timing artifact duration mismatch", zap.String("id", rec.ID))
		return nil, nil
	}
	if wordCount, ok := meta["timing_word_count"].(float64); !ok || int(wordCount) != len(artifact.Words) {
		a.log.Warn("voiceover cache: cached timing artifact word-count mismatch", zap.String("id", rec.ID))
		return nil, nil
	}
	return &artifact, nil
}

func driveFileIDFromLink(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse timing artifact link: %w", err)
	}
	if id := strings.TrimSpace(parsed.Query().Get("id")); id != "" {
		return id, nil
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "d" && strings.TrimSpace(parts[i+1]) != "" {
			return strings.TrimSpace(parts[i+1]), nil
		}
	}
	return "", fmt.Errorf("timing artifact link has no Drive file ID")
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
