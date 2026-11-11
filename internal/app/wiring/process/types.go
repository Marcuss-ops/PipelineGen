package process

import (
	"go.uber.org/zap"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	qdrantmaintenance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
	qdranthealth "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/health"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
	qdranttransport "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// ProcessQdrantBundle groups Qdrant projection/runtime dependencies used by
// the media execution plane.
type ProcessQdrantBundle struct {
	CollectionManager *collections.CollectionManager
	QdrantDeleter     jobsoutbox.VectorPointDeleter
	QdrantRuntime     *qdrant.QdrantRuntime
	VectorSvc         assetsearch.VectorStorePort
	QdrantClient      *qdranttransport.Client
	QdrantHealthProbe *qdranthealth.Probe
	LocatorCleaner    *qdrantmaintenance.LocatorCleaner
	QdrantSearcher    *qdrantsearch.Searcher
}

// BuildQdrantSubsystems projects the optional Qdrant runtime into the process
// bundle. Qdrant stays an auxiliary projection/runtime here; media authority
// remains with the canonical PostgreSQL media store.
func BuildQdrantSubsystems(qd *QdrantDeps, cfg *config.Config, log *zap.Logger) ProcessQdrantBundle {
	if qd == nil {
		return ProcessQdrantBundle{}
	}
	if qd.Runtime == nil {
		if log != nil {
			log.Info("QDRANT-003: Qdrant disabled — no Qdrant components wired (BuildProcessBundle)")
		}
		return ProcessQdrantBundle{QdrantDeleter: qd.QdrantDeleter}
	}
	runtime := qd.Runtime
	if log != nil {
		log.Info("QDRANT-005 PR4: HealthProbe + LocatorCleaner + Searcher + CollectionManager sourced from single QdrantRuntime (BuildProcessBundle)",
			zap.String("qdrant_url", cfg.Qdrant.BaseURL),
			zap.String("schema_version", runtime.Schema.Version))
		log.Info("QDRANT-004 PR4: VectorStorePort sourced from single QdrantRuntime.SearchAdapter (BuildProcessBundle)")
	}
	return ProcessQdrantBundle{
		CollectionManager: runtime.Manager,
		QdrantDeleter:     qd.QdrantDeleter,
		QdrantRuntime:     runtime,
		VectorSvc:         runtime.SearchAdapter,
		QdrantClient:      runtime.Client,
		QdrantHealthProbe: runtime.Health,
		LocatorCleaner:    runtime.Cleaner,
		QdrantSearcher:    runtime.Searcher,
	}
}

// ProcessBundle holds the heavy media-processing adapters.
type ProcessBundle struct {
	ProcessQdrantBundle
	MediaProcessor     detail.MediaProcessor
	ClipIndexerService *clipindexer.Service
	VLMClient          *vlm.Client
}

// QdrantDeps is the small pre-phase dependency bundle used by outbox wiring.
type QdrantDeps struct {
	Runtime            *qdrant.QdrantRuntime
	ClipIndexerService *clipindexer.Service
	QdrantDeleter      jobsoutbox.VectorPointDeleter
}
