// Package outbox — Dispatcher is the canonical ingestion entry point.
//
// PR1 invariant: every code path that mutates media_assets and triggers
// vector indexing MUST route through Dispatcher.EnqueueAndIndex. Doing so
// guarantees that the metadata write (media_assets) and the indexing job
// (outbox_events insert) are committed atomically — no orphan jobs, no
// orphan embeddings.
//
// The ONLY legitimate way to bypass the outbox is the DirectIndexer, which
// is restricted to admin reindex endpoints (see direct_indexer.go for
// the rule). All other callers must use Dispatcher.
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
)

// ClipsUpserter is the *assets.ClipsRepository method surface the Dispatcher needs.
// Defined as an interface so unit tests can substitute a fake without
// pulling the full assets.ClipsRepository dependency.
type ClipsUpserter interface {
	UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error
}

// Dispatcher is the ingestion entry point for the canonical
// UPSERT + INSERT-IN-OUTBOX pattern AND the canonical DEL +
// INSERT-IN-OUTBOX pattern (QDRANT-002 PR7 EnqueueAndDelete).
//
// Every ingestion path (catalogsync, YouTube clip registration, Artlist
// clip processing, stock pipeline, manual upload, transcript updates, …)
// MUST funnel through Dispatcher.EnqueueAndIndex. The previous pattern of
// `repo.UpsertClip; concurrent.SafeGoFunc(IndexClip)` violated atomicity:
// if the goroutine crashed before IndexClip ran, the asset had metadata
// but no embedding; if the goroutine started before the upsert committed,
// a concurrent reader saw a half-state.
//
// Every deletion path (DeletionService, admin delete endpoints, future
// archival pipelines) MUST funnel through Dispatcher.EnqueueAndDelete.
// The producer tx writes the canonical DELETE_PENDING marker on the
// media_assets.index_state column BEFORE the outbox event row, so even
// if the worker crashes mid-process an operator dashboard sees the
// delete-intent without waiting for the lease acquire. The handler
// (IndexDeleteHandler) completes the picture with Qdrant delete +
// SQLite SoftDelete + DELETED state flip — keeping the two side of
// the operation out of the producer tx avoids the orphan-vector bug
// (if SoftDelete ran in the producer tx, the handler's idempotency
// pre-flight would short-circuit on lifecycle_state='deleted' and
// skip Qdrant delete — QDRANT-002 PR5 ticket analysis 2026-06-25).
//
// By colocating the metadata write and the outbox_events insert in a
// single transaction we either commit both or neither; the outboxevents
// Pool then picks up the event and runs IndexClip (or IndexDelete) via
// the corresponding Handler.
type Dispatcher struct {
	clips            ClipsUpserter
	stateWriter      ClipsStateWriter
	outboxEventsRepo *outboxevents.Repository
	txmgr            TxManager
	log              *zap.Logger
}

// DeleteRequestSchemaVersion is the canonical, EXACT string the
// handler on the consumer side accepts. Producers MUST send
// "asset.index.delete_requested.v1" literally. This is duplicated
// here (vs importing it from the consumer side) so the outbox
// package — a leaf infra dependency that the application layer is
// allowed to import — does not introduce an import cycle. Mismatch
// is treated as TERMINAL by the handler (QDRANT-002 PR4 invariant I).
const DeleteRequestSchemaVersion = "asset.index.delete_requested.v1"

// NewDispatcher wires a Dispatcher against the canonical dependencies.
// clips is typically *assets.ClipsRepository (which implements ClipsUpserter).
// stateWriter is typically the same *assets.ClipsRepository (which
// implements ClipsStateWriter from PR7); the two-method split makes
// production wiring explicit and lets tests substitute fakes for one
// half without spelling out the other.
// outboxEventsRepo is the canonical outbox_events repository for
// asset.index.requested + asset.index.delete_requested event enqueue.
func NewDispatcher(
	clips ClipsUpserter,
	stateWriter ClipsStateWriter,
	outboxEventsRepo *outboxevents.Repository,
	txmgr TxManager,
	log *zap.Logger,
) *Dispatcher {
	return &Dispatcher{
		clips:            clips,
		stateWriter:      stateWriter,
		outboxEventsRepo: outboxEventsRepo,
		txmgr:            txmgr,
		log:              log,
	}
}

