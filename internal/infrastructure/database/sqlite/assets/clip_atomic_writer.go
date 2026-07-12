// Package assets — clip_atomic_writer.go: ClipAtomicWriterAdapter
// concrete implementation of the youtubeports.ClipAtomicWriter typed
// interface.
//
// Commit 1/6 (PR-C-YouTube-Cutover, June 2026): the canonical
// ProcessYouTubeSegmentUseCase (internal/application/youtube/usecase/
// process_segment.go) calls the atomic writer at Step 9 as its
// terminal step. Before Commit 1 the adapter was unbound — the use
// case's nil-port path silently produced `out.Status = "processed"`
// even when DB + outbox writes were skipped (P0 #3 in the compliance
// verdict).
//
// Commit 2/6 (PR-C-YouTube-Cutover, June 2026, Correttezza #6): the
// CommitClipAndIndexEvent parameter is now `youtubetypes.ClipAsset`
// (the canonical, strongly-typed internal domain entity) instead of
// `youtubetypes.ExtractItem` (the HTTP response shape). The
// verdict's P1 #6 mandates "il writer deve ricevere il record
// canonico, non un DTO di risposta HTTP" — ClipAsset bundles the
// ID/VideoID/LocalPath/FileHash/Drive/Coordinates/Metadata fields
// the writer needs in one typed struct so the DB column mapping is
// explicit and refactor-resistant.
//
// File layout (refactor Step 7 — clip_atomic_writer split, July 2026):
//
//	clip_atomic_writer.go         — orchestrator + 2 entry points (this file)
//	clip_atomic_writer_asset.go   — media_assets UPSERT + column-mapping +
//	                                derivation helpers (derive* / sourceVersion)
//	clip_atomic_writer_tracks.go  — asset_text_tracks UPSERT (RETURNING id),
//	                                match-key builder, LocalizedClipText→TextTrack
//	clip_atomic_writer_cues.go    — asset_text_track_segments BATCH INSERT
//	                                with sequence_no assignment
//	clip_atomic_writer_outbox.go  — outbox INSERT helper (tx-bound) + post-
//	                                commit typed-error contract (BLOCKER #4)
//	clip_atomic_writer_text.go   — CommitClipTextAndIndexEvent +
//	                                commitClipTextAndIndexEvent_validatePolicy
//	                                (localized super-tx; commits D refactor)
//
// Transaction shape (canonical PR-C PR-VO-A3 pattern):
//
//	BEGIN
//	UPSERT media_assets SET  ... (id=clipID, lifecycle_state='ACTIVE',
//	                                  source_version=deriveSourceVersion(...))
//	BUILD  eventKey, payload = BuildReindexEnvelopeV1(
//	          clipID, targetSchema="asset.index.requested.v1",
//	          sourceVersion=deriveSourceVersion(clipID, asset.FileHash, asset.PolicyVersion),
//	          requestedAt=now)
//	INSERT outbox_events (...) ON CONFLICT(event_key) WHERE !='' DO NOTHING
//	COMMIT
//
// Audit 2026-07-03 BLOCKER #2 closure: source_version is now written
// to BOTH the media_assets.source_version column (via
// clip_atomic_writer_asset.go::upsertClipInTx) AND the outbox event
// envelope (BuildReindexEnvelopeV1). The CAS fence in
// clipindexer.setIndexedAt reads source_version from the column;
// before this fix the column was always ” (default) while the event
// carried the real value — the CAS fence starved.
//
// Idempotency contracts (mirrored from outboxevents.BuildReindexEnvelopeV1):
//   - eventKey shaped "reconcile:reindex:<assetID>:<schema>:<source>".
//     Repeated calls with the same (clipID, file_hash, policy) tuple
//     collapse via outbox ON CONFLICT(event_key) DO NOTHING — only one
//     outbox row exists for any (clip, content) pair.
//   - Different file_hash on retry → new eventKey → new outbox row.
//     The supersede gate downstream (outbox.Pool / IndexingHandler)
//     compares payload.source_version against the current
//     media_assets.content_hash and rejects stale events as supersede.
//   - Empty sourceVersion is fail-closed at BuildReindexEnvelopeV1.
//     We compute sourceVersion = asset.FileHash (the canonical
//     ingest-time hash). On empty FileHash (the upstream
//     hash.MD5File skipped path) we fall back to a deterministic
//     MD5(clipID + policyVersion) so the event_key remains stable
//     across retries.
//
// godlike/06 SSOT (single tx, helpers-as-receivers): the orchestrator
// is the SOLE file that opens/closes the *sql.Tx. The four sibling
// helper files accept *sql.Tx as a parameter (never open their own).
// No business-logic branching lives inside the helpers — they only
// compose SQL strings and pass arguments. Provenance / language
// derivation lives ONCE (here in clip_atomic_writer_asset.go for
// column-mapping) and is reused across both entry points via direct
// calls; we never duplicate this logic.
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// ClipAtomicWriterAdapter implements youtubeports.ClipAtomicWriter over
// the canonical SQLite + outbox events schema. Holds *sql.DB (the
// ledger connection) and *outboxevents.Repository (the outbox writer
// — talks into the SAME connection within the same tx per the
// outboxevents.Repository.Enqueue contract).
type ClipAtomicWriterAdapter struct {
	db  *sql.DB
	box *outboxevents.Repository
	log *zap.Logger
	now func() time.Time // injectable clock for tests; production = time.Now
}

