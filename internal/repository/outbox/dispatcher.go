// Package outbox — Dispatcher is the canonical ingestion entry point.
//
// PR1 invariant: every code path that mutates media_assets and triggers
// vector indexing MUST route through Dispatcher.EnqueueAndIndex. Doing so
// guarantees that the metadata write (media_assets) and the indexing job
// (media_index_outbox) are committed atomically — no orphan jobs, no
// orphan embeddings.
//
// The ONLY legitimate way to bypass the outbox is the DirectIndexer, which
// is restricted to admin reindex endpoints (see direct_indexer.go for
// the rule). All other callers must use Dispatcher.
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/models"
)

// ClipsUpserter is the *clips.Repository method surface the Dispatcher needs.
// Defined as an interface so unit tests can substitute a fake without
// pulling the full clips.Repository dependency.
type ClipsUpserter interface {
	UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *models.MediaAsset) error
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
// By colocating the upsert and the outbox insert in a single transaction
// we either commit both or neither; the OutboxWorker then picks up the
// entry and runs IndexClip on its own schedule.
type Dispatcher struct {
	clips ClipsUpserter
	repo  *Repository
	txmgr TxManager
	log   *zap.Logger
}

// NewDispatcher wires a Dispatcher against the canonical dependencies.
// clips is typically *clips.Repository (which implements ClipsUpserter).
func NewDispatcher(clips ClipsUpserter, repo *Repository, txmgr TxManager, log *zap.Logger) *Dispatcher {
	return &Dispatcher{clips: clips, repo: repo, txmgr: txmgr, log: log}
}