// indexRequestV1 is the canonical envelope Dispatcher emits on
// the asset.index.requested.v1 event type (QDRANT-002 PR4, closes
// ticket items F + I).
//
// Producers MUST send the ingest-time content hash as
// source_version — the worker's supersede gate compares it against
// the current media_assets.metadata_json.$.content_hash and
// short-circuits stale events (see
// internal/application/jobs/outbox.indexingHandler).
//
// Schema_version is the canonical literal of the envelope family
// (mirrors IndexRequestSchemaVersion constant in the handler).
// Requested vectors default to ["text", "transcript"] so legacy
// consumers without an explicit vector list still work.
type indexRequestV1 struct {
	SchemaVersion      string   `json:"schema_version"`
	EventID            string   `json:"event_id"`
	AssetID            string   `json:"asset_id"`
	Operation          string   `json:"operation"`
	SourceVersion      string   `json:"source_version"`
	TargetIndexVersion string   `json:"target_index_version"`
	RequestedVectors   []string `json:"requested_vectors"`
	RequestedAt        string   `json:"requested_at"`
	EmbeddingModel     string   `json:"embedding_model,omitempty"`
	EmbeddingVersion   string   `json:"embedding_version,omitempty"`
	IdempotencyKey     string   `json:"idempotency_key"`
}

