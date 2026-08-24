package catalogsync

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
)

type Target struct {
	Name         string
	RootFolderID string
	Source       string
	MediaType    string
	Repo         CatalogRepository
	Indexer      AssetIndexer
}

type RootSummary struct {
	Name         string `json:"name"`
	RootFolderID string `json:"root_folder_id"`
	Source       string `json:"source"`
	MediaType    string `json:"media_type"`
	Requested    int    `json:"requested"`
	Synced       int    `json:"synced"`
	Failed       int    `json:"failed"`
	Error        string `json:"error,omitempty"`
}

type Summary struct {
	OK        bool          `json:"ok"`
	Roots     []RootSummary `json:"roots,omitempty"`
	Synced    int           `json:"synced"`
	Failed    int           `json:"failed"`
	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
	Error     string        `json:"error,omitempty"`
}

// Sentinel errors returned by NewService validation. Mirrors the
// stockpipeline convention: each missing dep has its own typed sentinel
// so production wiring can forward a single error verbatim and tests
// can assert the precise missing dep without unwrapping the chain.
// PR-D (June 2026):
// the ctor validates only the fields that are referenced UNCONDITIONALLY
// at runtime. Fields that have documented nil-safe guards in call sites
// (s.assetTree in sync_prune.go::pruneMissingFolders +
// sync_recursive.go:79,175) are ACCEPTED as nil at ctor time — the
// optionality is part of the contract.
// Required at ctor: Reader, Dispatcher, Log.
// Optional at ctor: AssetTree, Targets.
//
// Wave G (June 2026) DECOUPLING — three legacy fields removed:
//   - AssetIndex  : writeAssetIndex was the sole reader of
//     internal/platform/sqlite/assetindex.Service and a
//     post-commit best-effort writer of an `asset_index` row. The
//     media_assets + outbox_events commit in upsertPreservingExisting
//     is now the single source of truth; asset_index is a derived
//     projection that no longer needs to be eagerly mirrored here.
//   - ClipIndexer : legacy field; not currently referenced in any
//     catalogsync method. Removed entirely (the canonical clip
//     indexing lives in the outbox dispatcher pipeline).
//   - defaultClipsRepo : fallback ClipsRepository for ad-hoc sources
//     like "drive" that don't have a pre-configured target. The
//     async drive.folder.sync job now errors on unknown source
//     instead of silently falling back to a guessed repo.
var (
	ErrCatalogSyncNilReader     = errors.New("catalogsync.NewService: reader is required (read-only Drive port — sync_targets.go:23 + sync_recursive.go dereference unconditionally)")
	ErrCatalogSyncNilDispatcher = errors.New("catalogsync.NewService: dispatcher is required (QDRANT-002 PR7 — production canonical ingest; sync_persist.go::upsertPreservingExisting dereferences unconditionally)")
	ErrCatalogSyncNilLog        = errors.New("catalogsync.NewService: log is required (zap.Logger dereferenced by every warn/error path in sync_persist.go + sync_targets.go + sync_prune.go)")
	ErrCatalogSyncInvalidTarget = errors.New("catalogsync: target requires repository and indexer ports")
)

// Deps is the canonical constructor input for catalogsync.Service
// (PR-D, Wave 22 §D3, June 2026). Five flat fields — well under the
// AGENTS.md 8-per-bundle cap. The legacy signature took 6 positional
// arguments + a SetDispatcher late-bind; both styles are removed.
//
// Targets is a slice so it stays 1 field regardless of how many
// pre-configured targets the caller passes. The legacy
// `defaultClipsRepo` fallback for ad-hoc sources was removed in
// Wave G (June 2026) — the async drive.folder.sync job now errors
// on unknown source instead of silently falling back to a guessed
// repo. Caller-side wiring of Targets is the single, explicit
// source of truth for source→repo mapping.
//
// PR-D: SetDispatcher setter REMOVED. Concurrency hazard eliminated
// — the s.dispatcher field is now set in the ctor and read directly
// inside upsertPreservingExisting WITHOUT re-acquiring s.mu (s.mu is
// already held by the public SyncAll/SyncSource/SyncFolderID callers).
// The happens-before relationship moves from "the late SetDispatcher
// call" to "the ctor return" — the ratchet is monotonic toward
// strictly earlier dependency capture.
//
// REQUIRED vs OPTIONAL contract (right-sized to the actual call-site
// nil-safe guards, post review):
//
//	REQUIRED (ctor rejects nil with typed sentinel):
//	  - Reader      : sync_targets.go:23 + sync_recursive.go dereference
//	                  unconditionally (read-only Drive port).
//	  - Dispatcher  : sync_persist.go::upsertPreservingExisting calls
//	                  s.dispatcher.EnqueueAndIndex unconditionally
//	                  (QDRANT-002 PR7 — production canonical ingest).
//	  - Log         : every warn/error path dereferences s.log.
//
//	OPTIONAL (nil-safe guarded at every call site):
//	  - Targets     : empty slice == no pre-configured targets; ad-hoc
//	                  SyncFolderID paths still work via the explicit
//	                  repo arg supplied by the caller. The async
//	                  drive.folder.sync job, however, requires the
//	                  source to be pre-listed in Targets (no fallback
//	                  repo as of Wave G, June 2026).
//	  - AssetTree   : sync_prune.go:35 + sync_recursive.go:79,175 —
//	                  `if s.assetTree != nil { ... }`. nil falls back to
//	                  flat folder-id sync (no tree-bounded walks).
//
// The 2 optional fields do NOT have typed sentinels because they don't
// fail at any documented runtime path. Adding Err*s for them would be
// a false-positive: composition wiring that legitimately wants a
// minimal-Deps Service (e.g. test fixtures) could not pass.
type Deps struct {
	Reader     SourceReader
	Targets    []Target
	AssetTree  *assettree.Service
	Dispatcher ProjectionDispatcher
	Log        *zap.Logger
}

