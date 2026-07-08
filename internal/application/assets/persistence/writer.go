// Package persistence — AssetPersistenceWriter (PR-CANONICAL-ASSET-WRITER, July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE canonical owner of the AssetPersistenceWriter port — the unified
// application-layer write surface for persisting a media asset row
// (media_assets) and emitting an indexing outbox event (outbox_events)
// in a single atomic transaction.
//
// Problem this solves:
//
//	YouTube has ClipAtomicWriterAdapter.CommitClipAndIndexEvent
//	(5-step atomic TX in clip_atomic_writer.go) and Stock has
//	AssetTxFinalizer.FinalizeAsset (3-step caller-owned TX in
//	asset_finalizer_tx.go). Both write media_assets + outbox_events
//	in a single transaction with nearly identical contracts.
//
// Design (godlike/06 SSOT):
//
//   - AssetPersistenceWriter is the narrow port: one method,
//     PersistAndIndex, that takes a caller-owned Transaction +
//     PersistAndIndexRequest and returns PersistAndIndexResult.
//   - The port accepts a Transaction (Stock pattern — caller-owned TX)
//     because it's more composable: the JobFinalizer orchestrates
//     multiple finalization steps in one TX. YouTube wraps it with
//     own-TX semantics via a thin adapter.
//   - The concrete implementation lives in the infrastructure layer
//     (internal/infrastructure/database/sqlite/assets/persistence_writer.go).
//   - YouTube's ClipAtomicWriterAdapter delegates to this port
//     after opening its own TX.
//   - Stock's AssetTxFinalizer delegates to this port inside the
//     caller-owned TX (the FinalizeAsset method calls PersistAndIndex
//     for the media_assets + outbox_events portion).
//
// godlike/07 minimum-blast-radius: additive-only. Existing per-pipeline
// write surfaces continue working; callers that adopt the unified port
// deprecate their local derivation over time.
package persistence

import (
	"context"
	"errors"
	"time"
)

// ── Transaction surface ──────────────────────────────────────────────

// Transaction is a narrow write-only surface that the
// AssetPersistenceWriter consumes to participate in the caller's
// transaction. It mirrors finalization.Transaction exactly (structural
// typing — same methods, same semantics) but lives at the application
// layer so the persistence package doesn't import the domain/finalization
// package (clean architecture: application layers don't import other
// application layers' domain types unless necessary).
//
// Production concrete: *sql.Tx (via a thin adapter in the infrastructure
// layer). Tests inject hand-rolled fakes.
type Transaction interface {
	// ExecContext executes a query that does not return rows.
	ExecContext(ctx context.Context, query string, args ...any) (Result, error)

	// QueryRowContext executes a query that returns at most one row.
	QueryRowContext(ctx context.Context, query string, args ...any) Row
}

// Result is the narrow result surface from Transaction.ExecContext.
// Mirrors sql.Result; satisfied structurally by sql.Result.
type Result interface {
	LastInsertId() (int64, error)
	RowsAffected() (int64, error)
}

// Row is the narrow result surface from Transaction.QueryRowContext.
// Mirrors *sql.Row; satisfied structurally by *sql.Row.
type Row interface {
	Scan(dest ...any) error
}

// ── Request ──────────────────────────────────────────────────────────