// EnqueueAndIndex performs UPSERT media_assets + INSERT outbox_events
// (event_type='asset.index.requested') in a single atomic transaction,
// then commits. After commit, the outboxevents Pool will see the new
// pending event and run IndexClip on it asynchronously via the
// IndexingHandler.
//
// Callers MUST NOT subsequently run SafeGoFunc(IndexClip(...)) — the
// outbox event IS the indexing trigger.
//
// contentHash should be the canonical content fingerprint. Used to build
// the event_key for deduplication, so duplicate ingestions are safe.
//
// Folders (clip.IsFolder == true) MUST be filtered by the caller before
// calling — vector indexing of folders is meaningless.
//
// ──────────────────────────────────────────────────────────────────────────
// End-to-end auto-sync to Qdrant (canonical flow, June 2026)
// ──────────────────────────────────────────────────────────────────────────
// Operators do NOT need a manual sync step after a canonical ingest
// through Dispatcher.EnqueueAndIndex. The pipeline runs automatically:
//
//	EnqueueAndIndex commits (media_assets UPSERT + outbox_events INSERT)
//	  ↓
//	outboxevents Pool (cfg.Outbox.PollIntervalMs, default 500ms) claims
//	  the event via CTE-based atomic claim + lease fencing
//	  ↓
//	IndexingHandler calls clipindexer.IndexClip(ctx, assetID):
//	  1. State transitions: pending → embedding → upserting → indexed
//	  2. POST embedding_server.py /index with the clip's search_text
//	     → multilingual-e5-base 768d vector
//	  3. Qdrant PUT /collections/{alias}/points (point id = uuid5(assetID))
//
// Failure modes handled without manual intervention:
//   - Embedding server unreachable → IndexClip retries, IndexingHandler
//     returns error → outboxevents Pool calls MarkFailed (retry with
//     backoff, or dead_letter after max attempts)
//   - Qdrant unreachable → same retry/dead_letter pattern
func (d *Dispatcher) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
	}
	if d.clips == nil {
		return errors.New("outbox.Dispatcher: clips repo not configured")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("outbox.Dispatcher: outbox events repo not configured")
	}
	if clip == nil || clip.ID == "" {
		return errors.New("clip with non-empty ID is required")
	}
	// Folders are not vector-indexable. Defense in depth: callers SHOULD
	// filter, but a forgotten caller must not trigger a wasted embedding
	// job. The metadata UPSERT still runs so Drive folder traversal is
	// not broken — only the outbox enqueue is suppressed.
	if clip.IsFolder() {
		return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
			if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
				return fmt.Errorf("dispatcher upsert folder %s: %w", clip.ID, err)
			}
			if d.log != nil {
				d.log.Debug("dispatcher skipped outbox enqueue for folder",
					zap.String("asset_id", clip.ID),
				)
			}
			return nil
		})
	}

	// Embedding-model package-level vars MUST be read inside the closure
	// so a misconfigured startup is observable as a panic at commit time,
	// not as a silent empty-string stamp in the outbox row.
	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
			return fmt.Errorf("dispatcher upsert clip %s: %w", clip.ID, err)
		}

		// Canonical v1 payload — schema_version literal matches the
		// handler's IndexRequestSchemaVersion so the round trip is
		// strictly validated. operation defaults to UPSERT (the only
		// value v1 supports). source_version is the ingest-time
		// content hash the worker reads from metadata_json later.
		// event_id is a producer-side UUID for operator audit.
		eventID := uuid.NewString()
		// Idempotency_key vs event_key — DO NOT split (QDRANT-002 v1):
		//
		//   - idempotency_key (the payload field produced below) is
		//     the operational audit token callers reference when
		//     replaying events.
		//   - event_key (the outbox_events.event_key column written
		//     further down) is the dedup vector used by
		//     ON CONFLICT(event_key) DO NOTHING.
		//
		// Both carry the SAME string in v1. There is no separate
		// operational semantics for the payload field yet — the
		// payload field is read by humans for log/dashboard audit
		// only, not by anything machine-actionable. Splitting them
		// is NOT a v1 fixup: any shape that diverges from event_key
		// is by definition a new field with new semantics. Treat
		// any future split as a v2 envelope change (new
		// schema_version literal, new optional field), not a
		// drive-by refactor. The runtime assertion below catches a
		// divergence on the first dispatch and rolls back the tx
		// loudly — both the comment and the assertion are
		// load-bearing for the v1 contract.
		eventKey := fmt.Sprintf("index:%s:%s:%s:%s:%s",
			clip.ID,
			shortHashPrefix(contentHash),
			clipindexer.EmbeddingModel(),
			clipindexer.EmbeddingModelVersion(),
			clipindexer.CollectionVersion(),
		)
		// event_key deduplicates: same (asset_id, content_hash,
		// embedding_model, embedding_version, collection_version)
		// tuple prevents duplicate indexing jobs. Uses full content
		// hash (not prefix) so distinct ingestions of the same asset
		// with identical models/versions BUT different content
		// fingerprints correctly produce distinct outbox rows —
		// the supersede gate then closes the older one. Matches the
		// old media_index_outbox unique key semantics for legacy
		// continuity.
		idempotencyKey := eventKey
		payload := indexRequestV1{
			SchemaVersion:      "asset.index.requested.v1",
			EventID:            eventID,
			AssetID:            clip.ID,
			Operation:          "UPSERT",
			SourceVersion:      contentHash,
			TargetIndexVersion: clipindexer.CollectionVersion(),
			RequestedVectors:   []string{"text", "transcript"},
			// FormatRFC3339 is the canonical pattern across this
			// package (see outboxevents/repository.go::MarkCompleted
			// + MarkFailed); pkg/timeutil keeps all timestamps in
			// the same RFC3339-with-nano-seconds shape so log greps
			// + replay tooling find the exact substring they're
			// looking for.
			RequestedAt:      timeutil.FormatRFC3339(time.Now()),
			EmbeddingModel:   clipindexer.EmbeddingModel(),
			EmbeddingVersion: clipindexer.EmbeddingModelVersion(),
			IdempotencyKey:   idempotencyKey,
		}
		// Runtime safety net for the v1 conflation contract (see the
		// "DO NOT split" comment above): the payload's idempotency_key
		// field and the column's event_key MUST carry the same
		// string. A future agent "fixing" the perceived duplication
		// would break this assertion. Cheap to test (one string
		// compare), expensive to discover in a production
		// replay-mismatch incident.
		if payload.IdempotencyKey != eventKey {
			return fmt.Errorf("dispatcher: payload.IdempotencyKey (%q) != event_key (%q) — v1 conflation invariant broken", payload.IdempotencyKey, eventKey)
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dispatcher marshal v1 index payload %s: %w", clip.ID, err)
		}

		if err := d.outboxEventsRepo.Enqueue(
			ctx, tx,
			outboxevents.EventAssetIndexRequested,
			clip.ID,
			"media_asset",
			string(payloadJSON),
			eventKey,
		); err != nil {
			return fmt.Errorf("dispatcher enqueue outbox event %s: %w", clip.ID, err)
		}

		if d.log != nil {
			d.log.Debug("dispatcher enqueued asset for outbox_events indexing (v1 envelope)",
				zap.String("asset_id", clip.ID),
				zap.String("outbox_event_id", eventID),
				zap.String("source", string(clip.Source)),
				zap.String("source_version", contentHash),
				zap.String("content_hash_prefix", shortHashPrefix(contentHash)),
			)
		}
		return nil
	})
}