// NewClipAtomicWriterAdapter constructs the adapter. Both db AND box
// MUST be non-nil — a nil either side is a fail-closed panic so a
// wiring gap lands in a build-side output rather than a runtime panic
// at first CommitClipAndIndexEvent call.
func NewClipAtomicWriterAdapter(db *sql.DB, box *outboxevents.Repository, log *zap.Logger) *ClipAtomicWriterAdapter {
	if db == nil {
		panic("assets.NewClipAtomicWriterAdapter: db is required (composition must pass root.DB.DB)")
	}
	if box == nil {
		panic("assets.NewClipAtomicWriterAdapter: outboxevents.Repository is required (composition must pass root.Outbox.EventsRepo)")
	}
	return &ClipAtomicWriterAdapter{
		db:  db,
		box: box,
		log: log,
		now: time.Now,
	}
}

// CommitClipAndIndexEvent performs the canonical atomic write.
//
// Commit 2/6: the parameter is `youtubetypes.ClipAsset` (canonical
// domain entity). The ClipAsset's Drive / Coordinates / Metadata
// fields are the canonical writer surface; the column mapping in
// `upsertClipInTx` reads from ClipAsset's nested structs.
//
// Helper-call order MUST-stay (Step 7 split — see file header):
//
//  1. BeginTx                              ← orchestrator
//  2. upsertClipInTx                       ← asset.go
//     2.5) upsertTextTracksInTx (legacy)      ← clip_metadata_writer.go (pre-existing)
//  3. BuildReindexEnvelopeV1               ← outboxevents envelope.go (no SQL)
//  4. enqueueClipIndexEventInTx            ← outbox.go
//  5. Commit                               ← orchestrator
//  6. checkOutboxTerminalAfterCommit       ← outbox.go (BLOCKER #4)
func (w *ClipAtomicWriterAdapter) CommitClipAndIndexEvent(
	ctx context.Context,
	clipID string,
	asset youtubetypes.ClipAsset,
	event youtubeports.IndexEventPayload,
) error {
	if w == nil || w.db == nil || w.box == nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: adapter not wired")
	}
	if clipID == "" {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: clipID is required")
	}
	if event.Type != "" && event.Type != outboxevents.EventAssetIndexRequested {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: unsupported event.Type %q (only %q supported)",
			event.Type, outboxevents.EventAssetIndexRequested)
	}
	if event.Type == "" {
		event.Type = outboxevents.EventAssetIndexRequested
	}
	if event.AggregateID == "" {
		event.AggregateID = clipID
	}

	// ── 1) Begin tx (orchestrator-owned).
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// ── 2) UPSERT media_assets (BLOCKER #2 closure: source_version
	//     is now written to the DB column, not just the outbox envelope).
	nowStr := w.now().UTC().Format(time.RFC3339)
	// Compute before both UPSERT + outbox so both surfaces agree on
	// the same sourceVersion — invariant enforced by the test.
	policyVersion := asset.PolicyVersion
	if policyVersion == "" {
		policyVersion = derivePolicyVersion(clipID)
	}
	sourceVersion := deriveSourceVersion(clipID, asset.FileHash, policyVersion)
	if err := upsertClipInTx(ctx, tx, clipID, asset, sourceVersion, nowStr); err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: upsert: %w", err)
	}

	// ── 2.5) UPSERT asset_text_tracks from payload Texts[] (legacy stripe).
	// When the caller provided localized texts (transcripts, descriptions,
	// etc.) in the Segment.Texts[] field, persist them atomically in the
	// same transaction as media_assets + outbox_events. This eliminates
	// the race where a separate TextTrackResolver.Save() call could fail
	// silently after Step 9 committed.
	//
	// godlike/06 SSOT: this helper is defined in clip_metadata_writer.go
	// (a sibling file in the same package). We inherit the existing
	// implementation verbatim — splitting added RETURNING-id semantics
	// to its sibling `upsertTextTracksReturningIDsInTx` in
	// clip_atomic_writer_tracks.go, but the legacy non-RETURNING helper
	// stays put so callers that already use it (e.g. metadata-only
	// paths) keep their behaviour byte-for-byte.
	if len(asset.Texts) > 0 {
		tracks := localizedClipTextsToTextTracks(clipID, asset.Texts)
		if len(tracks) > 0 {
			if err := upsertTextTracksInTx(ctx, tx, tracks, nowStr); err != nil {
				return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: upsert text tracks: %w", err)
			}
		}
	}

	// ── 3) Build canonical v1 envelope (no SQL — outboxevents.BuildReindexEnvelopeV1).
	eventKey, payloadJSON, err := outboxevents.BuildReindexEnvelopeV1(
		clipID,
		outboxevents.ReindexEnvelopeV1Schema,
		sourceVersion,
		w.now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: build envelope: %w", err)
	}
	// Blocco 1.1: the canonical envelope built by BuildReindexEnvelopeV1
	// always wins. The caller MUST NOT supply a custom payload — the
	// IndexEventPayload carries only routing fields (Type, AggregateID,
	// CreatedAt). The previous `if len(event.Payload) > 0` override path
	// replaced the canonical envelope with an ad-hoc payload that the
	// IndexingHandler consumer rejected as terminal (dead_letter).

	// ── 4) INSERT outbox_events (tx-bound helper).
	enqResult, err := enqueueClipIndexEventInTx(
		ctx, w.box, tx,
		event.Type, event.AggregateID, clipID,
		payloadJSON, eventKey,
	)
	if err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: outbox enqueue: %w", err)
	}

	// ── 5) Commit (orchestrator-owned).
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ClipAtomicWriterAdapter.CommitClipAndIndexEvent: commit: %w", err)
	}
	committed = true

	// ── 6) BLOCKER #4 closure: terminal conflict → typed error, not silent success.
	// Pre-closure the writer logged a warning and returned nil, producing
	// "processed" with no index event. Post-closure we return a typed
	// sentinel so the use case can surface "processed_but_index_blocked".
	if terr := checkOutboxTerminalAfterCommit(w.log, enqResult, clipID, eventKey); terr != nil {
		return terr
	}

	if w.log != nil {
		w.log.Debug("ClipAtomicWriterAdapter: clip + index event committed",
			zap.String("clip_id", clipID),
			zap.String("event_key", eventKey),
			zap.String("source_version", sourceVersion),
			zap.Bool("outbox_inserted", enqResult.Inserted))
	}
	return nil
}