type Service struct {
	reader    SourceReader
	log       *zap.Logger
	targets   []Target
	assetTree *assettree.Service
	// dispatcher is the canonical ingestion entry point. Routes media_assets
	// upserts + outbox enqueues through Dispatcher.EnqueueAndIndex (atomic),
	// replacing the legacy `repo.UpsertClip; concurrent.SafeGoFunc(IndexClip)`
	// pattern. Required for production — NewService rejects nil with
	// ErrCatalogSyncNilDispatcher; upsertPreservingExisting also surfaces
	// an explicit "dispatcher is nil" runtime check as defence-in-depth.
	//
	// Wired at composition time via Deps.Dispatcher (PR-D, June 2026).
	// See internal/platform/sqlite/outbox (package) for
	// the integration contract; BuildSyncBundle is the canonical owner.
	//
	// Wave G (June 2026) DECOUPLING — the legacy `assetIndex` and
	// `clipIndexer` fields are removed: writeAssetIndex (the only
	// reader of assetIndex) was extracted away as a redundant
	// best-effort post-commit mirror; clipIndexer was never wired
	// into any catalogsync method. The `defaultClipsRepo` fallback
	// is also removed — async drive.folder.sync now requires the
	// source to be pre-listed in Targets.
	dispatcher ProjectionDispatcher
	mu         sync.Mutex
}

// NewService creates a catalogsync service via the canonical Deps struct
// (PR-D, June 2026). Returns *Service + error. The legacy signature
// (6 positional args, no error) is replaced by Deps{} + error; the pre-existing
// late-bind SetDispatcher ordering hazard is gone because the dispatcher is
// captured at construction time.
//
// Validation order: Drive reader → storage (Dispatcher) → Log.
// Targets is NOT validated at ctor time — an empty slice is allowed
// (ad-hoc SyncFolderID still works because the explicit repo arg is
// supplied by the caller; the async drive.folder.sync job requires
// the source to be pre-listed in Targets, enforced at execution time
// in sync_jobs.go::HandleDriveFolderSyncJob).
//
// Production wiring: BuildSyncBundle in
// internal/app/build_bundles_domain.go::BuildSyncBundle constructs
// Deps{...} from the bundles and passes the outbox.Dispatcher directly
// — ordering-hazard pre-rejection lives in the composition root, and
// NewService is the second line of defence so accidental misuse from
// tests still fails loud.
func NewService(deps Deps) (*Service, error) {
	if deps.Reader == nil {
		return nil, ErrCatalogSyncNilReader
	}
	if deps.Dispatcher == nil {
		return nil, ErrCatalogSyncNilDispatcher
	}
	if deps.Log == nil {
		return nil, ErrCatalogSyncNilLog
	}
	for _, target := range deps.Targets {
		if strings.TrimSpace(target.RootFolderID) == "" {
			continue
		}
		if target.Repo == nil || target.Indexer == nil {
			return nil, fmt.Errorf("%w: source=%q", ErrCatalogSyncInvalidTarget, target.Source)
		}
	}

	return &Service{
		reader:     deps.Reader,
		log:        deps.Log,
		targets:    deps.Targets,
		assetTree:  deps.AssetTree,
		dispatcher: deps.Dispatcher,
	}, nil
}