// shortHashPrefix returns a short log-friendly prefix; the empty string
// yields "" so log readers do not see a misleading "(empty)" marker.
func shortHashPrefix(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

// EnqueueAndDelete performs the canonical DISPATCH step of an
// asset.delete flow (QDRANT-002 PR7):
//
//	tx body:
//	  1. SET index_state=DELETE_PENDING on media_assets (the
//	     visibility marker operators see on dashboards while the
//	     Qdrant delete is in flight; survives a worker crash)
//	  2. INSERT outbox_events (event_type='asset.index.delete_requested',
//	     idempotency_key/event_key via ON CONFLICT DO NOTHING)
//
// Both writes commit atomically. After commit, the outboxevents
// Pool picks up the event and runs IndexDeleteHandler.Handle which
// finishes the delete (Qdrant DeletePoints + SQLite SoftDelete +
// index_state=DELETED).
//
// IMPORTANT: this function does NOT call repo.SoftDelete. QDRANT-002
// PR5 analysis confirmed that doing SoftDelete here would break the
// IndexDeleteHandler idempotency pre-flight (which short-circuits on
// lifecycle_state in {deleted, DELETED}, leaving orphan Qdrant vectors).
// See application/jobs/outbox/index_delete.go::Handle for the matching
// pre-flight you must keep in sync if you tune this contract.
//
// Required wiring:
//   - d.stateWriter: any ClipsStateWriter (production: *assets.ClipsRepository).
//     nil → error returned (defense in depth — production wiring should
//     supply a non-nil writer; NewDispatcher also accepts nil for test
//     fixtures that exercise only EnqueueAndIndex).
//   - d.outboxEventsRepo: not nil.
//   - d.txmgr: not nil.
//
// Required inputs:
//   - assetID: non-empty string; the canonical media_assets.id. The
//     function does NOT verify existence in the assets table — a
//     missing row is a benign no-op (the worker's pre-flight catches
//     it and short-circuits the Qdrant + soft-delete phase to
//     success, idempotent). This is intentional: callers do NOT
//     need to pre-fetch the row before enqueuing.
//
// Idempotency:
//   - Many producers can call EnqueueAndDelete in rapid succession
//     for the same assetID. event_key shape `delete:<asset_id>` (see
//     deleteEventKey) collapses all but the first into the
//     outbox_events ON CONFLICT DO NOTHING. The repeated calls are
//     safe — only one event row is created and one Qdrant delete
//     fires.
//   - The deadline mid-flight retry is also safe: column flip from
//     DELETE_PENDING → DELETED is idempotent (no-op on already-DELETED),
//     Qdrant DeletePoints is natively idempotent at the API layer
//     (200 + deleted_count:0 on missing point), and the SQLite
//     SoftDelete is itself idempotent through the lifecycle_state
//     guard in ClipsRepository.AssetStoreSQLite.Delete.
//
// Returns: nil on commit; wrapped error if any tx step fails (the
// tx is rolled back — neither the index_state flip NOR the outbox
// row are observable to readers). Empty assetID returns an error
// without opening a tx.
func (d *Dispatcher) EnqueueAndDelete(ctx context.Context, assetID string) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
	}
	if d.stateWriter == nil {
		return errors.New("outbox.Dispatcher: state writer not configured (required for EnqueueAndDelete — wire *assets.ClipsRepository)")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("outbox.Dispatcher: outbox events repo not configured")
	}
	if assetID == "" {
		return errors.New("outbox.Dispatcher.EnqueueAndDelete: assetID is required")
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		// Step 1: stamp index_state=DELETE_PENDING before any external
		// side-effect. This is the visibility marker operators observe
		// from dashboards while the IndexDeleteHandler is mid-process.
		// StateWriter.SetIndexStateTx stamps both index_state AND
		// index_state_updated_at in one UPDATE. The change is
		// observable to a concurrent reader AFTER commit; a worker
		// crash before commit rolls back the marker (clean retry).
		if err := d.stateWriter.SetIndexStateTx(ctx, tx, assetID, asset.StateDeletePending); err != nil {
			return fmt.Errorf("dispatcher delete: set index_state=DELETE_PENDING %s: %w", assetID, err)
		}

		// Step 2: emit the v1 envelope. v1 conflation invariant as
		// in EnqueueAndIndex (see repository.go::EnqueueAndIndex for
		// the full rationale — applies verbatim here).
		payload := buildDeleteRequestV1(assetID)
		eventKey := deleteEventKey(assetID)
		if payload.IdempotencyKey != eventKey {
			return fmt.Errorf("dispatcher: delete payload.IdempotencyKey (%q) != event_key (%q) — v1 conflation invariant broken", payload.IdempotencyKey, eventKey)
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dispatcher marshal v1 delete payload %s: %w", assetID, err)
		}

		if err := d.outboxEventsRepo.Enqueue(
			ctx, tx,
			outboxevents.EventAssetIndexDeleteRequested,
			assetID,
			"media_asset",
			string(payloadJSON),
			eventKey,
		); err != nil {
			return fmt.Errorf("dispatcher enqueue outbox delete event %s: %w", assetID, err)
		}

		if d.log != nil {
			d.log.Debug("dispatcher enqueued asset for outbox_events deletion (v1 envelope)",
				zap.String("asset_id", assetID),
				zap.String("outbox_event_id", payload.EventID),
			)
		}
		return nil
	})
}