// ── Compile-time assertions (AGENTS.md Pattern 0) ────────────────────

// Per AGENTS.md Pattern 0: the concrete receiver must satisfy the
// typed port so any signature drift surfaces as a build failure.
var _ youtubeports.ClipAtomicWriter = (*ClipAtomicWriterAdapter)(nil)

// ── Per AGENTS.md Pattern 0 (second port): the concrete receiver
// INTENT-DUAL-PORT satisfies the localized-clip atomic writer port
// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b, July 2026). The exact
// same concrete instance is wired by the composition root through
// BOTH ports (processSegmentDeps.Writer AND
// processSegmentDeps.LocalizedWriter) — the dual-port pattern is
// intentional: the legacy stripe (CommitClipAndIndexEvent, no text
// tracks) and the new atomic-super-tx stripe
// (CommitClipTextAndIndexEvent, clip+tracks+cues+outbox) both
// resolve to the same *ClipAtomicWriterAdapter which keeps a single
// SQL connection pool + a single outbox.Repository handle. Adding
// a parallel adapter would double the connection pool and risk
// dead-letter routing divergence between the two surfaces — this
// dual-port assertion is the SSOT that defeats that divergence.
var _ localized.LocalizedClipWriter = (*ClipAtomicWriterAdapter)(nil)

// ── Diagnostics ────────────────────────────────────────────────────

// MarshalCanonicalPayload marshals a map into a JSON raw message for
// callers that want to enrich the canonical payload with extra fields
// (e.g. metadata.summary, source_url). Exposed at the package boundary
// so callers don't need to import encoding/json directly.
func MarshalCanonicalPayload(extra map[string]any) (json.RawMessage, error) {
	if extra == nil {
		return json.RawMessage(`{}`), nil
	}
	return json.Marshal(extra)
}
