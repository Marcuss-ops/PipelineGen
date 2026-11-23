package voiceover

// deps.go owns the typed input contracts of the voiceover module.
//
// NewFinalAudioPublisher took five positional arguments where two of them are
// publisher/committer ports of different interfaces and a third is a
// concurrency gate. BuildVoiceoverDestResolvers took four, mixing an asset
// resolver with a config and a directory. Named fields make the composition
// site state which handle is which, instead of relying on argument order
// (BON-03 step B).

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// FinalAudioPublisherDeps is the input contract of NewFinalAudioPublisher.
// DB, Publisher and Committer are mandatory: the constructor returns nil when
// one of them is missing. Gate may be nil (no fairness limit).
type FinalAudioPublisherDeps struct {
	DB        *sql.DB
	Publisher delivery.Publisher
	Committer persistence.AssetCommitter
	Gate      *concurrent.FairSemaphore
	Log       *zap.Logger
}

// VoiceoverDestResolversDeps is the input contract of
// BuildVoiceoverDestResolvers. DestResolver may be nil: only group-based
// routing needs the asset tree, and that path fails with a typed error.
type VoiceoverDestResolversDeps struct {
	DestResolver asset.Resolver
	Config       *config.Config
	VoiceoverDir string
	Log          *zap.Logger
}