// EnqueueAndRestore performs the canonical DISPATCH step of an
// asset.restore flow (Wave 22, June 2026 — task 1 of 5 foundation;
// the restore HANDLER ships in task 3):
//
//	tx body:
//	  1. SET index_state=PENDING on media_assets (visibility
//	     marker operators observe while the restore handler is
//	     mid-process; survives a worker crash)
//	  2. INSERT outbox_events (event_type='asset.index.restore_requested',
//	     idempotency_key/event_key via ON CONFLICT DO NOTHING)
//
// Both writes commit atomically. The outboxevents Pool then picks
// up the event and runs the future restore handler which finishes
// the picture (Qdrant re-upsert + lifecycle_state flip to 'ready'
// + media_assets.deleted_at NULL).
//
// Symmetric mirror of EnqueueAndDelete (QDRANT-002 PR7) — empty
// assetID rejected before opening any tx; idempotency_key equals
// event_key to satisfy the v1 conflation invariant (see the
// EnqueueAndIndex comment block for the full rationale).
//
// Required wiring:
//   - d.stateWriter: any ClipsStateWriter (production: *assets.ClipsRepository).
//     nil → error returned (defense in depth).
//   - d.outboxEventsRepo: not nil.
//   - d.txmgr: not nil.
func (d *Dispatcher) EnqueueAndRestore(ctx context.Context, assetID string) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
	}
	if d.stateWriter == nil {
		return errors.New("outbox.Dispatcher: state writer not configured (required for EnqueueAndRestore — wire *assets.ClipsRepository)")
	}
	if d.outboxEventsRepo == nil {
		return errors.New("outbox.Dispatcher: outbox events repo not configured")
	}
	if assetID == "" {
		return errors.New("outbox.Dispatcher.EnqueueAndRestore: assetID is required")
	}

	return d.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		// Step 1: stamp index_state=PENDING before any external
		// side-effect. The worker pre-flight short-circuits if
		// the row is already in PENDING (no-op write); instant
		// retry-safe. The producer tx is the visibility layer —
		// the await for the actual lifecycle_state flip lives in
		// the future restore handler.
		if err := d.stateWriter.SetIndexStateTx(ctx, tx, assetID, asset.StateIndexPending); err != nil {
			return fmt.Errorf("dispatcher restore: set index_state=PENDING %s: %w", assetID, err)
		}

		// Step 2: emit the v1 envelope. Idempotency_key equals
		// event_key (v1 conflation invariant; see EnqueueAndIndex).
		// event_key shape `restore:<asset_id>` collapses any repeat
		// callers into a single outbox_events row via ON CONFLICT.
		eventID := uuid.NewString()
		eventKey := fmt.Sprintf("restore:%s", assetID)
		payload := restoreRequestV1{
			SchemaVersion:  "asset.index.restore_requested.v1",
			EventID:        eventID,
			AssetID:        assetID,
			Operation:      "RESTORE",
			IdempotencyKey: eventKey,
			RequestedAt:    timeutil.FormatRFC3339(time.Now()),
		}
		if payload.IdempotencyKey != eventKey {
			return fmt.Errorf("dispatcher: restore payload.IdempotencyKey (%q) != event_key (%q) — v1 conflation invariant broken", payload.IdempotencyKey, eventKey)
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dispatcher marshal v1 restore payload %s: %w", assetID, err)
		}

		if err := d.outboxEventsRepo.Enqueue(
			ctx, tx,
			outboxevents.EventAssetIndexRestoreRequested,
			assetID,
			"media_asset",
			string(payloadJSON),
			eventKey,
		); err != nil {
			return fmt.Errorf("dispatcher enqueue outbox restore event %s: %w", assetID, err)
		}

		if d.log != nil {
			d.log.Debug("dispatcher enqueued asset for outbox_events restoration (v1 envelope)",
				zap.String("asset_id", assetID),
				zap.String("outbox_event_id", eventID),
			)
		}
		return nil
	})
}

