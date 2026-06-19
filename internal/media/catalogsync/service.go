package catalogsync

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	uploaddrive "github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
)

type Target struct {
	Name         string
	RootFolderID string
	Source       string
	MediaType    string
	Repo         *sqlite.ClipsRepository
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

type Service struct {
	uploader    *uploaddrive.Uploader
	log         *zap.Logger
	targets     []Target
	assetIndex  *assetindex.Service
	assetTree   *assettree.Service
	clipIndexer *clipindexer.Service
	// dispatcher is the canonical ingestion entry point. When non-nil,
	// upsertPreservingExisting routes media_assets upserts + outbox
	// enqueues through Dispatcher.EnqueueAndIndex (atomic), replacing the
	// legacy `repo.UpsertClip; concurrent.SafeGoFunc(IndexClip)` pattern.
	//
	// Initial nil so existing tests (which construct `Service{}` directly
	// or via NewService without an outbox) keep their legacy behaviour.
	// Production wiring sets it via SetDispatcher after composeIntegration
	// creates the Dispatcher. See internal/infrastructure/database/sqlite/outbox
	// (package) for the contract.
	dispatcher *outbox.Dispatcher
	mu         sync.Mutex
}

func NewService(uploader *uploaddrive.Uploader, targets []Target, assetIndex *assetindex.Service, assetTree *assettree.Service, clipIndexer *clipindexer.Service, log *zap.Logger) *Service {
	return &Service{
		uploader:    uploader,
		log:         log,
		targets:     targets,
		assetIndex:  assetIndex,
		assetTree:   assetTree,
		clipIndexer: clipIndexer,
	}
}

// SetDispatcher installs the canonical outbox dispatcher. After installation,
// upsertPreservingExisting routes ingestion through Dispatcher.EnqueueAndIndex
// — atomic upsert + outbox enqueue. The legacy SafeGoFunc(IndexClip) path is
// only used when s.dispatcher is nil (partial bring-up / unit tests).
//
// Concurrency: the s.dispatcher field is read directly inside
// upsertPreservingExisting WITHOUT re-acquiring s.mu — doing so would deadlock
// because s.mu is already held by the public SyncAll/SyncSource/SyncFolderID
// callers (sync.Mutex is non-reentrant). SetDispatcher is safe to call at
// startup because the field has a happens-before relationship with any
// later Sync call (WireServices → handler registration → first external
// request). Production wiring calls SetDispatcher exactly once at startup.
func (s *Service) SetDispatcher(d *outbox.Dispatcher) {
	s.dispatcher = d
}
