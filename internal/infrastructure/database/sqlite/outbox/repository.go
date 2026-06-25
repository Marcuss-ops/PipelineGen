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
// UPSERT + INSERT-IN-OUTBOX pattern.
//
// Every ingestion path (catalogsync, YouTube clip registration, Artlist
// clip processing, stock pipeline, manual upload, transcript updates, …)
// MUST funnel through Dispatcher.EnqueueAndIndex. The previous pattern of
// `repo.UpsertClip; concurrent.SafeGoFunc(IndexClip)` violated atomicity:
// if the goroutine crashed before IndexClip ran, the asset had metadata
// but no embedding; if the goroutine started before the upsert committed,
// a concurrent reader saw a half-state.
//
// By colocating the upsert and the outbox_events insert in a single
// transaction we either commit both or neither; the outboxevents Pool
// then picks up the event and runs IndexClip via the
// IndexingHandler.
type Dispatcher struct {
	clips            ClipsUpserter
	outboxEventsRepo *outboxevents.Repository
	txmgr            TxManager
	log              *zap.Logger
}

// NewDispatcher wires a Dispatcher against the canonical dependencies.
// clips is typically *assets.ClipsRepository (which implements ClipsUpserter).
// outboxEventsRepo is the canonical outbox_events repository for
// asset.index.requested event enqueue.
func NewDispatcher(clips ClipsUpserter, outboxEventsRepo *outboxevents.Repository, txmgr TxManager, log *zap.Logger) *Dispatcher {
	return &Dispatcher{clips: clips, outboxEventsRepo: outboxEventsRepo, txmgr: txmgr, log: log}
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