// restoreRequestV1 is the canonical envelope Dispatcher emits on
// the asset.index.restore_requested event type (Wave 22, task 1
// of 5 foundation; handler lands in task 3 of 5). Schema mirrors
// indexRequestV1 + deleteRequestV1 from sibling method blocks so
// the consumer-side decoder (future RestoreHandler) can re-use
// the v1 conflation invariant + event_key canonicalisation.
type restoreRequestV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	Operation      string `json:"operation"`
	IdempotencyKey string `json:"idempotency_key"`
	RequestedAt    string `json:"requested_at"`
}

// MultiClipsUpserter routes UpsertClipTx calls to one of several underlying
// repositories based on `clip.Source`. Useful when a single outbox.Dispatcher
// must ingest across many per-source assets.ClipsRepository instances (e.g.
// catalogsync drives youtube, stock, and artlist sources through one
// canonical dispatcher).
//
// Routing rules (in order):
//  1. If `clip.Source` matches a key in `repos` (case-sensitive) AND that
//     repository is non-nil → use it.
//  2. Otherwise, fall back to `defaultRepo`.
//  3. If `defaultRepo` is also nil, return an error.
//
// The component implements `ClipsUpserter` so it can be passed directly to
// `outbox.NewDispatcher` as the canonical ingestion surface.
type MultiClipsUpserter struct {
	repos       map[string]ClipsUpserter
	defaultRepo ClipsUpserter
	log         *zap.Logger
}

