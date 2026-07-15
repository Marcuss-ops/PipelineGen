// Package stockpipeline owns constructor input types for Service.
package stockpipeline

import (
	"database/sql"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// PipelineConfig holds configuration for the stock pipeline run.
type PipelineConfig struct {
	ChunkDuration  int
	MaxResults     int
	EffectInterval int
	EffectsDir     string
}

// DefaultPipelineConfig returns a PipelineConfig with sensible defaults.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		ChunkDuration:  25,
		MaxResults:     25,
		EffectInterval: 4,
		EffectsDir:     "assets/effects/EffettiVisiv",
	}
}

// StorageDeps owns canonical media state and indexing persistence.
type StorageDeps struct {
	ClipsRepo  stockClipsSearchTermUpdater
	AssetIndex stockAssetIndexUpserter
	Dispatcher stockChunkDispatcher
}

// MediaDeps owns CPU media transformation.
type MediaDeps struct {
	Cutter   VideoCutter
	Renderer StockRenderer
}

// OperationalDeps groups execution ports that support publishing completion,
// source acquisition, durable step state and optional channel discovery.
type OperationalDeps struct {
	Finalizer     finalization.JobFinalizer
	SourceStager  acquisition.SourceStager
	DB            *sql.DB
	ChannelLister ChannelLister
}

// Deps is the immutable constructor input for Service. Storage, Media and
// Operational are real capability boundaries; no late-binding setters exist.
type Deps struct {
	Cfg       *config.Config
	Log       *zap.Logger
	Publisher delivery.Publisher
	Storage   StorageDeps
	Media     MediaDeps
	Jobs      *appjobs.Service
	OperationalDeps
}
