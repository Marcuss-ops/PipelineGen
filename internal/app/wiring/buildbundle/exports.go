package buildbundle

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	client "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

func NewCanonicalAssetCommitter(db *sql.DB, log *zap.Logger) (persistence.CanonicalAssetWriter, error) {
	return newCanonicalAssetCommitter(db, log)
}

func NewClipsRegistry(querySvc artifacts.AssetDetailsReader, processing persistence.AssetProcessingWriter, committer persistence.AssetCommitter, log *zap.Logger) *artifacts.ClipsRegistry {
	return newClipsRegistry(querySvc, processing, committer, log)
}

func MediaDetailsReaderFromCommitter(committer persistence.AssetCommitter) *pgmedia.AssetDetailsReader {
	return mediaDetailsReaderFromCommitter(committer)
}

func MediaEngineHandle(committer persistence.AssetCommitter) pgmedia.EngineHandle {
	return mediaEngineHandle(committer)
}

func WhisperBridgeVersion(scriptPath string) string {
	return whisperBridgeVersion(scriptPath)
}

func BuildIndexDeps(cfg *config.Config) *IndexDeps {
	return buildIndexDeps(cfg)
}

func NewPostgresMediaAssetLister(db *sql.DB) texttracks.MediaAssetLister {
	return newPostgresMediaAssetLister(db)
}

func WireYoutubeCatalogJobBindings(sync *SyncBundle, domains *DomainBundle, jobs *JobsBundle, log *zap.Logger) error {
	return wireYoutubeCatalogJobBindings(sync, domains, jobs, log)
}

func WireVoiceoverJobBindings(domains *DomainBundle, jobs *JobsBundle, log *zap.Logger) error {
	return wireVoiceoverJobBindings(domains, jobs, log)
}

func AppendYoutubeCatalogCriticalValidators(sync *SyncBundle, domains *DomainBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
	appendYoutubeCatalogCriticalValidators(sync, domains, jobs, validators)
}

func AppendVoiceoverCriticalValidators(domains *DomainBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
	appendVoiceoverCriticalValidators(domains, jobs, validators)
}

func MediaDuplicateGroupReaderFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaDuplicateGroupReader {
	return mediaDuplicateGroupReaderFromCommitter(committer)
}

func MediaDriveFileListerFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaDriveFileLister {
	return mediaDriveFileListerFromCommitter(committer)
}

func BuildSourceAwareDownloader(cfg *config.Config, db, cacheDB *sql.DB, log *zap.Logger) (assets.MediaDownloader, error) {
	return buildSourceAwareDownloader(cfg, db, cacheDB, log)
}

func NewVisionVerdictClient(cfg *config.Config, scriptClient *client.Client) *client.Client {
	return newVisionVerdictClient(cfg, scriptClient)
}
