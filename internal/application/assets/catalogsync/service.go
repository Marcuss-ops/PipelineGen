package catalogsync

import (
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	uploaddrive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

type Target struct {
	Name         string
	RootFolderID string
	Source       string
	MediaType    string
	Repo         *assets.ClipsRepository
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
// Sentinel errors returned by NewService validation. PR-D (June 2026):
// the ctor validates only the fields that are referenced UNCONDITIONALLY
// at runtime. Fields that have document-ed nil-safe guards in call sites
// (s.assetIndex in sync_persist.go::writeAssetIndex; s.assetTree in
// sync_prune.go::pruneMissingFolders + sync_recursive.go:79,175) are
// ACCEPTED as nil at ctor time — the optionality is part of the contract.
// Required at ctor: Uploader, Dispatcher, Log.
// Optional at ctor: AssetIndex, AssetTree, ClipIndexer, Targets.
var (
	ErrCatalogSyncNilUploader   = errors.New("catalogsync.NewService: uploader is required (Drive uploader for canonical sync — sync_targets.go:23 + sync_recursive.go dereference unconditionally)")
	ErrCatalogSyncNilDispatcher = errors.New("catalogsync.NewService: dispatcher is required (QDRANT-002 PR7 — production canonical ingest; sync_persist.go::upsertPreservingExisting dereferences unconditionally)")
	ErrCatalogSyncNilLog        = errors.New("catalogsync.NewService: log is required (zap.Logger dereferenced by every warn/error path in sync_persist.go + sync_targets.go + sync_prune.go)")
)

// Deps is the canonical constructor input for catalogsync.Service
// (PR-D, Wave 22 §D3, June 2026). Seven flat fields — under the
// AGENTS.md 8-per-bundle cap. The legacy signature took 6 positional
// arguments + a SetDispatcher late-bind; both styles are removed.
//
// Targets is a slice so it stays 1 field regardless of how many
// pre-configured targets the caller passes; defaultClipsRepo is
// derived inside NewService from the first non-nil Target.Repo.
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
//	  - Uploader    : sync_targets.go:23 + sync_recursive.go dereference
//	                  unconditionally (Drive uploader is the canonical
//	                  transport integration point).
//	  - Dispatcher  : sync_persist.go::upsertPreservingExisting calls
//	                  s.dispatcher.EnqueueAndIndex unconditionally
//	                  (QDRANT-002 PR7 \u2014 production canonical ingest).
//	  - Log         : every warn/error path dereferences s.log.
//
//	OPTIONAL (nil-safe guarded at every call site):
//	  - Targets     : empty slice == no pre-configured targets; ad-hoc
//	                  SyncFolderID paths still work via repo arg.
//	  - AssetIndex  : sync_persist.go:66 \u2014 `if s.assetIndex == nil { return }`.
//	                  TESTS may pass nil.
//	  - AssetTree   : sync_prune.go:35 + sync_recursive.go:79,175 \u2014
//	                  `if s.assetTree != nil { ... }`. nil falls back to
//	                  flat folder-id sync (no tree-bounded walks).
//	  - ClipIndexer : legacy field; not currently referenced in any
//	                  catalogsync method \u2014 retained on the Service
//	                  struct for the historical api surface; nil-safe.
//
// The 5 optional fields do NOT have typed sentinels because they don't
// fail at any documented runtime path. Adding Err*s for them would be
// a false-positive: composition wiring that legitimately wants a
// minimal-Deps Service (e.g. test fixtures) could not pass.
type Deps struct {
	Uploader    *uploaddrive.Uploader
	Targets     []Target
	AssetIndex  *assetindex.Service
	AssetTree   *assettree.Service
	ClipIndexer *clipindexer.Service
	Dispatcher  *outbox.Dispatcher
	Log         *zap.Logger
}

type Service struct {
	uploader    *uploaddrive.Uploader
	log         *zap.Logger
	targets     []Target
	assetIndex  *assetindex.Service
	assetTree   *assettree.Service
	clipIndexer *clipindexer.Service
	// defaultClipsRepo is the fallback ClipsRepository for ad-hoc sync
	// requests (e.g. source="drive") that don't have a pre-configured target.
	// Initialised from the first target's Repo in NewService.
	defaultClipsRepo *assets.ClipsRepository
	// dispatcher is the canonical ingestion entry point. Routes media_assets
	// upserts + outbox enqueues through Dispatcher.EnqueueAndIndex (atomic),
	// replacing the legacy `repo.UpsertClip; concurrent.SafeGoFunc(IndexClip)`
	// pattern. Required for production — NewService rejects nil with
	// ErrCatalogSyncNilDispatcher; upsertPreservingExisting also surfaces
	// an explicit "dispatcher is nil" runtime check as defence-in-depth.
	//
	// Wired at composition time via Deps.Dispatcher (PR-D, June 2026).
	// See internal/infrastructure/database/sqlite/outbox (package) for
	// the integration contract; BuildSyncBundle is the canonical owner.
	dispatcher *outbox.Dispatcher
	mu         sync.Mutex
}

// NewService creates a catalogsync service via the canonical Deps struct
// (PR-D, June 2026). Returns *Service + error. The legacy signature
// (6 positional args, no error) is replaced by Deps{} + error; the pre-existing
// late-bind SetDispatcher ordering hazard is gone because the dispatcher is
// captured at construction time.
//
// Validation order: transport (Uploader, ClipIndexer) → storage
// (AssetIndex, Dispatcher) → Log. Targets validates as a slice (non-nil);
// empty Targets is allowed (ad-hoc SyncFolderID still works).
//
// Production wiring: BuildSyncBundle in
// internal/app/build_bundles_domain.go::BuildSyncBundle constructs
// Deps{...} from the bundles and passes the outbox.Dispatcher directly
// — ordering-hazard pre-rejection lives in the composition root, and
// NewService is the second line of defence so accidental misuse from
// tests still fails loud.
func NewService(deps Deps) (*Service, error) {
	if deps.Uploader == nil {
		return nil, ErrCatalogSyncNilUploader
	}
	if deps.Dispatcher == nil {
		return nil, ErrCatalogSyncNilDispatcher
	}
	if deps.Log == nil {
		return nil, ErrCatalogSyncNilLog
	}

	s := &Service{
		uploader:    deps.Uploader,
		log:         deps.Log,
		targets:     deps.Targets,
		assetIndex:  deps.AssetIndex,
		assetTree:   deps.AssetTree,
		clipIndexer: deps.ClipIndexer,
		dispatcher:  deps.Dispatcher,
	}
	// Use the first non-nil target repo as the default fallback repo
	for _, t := range deps.Targets {
		if t.Repo != nil {
			s.defaultClipsRepo = t.Repo
			break
		}
	}
	return s, nil
}