// PersistAndIndexRequest carries all the data needed to write a
// media_assets row and emit an asset.index.requested outbox event
// in a single atomic transaction.
//
// This struct unifies the YouTube ClipAsset + IndexEventPayload and
// the Stock PublishedArtifact shapes into a single canonical input.
//
// Field mapping:
//
//	YouTube ClipAsset fields → AssetID, Source="youtube", Name, Filename,
//	  MediaType="video", DriveFileID, DriveLink, LocalPath, ContentHash,
//	  FolderID, FolderPath, SearchText, LifecycleState="ACTIVE"
//	YouTube IndexEventPayload → AggregateID, EventCreatedAt
//
//	Stock PublishedArtifact fields → AssetID=ArtifactID, Source,
//	  Name=Filename, Filename, MediaType, ContentHash=SHA256,
//	  DriveFileID=Location.FileID, DriveLink=Location.WebViewLink,
//	  FolderID=Location.FolderID, FolderPath=Location.FolderPath,
//	  Description, MetadataJSON (from ArtifactMetadata merge),
//	  LifecycleState="PUBLISHED", IndexState="INDEXING_PENDING"
type PersistAndIndexRequest struct {
	// ── Identity ──────────────────────────────────────────────────────

	// AssetID is the canonical media_assets.id primary key.
	// YouTube: yt_{videoID}_{start}_{end}_{policy}
	// Stock: ArtifactID (= planner:{hash}:{index})
	AssetID string

	// Source is the content source ("youtube", "stock", "artlist", etc.).
	// Written to media_assets.source.
	Source string

	// ── Content ───────────────────────────────────────────────────────

	// Name is the human-readable display name for the asset.
	// YouTube: derived from ClipMetadataInput.Summary or clipID.
	// Stock: PublishedArtifact.Filename.
	Name string

	// Filename is the canonical filename (e.g. "round-7.mp4").
	// YouTube: filepath.Base(LocalPath) or clipID+".mp4".
	// Stock: PublishedArtifact.Filename.
	Filename string

	// MediaType is the IANA content type classification.
	// YouTube: "video"; Stock: kindToMediaType(Kind).
	MediaType string

	// ContentHash is the SHA-256 fingerprint of the asset's content.
	// YouTube: asset.FileHash (MD5 of local clip file). Written to
	//   media_assets.source_version column (the supersede gate reads
	//   from this column via SourceVersionFor).
	// Stock: PublishedArtifact.SHA256. Written to
	//   media_assets.metadata_json.content_hash (the supersede gate
	//   reads from metadata_json Tier 2).
	// The concrete adapter decides which column to populate based on
	// the Source field — YouTube writes source_version; Stock writes
	// metadata_json content_hash.
	//
	// godlike/06 SSOT: the IndexingHandler supersede gate is the
	// canonical reader; the concrete adapter is the canonical writer.
	ContentHash string

	// Description is the human-readable English summary for the clip.
	// YouTube: empty (metadata enrichment writes it later via Commit 4).
	// Stock: PublishedArtifact.Description.
	Description string

	// ── Location ──────────────────────────────────────────────────────

	// DriveFileID is the Google Drive file ID.
	// YouTube: asset.Drive.FileID; Stock: Location.FileID.
	DriveFileID string

	// DriveLink is the Google Drive web-view link.
	// YouTube: asset.Drive.WebViewLink; Stock: Location.WebViewLink.
	DriveLink string

	// DownloadLink is the direct download URL (if available).
	// YouTube: empty (derived from FileID); Stock: Location.DownloadLink.
	DownloadLink string

	// LocalPath is the local filesystem path to the asset.
	// YouTube: asset.LocalPath; Stock: empty (remote-only).
	LocalPath string

	// FolderID is the Google Drive folder ID containing this asset.
	// YouTube: asset.Drive.FolderID; Stock: Location.FolderID.
	FolderID string

	// FolderPath is the human-readable Drive folder path.
	// YouTube: asset.Drive.FolderPath; Stock: Location.FolderPath.
	FolderPath string

	// ── State ─────────────────────────────────────────────────────────

	// LifecycleState is the lifecycle state to set on the asset row.
	// YouTube: "ACTIVE"; Stock: "PUBLISHED".
	// Callers MUST set this explicitly — there is no default.
	LifecycleState string

	// IndexState is the index state to set on the asset row.
	// YouTube: derived from LifecycleState; Stock: "INDEXING_PENDING".
	// When empty, the concrete adapter omits it from the INSERT
	// (YouTube path — the column has a DEFAULT).
	IndexState string

	// ── Search ────────────────────────────────────────────────────────

	// SearchText is the BM25 search text for Qdrant indexing.
	// YouTube: composeYouTubeClipSearchText result; Stock: empty.
	SearchText string

	// ── Metadata ──────────────────────────────────────────────────────

	// MetadataJSON is the raw JSON to write to media_assets.metadata_json.
	// YouTube: empty (metadata enrichment writes it later via Commit 4).
	// Stock: merged ArtifactMetadata + content_hash + publish_action.
	// When nil/empty, the column defaults to '{}'.
	MetadataJSON []byte

	// ── Outbox ────────────────────────────────────────────────────────

	// EventCreatedAt is the timestamp for the outbox event.
	// YouTube: IndexEventPayload.CreatedAt; Stock: time.Now().
	// When zero (time.Time{}.IsZero() == true), the concrete adapter
	// uses time.Now().
	EventCreatedAt time.Time

	// ── Extra ─────────────────────────────────────────────────────────

	// Extra is a free-form map for source-specific fields that don't
	// fit the canonical shape. The concrete adapter merges these into
	// the media_assets INSERT columns or metadata_json as appropriate.
	//
	// YouTube: nil (all fields are in the typed columns above).
	//
	// Stock convention (keys read by the concrete adapter):
	//   "title"           (string) — clip title
	//   "description"     (string) — clip description
	//   "round"           (int)    — boxing-style round number
	//   "event"           (string) — event label
	//   "subject"         (string) — content subject
	//   "tags"            ([]string) — clip tags
	//   "category"        (string) — content category
	//   "source_provider" (string) — e.g. "pexels", "pixabay"
	//   "source_video_id" (string) — provider-native video ID
	//   "drive_path"      (string) — Drive web-view link
	//   "indexing_status" (string) — lifecycle state hint
	//   "start_sec"       (float64) — clip start timestamp
	//   "end_sec"         (float64) — clip end timestamp
	//   "slug"            (string) — Drive folder slug
	//
	// The concrete adapter MUST document which keys it reads.
	Extra map[string]any
}