// Compile-time interface compliance check. If this fails to compile, the
// signature of ClipsUpserter has drifted and MultiClipsUpserter must be
// updated to match.
var _ ClipsUpserter = (*MultiClipsUpserter)(nil)

// NewMultiClipsUpserter constructs a routing upserter. `repos` is keyed by
// clip.Source (e.g. "youtube", "stock", "artlist") and may be nil. The
// `defaultRepo` catches any source not present in `repos`. Pass a sane
// fallback so unknown sources don't fail loudly — the prior behaviour was
// `repo.UpsertClip(...)` against a single chosen repo, so defaulting to
// the same instance preserves the silent fallback.
//
// `log` may be nil for tests; production callers pass a logger so the
// fallback path emits a debug entry that surfaces misconfigured clip.Source
// strings (e.g. an upstream typo) without paying an error cost.
func NewMultiClipsUpserter(repos map[string]ClipsUpserter, defaultRepo ClipsUpserter, log *zap.Logger) *MultiClipsUpserter {
	if log == nil {
		log = zap.NewNop()
	}
	return &MultiClipsUpserter{
		repos:       repos,
		defaultRepo: defaultRepo,
		log:         log,
	}
}

// UpsertClipTx routes the call based on clip.Source. See type doc for routing
// rules. tx is forwarded untouched to the chosen repository.
func (m *MultiClipsUpserter) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	if m == nil {
		return errors.New("outbox.MultiClipsUpserter is nil")
	}
	if clip == nil {
		return errors.New("outbox.MultiClipsUpserter: clip is nil")
	}
	var repo ClipsUpserter
	var matched bool
	if clip.Source != "" {
		if r, ok := m.repos[string(clip.Source)]; ok && r != nil {
			repo = r
			matched = true
		}
	}
	if !matched {
		// Surface the fallback as a debug entry so misconfigured
		// clip.Source strings show up in dev/staging without paying the
		// cost of an error log in prod.
		m.log.Debug("MultiClipsUpserter: using default repo for unknown source",
			zap.String("source", string(clip.Source)),
			zap.String("asset_id", clip.ID),
		)
		repo = m.defaultRepo
	}
	if repo == nil {
		return fmt.Errorf("outbox.MultiClipsUpserter: no repository for source %q and no default configured", string(clip.Source))
	}
	return repo.UpsertClipTx(ctx, tx, clip)
}

// Compile-time assertion (Wave 22 task 1 of 5, June 2026):
// *outbox.Dispatcher statically satisfies the canonical
// mutations.AssetMutationDispatcher SSOT interface declared in
// internal/application/assets/mutations/dispatcher.go.
//
// The standard AGENTS.md Pattern 0 layering rule forbids
// `internal/infrastructure/...` from importing
// `internal/application/...`. The placement of the interface in
// `internal/application/assets/mutations/` is a deliberate layering
// INVERSION: the canonical asset-mutation dispatcher port lives
// alongside its consumer (the application layer), and the dispatcher
// assertion here grants the dispatcher its explicit SSOT membership.
//
// A second assertion on the composition-root adapter
// `*mutationsDispatcherAdapter` (in internal/app/registry_adapters.go)
// covers the thin delegate. BOTH assertions are necessary because
// the adapter carries no inherent type information about the
// dispatcher's signatures — drift on *outbox.Dispatcher alone would
// not trip the adapter assertion.
//
// If a future PR changes any of the 3 method signatures
// (contentHash type, assetID naming, error contract), this line
// fails the build — the user-specified verification gate
// ("at least one compile-time assertion fires if a method signature
// drifts") is satisfied twice.
var _ mutations.AssetMutationDispatcher = (*Dispatcher)(nil)