// EnqueueAndIndex performs UPSERT media_assets + INSERT media_index_outbox
// in a single atomic transaction, then commits. After commit, the
// outbox worker (worker.go) will see the new pending entry and run
// IndexClip on it asynchronously.
//
// Callers MUST NOT subsequently run SafeGoFunc(IndexClip(...)) — the
// outbox entry IS the indexing trigger.
//
// contentHash should be the canonical content fingerprint. The same
// (asset_id, content_hash, embedding_model, embedding_version,
// collection_version) tuple intentionally produces an INSERT OR IGNORE
// no-op (idempotent), so duplicate ingestions are safe.
//
// Folders (clip.IsFolder == true) MUST be filtered by the caller before
// calling — vector indexing of folders is meaningless.
//
// ──────────────────────────────────────────────────────────────────────────
// End-to-end auto-sync to Qdrant (canonical flow, verified June 2026)
// ──────────────────────────────────────────────────────────────────────────
// Operators do NOT need a manual sync step (e.g. calling
// scripts/tools/reindex_qdrant.py or embedding_server /index_bulk) after
// a canonical ingest through Dispatcher.EnqueueAndIndex. The pipeline runs
// automatically:
//
//	EnqueueAndIndex commits (media_assets UPSERT + media_index_outbox INSERT)
//	  ↓
//	OutboxWorker (cfg.Outbox.PollIntervalMs, default 500ms; the JOBS
//	  runner in background_jobs.go uses a separate PollEvery=2s — those are
//	  different schedulers) claims the row
//	  ↓
//	clipindexer.IndexClip(ctx, assetID):
//	  1. State transitions: pending → embedding → upserting → indexed
//	  2. POST embedding_server.py /index with the clip's search_text
//	     → multilingual-e5-base 768d vector (text named-vector)
//	     → writes media_assets.embedding_json only (768d multilingual-e5-base)
//	     → /index_transcript, /index_visual, /index_audio are independent
//	       on-demand endpoints — they are NOT called by the IndexClip hot
//	       path; transcript/visual/audio vectors are populated via separate
//	       IndexClip-API calls when the operator triggers them.
//	  3. clipindexer.UpsertVectorStore →
//	     vectorstore.Service.UpsertAsset →
//	     Qdrant PUT /collections/{alias}/points
//	     (point id = uuid5(DNS_NS, assetID); payload includes drive_link,
//	      local_path, search_text, tags; sparse = BM25(search_text))
//
// Failure modes handled without manual intervention:
//   - Embedding server unreachable → IndexClip retries 3× with exp. backoff,
//     then outbox row goes to dead_letter; the OutboxWorker's reclaim loop
//     will re-claim later.
//   - Qdrant unreachable → same retry/dead_letter pattern.
//
// Both ops are idempotent (Qdrant upsert by point id; SQLite UPSERT via
// (asset_id, content_hash, embedding_model, embedding_version,
// collection_version) on the outbox row), so a duplicate worker run after
// a partial failure converges to the correct state.
//
// The ONLY historical case where manual sync was needed was when a tool
// inserted rows into media_assets bypassing the dispatcher (one-shot
// bootstrap scripts that used UPSERT_SQL directly). Those scripts have
// been removed; canonical ingests flow through this Dispatcher.
func (d *Dispatcher) EnqueueAndIndex(ctx context.Context, clip *models.MediaAsset, contentHash string) error {
	if d == nil {
		return errors.New("outbox.Dispatcher is nil")
	}
	if d.txmgr == nil {
		return errors.New("outbox.Dispatcher: txmgr not configured")
	}
	if d.clips == nil {
		return errors.New("outbox.Dispatcher: clips repo not configured")
	}
	if d.repo == nil {
		return errors.New("outbox.Dispatcher: outbox repo not configured")
	}
	if clip == nil || clip.ID == "" {
		return errors.New("clip with non-empty ID is required")
	}
	// Folders are not vector-indexable. Defense in depth: callers SHOULD
	// filter, but a forgotten caller must not trigger a wasted embedding
	// job. The metadata UPSERT still runs so Drive folder traversal is
	// not broken — only the outbox enqueue is suppressed.
	if clip.IsFolder {
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
		entry := &OutboxEntry{
			AssetID:           clip.ID,
			ContentHash:       contentHash,
			EmbeddingModel:    clipindexer.EmbeddingModel(),
			EmbeddingVersion:  clipindexer.EmbeddingModelVersion(),
			CollectionVersion: clipindexer.CollectionVersion(),
		}
		if err := d.clips.UpsertClipTx(ctx, tx, clip); err != nil {
			return fmt.Errorf("dispatcher upsert clip %s: %w", clip.ID, err)
		}
		if err := d.repo.Enqueue(ctx, tx, entry); err != nil {
			return fmt.Errorf("dispatcher enqueue outbox %s: %w", clip.ID, err)
		}
		if d.log != nil {
			d.log.Debug("dispatcher enqueued asset for outbox indexing",
				zap.String("asset_id", clip.ID),
				zap.String("source", clip.Source),
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
// must ingest across many per-source clips.Repository instances (e.g.
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
func (m *MultiClipsUpserter) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *models.MediaAsset) error {
	if m == nil {
		return errors.New("outbox.MultiClipsUpserter is nil")
	}
	if clip == nil {
		return errors.New("outbox.MultiClipsUpserter: clip is nil")
	}
	var repo ClipsUpserter
	var matched bool
	if clip.Source != "" {
		if r, ok := m.repos[clip.Source]; ok && r != nil {
			repo = r
			matched = true
		}
	}
	if !matched {
		// Surface the fallback as a debug entry so misconfigured
		// clip.Source strings show up in dev/staging without paying the
		// cost of an error log in prod.
		m.log.Debug("MultiClipsUpserter: using default repo for unknown source",
			zap.String("source", clip.Source),
			zap.String("asset_id", clip.ID),
		)
		repo = m.defaultRepo
	}
	if repo == nil {
		return fmt.Errorf("outbox.MultiClipsUpserter: no repository for source %q and no default configured", clip.Source)
	}
	return repo.UpsertClipTx(ctx, tx, clip)
}