// ── Result ───────────────────────────────────────────────────────────

// PersistAndIndexResult carries the output of a PersistAndIndex call.
//
// The caller is responsible for inserting the returned OutboxEvents
// into the outbox table (inside the same TX or a separate TX depending
// on the orchestration pattern).
type PersistAndIndexResult struct {
	// EventKey is the deterministic outbox event_key for the
	// asset.index.requested event. The caller uses this to insert
	// the outbox event or to detect terminal conflicts.
	EventKey string

	// PayloadJSON is the canonical v1 envelope JSON for the outbox
	// event. The caller inserts this into outbox_events alongside
	// the EventKey.
	PayloadJSON []byte

	// RowsAffected is the number of rows affected by the media_assets
	// UPSERT. 1 = insert, 2 = update-on-conflict, 0 = no-op (SQLite
	// skip-write optimization for byte-identical values).
	RowsAffected int64
}

// ── Port ─────────────────────────────────────────────────────────────

// AssetPersistenceWriter is the canonical application-layer port for
// writing a media asset row and emitting an indexing outbox event in
// a single atomic transaction.
//
// It unifies YouTube's ClipAtomicWriterAdapter.CommitClipAndIndexEvent
// and Stock's AssetTxFinalizer.FinalizeAsset (media_assets + outbox
// portions only — Stock's asset_versions and asset_locations writes
// remain in AssetTxFinalizer).
//
// The method accepts a caller-owned Transaction (Stock pattern — the
// JobFinalizer orchestrates multiple finalization steps in one TX).
// YouTube wraps this with own-TX semantics via a thin adapter that
// calls Begin, PersistAndIndex, EnqueueOutbox, Commit.
//
// godlike/06 SSOT: this port is the SOLE canonical owner of the
// media_assets + outbox_events write contract. Concrete adapters in
// the infrastructure layer implement it; the composition root wires
// them.
//
// godlike/07 NO-FAKE-AVAILABILITY: the port does NOT swallow errors.
// Every SQL error propagates to the caller. The caller decides whether
// to retry, dead-letter, or surface the error.
type AssetPersistenceWriter interface {
	// PersistAndIndex writes the media_assets row and returns the
	// outbox event envelope (EventKey + PayloadJSON) for the caller
	// to insert into outbox_events inside the same transaction.
	//
	// The caller MUST insert the outbox event after PersistAndIndex
	// returns successfully — the event is NOT inserted by this method.
	// This separation allows the caller to batch multiple
	// PersistAndIndex calls before inserting all outbox events at
	// once (Stock multi-chunk pattern).
	//
	// Returns PersistAndIndexResult with the event envelope.
	// Returns error on SQL failure, missing required fields, or
	// terminal outbox conflict (ErrOutboxTerminalConflict).
	PersistAndIndex(
		ctx context.Context,
		tx Transaction,
		req PersistAndIndexRequest,
	) (PersistAndIndexResult, error)
}

// ── Typed sentinels ──────────────────────────────────────────────────

// ErrAssetPersistenceNotWired signals that the concrete adapter was
// not wired at composition time. Callers should return HTTP 503 or
// log a fatal at boot time.
var ErrAssetPersistenceNotWired = errors.New("asset persistence: writer not wired (composition must inject concrete)")

// ErrAssetPersistenceMissingAssetID signals that the request's
// AssetID is empty. The media_assets.id primary key is NOT NULL.
var ErrAssetPersistenceMissingAssetID = errors.New("asset persistence: AssetID is required")

// ErrAssetPersistenceMissingSource signals that the request's
// Source is empty. The media_assets.source column must identify
// the content origin.
var ErrAssetPersistenceMissingSource = errors.New("asset persistence: Source is required")

// ErrAssetPersistenceMissingContentHash signals that the request's
// ContentHash is empty. The supersede gate in IndexingHandler requires
// a non-empty source_version to function.
var ErrAssetPersistenceMissingContentHash = errors.New("asset persistence: ContentHash is required (supersede gate needs a fingerprint)")

// ErrAssetPersistenceNilTx signals that the caller passed a nil
// transaction. The adapter cannot write without a valid TX.
var ErrAssetPersistenceNilTx = errors.New("asset persistence: Transaction is nil")
