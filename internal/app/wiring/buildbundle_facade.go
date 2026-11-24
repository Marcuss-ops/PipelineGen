package wiring

import (
	"context"
	"database/sql"

	stockwiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/stock"
	buildbundle "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/buildbundle"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	texttrackspkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	assetchannels "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	imagesrepo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

func BuildRepoBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, mediaPostgres ...*sql.DB) (*RepoBundle, error) {
	return buildbundle.BuildRepoBundle(ctx, cfg, dbs, log, mediaPostgres...)
}

func BuildSearchBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, repos *RepoBundle) (*SearchBundle, error) {
	return buildbundle.BuildSearchBundle(ctx, cfg, dbs, log, repos)
}

func BuildUtilityBundle(cfg *config.Config, db, jobsDB *sqlite.SQLiteDB, storagePlanes func(context.Context) map[string]systemhealth.CheckResult, driveReader drive.Reader, publisher delivery.Publisher, jobsSvc *appjobs.Service, ollamaClient *client.Client, outboxPool *outboxevents.Pool, log *zap.Logger) *UtilityBundle {
	return buildbundle.BuildUtilityBundle(cfg, db, jobsDB, storagePlanes, driveReader, publisher, jobsSvc, ollamaClient, outboxPool, log)
}

func BuildAIBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, driveBundle *DriveBundle, mediaDB *sql.DB) (*AIBundle, error) {
	return buildbundle.BuildAIBundle(ctx, cfg, dbs, log, driveBundle, mediaDB)
}

func BuildDomainBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, driveBundle *DriveBundle, repos *RepoBundle, media *MediaRepoBundle, search *SearchBundle, process *ProcessBundle, ai *AIBundle, outbox *OutboxBundle, mediaConfig mediaexec.ExecutionConfig, driveUploadGate *concurrent.FairSemaphore, mediaPostgres ...*sql.DB) (*DomainBundle, error) {
	return buildbundle.BuildDomainBundle(ctx, cfg, dbs, log, driveBundle, repos, media, search, process, ai, outbox, mediaConfig, driveUploadGate, mediaPostgres...)
}

func BuildOutboxBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, repos *RepoBundle, qd *IndexDeps, jobs *JobsBundle, voiceoverDriver jobs.VoiceoverCleanupDriver, stagingSvc staging.Store, repo detail.ArtifactStageRepository, drivePublisher delivery.Publisher, driveDeleter jobs.DriveDeleter, mediaPostgres *sql.DB) (*OutboxBundle, IOpaqueStartFunc, error) {
	return buildbundle.BuildOutboxBundle(ctx, cfg, dbs, log, repos, qd, jobs, voiceoverDriver, stagingSvc, repo, drivePublisher, driveDeleter, mediaPostgres)
}

func BuildProcessBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, repos *RepoBundle, publisher delivery.Publisher, outbox *OutboxBundle, deps *IndexDeps, mediaPG *sql.DB, mediaConfig mediaexec.ExecutionConfig) (*ProcessBundle, error) {
	return buildbundle.BuildProcessBundle(ctx, cfg, dbs, log, repos, publisher, outbox, deps, mediaPG, mediaConfig)
}

func BuildMaintBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, driveBundle *DriveBundle, repos *RepoBundle, search *SearchBundle, jobs *JobsBundle, outbox *OutboxBundle, sourceCatalog *artifacts.SourceCatalog) (*MaintBundle, error) {
	return buildbundle.BuildMaintBundle(ctx, cfg, dbs, log, driveBundle, repos, search, jobs, outbox, sourceCatalog)
}

func BuildStagingBundle(dbs *Databases, cfg *config.Config, log *zap.Logger) (*StagingBundle, error) {
	return buildbundle.BuildStagingBundle(dbs, cfg, log)
}

func BuildArtifactFinalizeBundle(staging *StagingBundle, log *zap.Logger) (*FinalizerBundle, error) {
	return buildbundle.BuildArtifactFinalizeBundle(staging, log)
}

func BuildTextTrackBundle(cfg *config.Config, repos *RepoBundle, ai *AIBundle, outbox *OutboxBundle, acquirePorts *AcquirePorts, publisher delivery.Publisher, log *zap.Logger) (*TextTrackBundle, error) {
	return buildbundle.BuildTextTrackBundle(cfg, repos, ai, outbox, acquirePorts, publisher, log)
}

func BuildStockBundle(deps StockBundleDeps) (*stockwiring.StockPipelineWiring, error) {
	return buildbundle.BuildStockBundle(deps)
}

func BuildJobsBundle(db *sqlite.SQLiteDB, log *zap.Logger, voiceoverRepo *assetchannels.VoiceoversRepository, imagesRepo *imagesrepo.ImagesRepository, driveUploader *drive.Uploader, driveLifecycle drive.FileLifecycle) (*JobsBundle, error) {
	return buildbundle.BuildJobsBundle(db, log, voiceoverRepo, imagesRepo, driveUploader, driveLifecycle)
}

func NewMediaRepoBundle(pgDB *sql.DB, textTrackRepo detail.TextTrackRepository) *MediaRepoBundle {
	return buildbundle.NewMediaRepoBundle(pgDB, textTrackRepo)
}

func NewSubtitleRootLayoutResolver(rootID string) texttracks.SubtitleFolderResolver {
	return buildbundle.NewSubtitleRootLayoutResolver(rootID)
}

func WireTextTracksFanOut(textTracks *TextTrackBundle, jobsService texttracks.MaterializeEnqueuer, log *zap.Logger) {
	buildbundle.WireTextTracksFanOut(textTracks, jobsService, log)
}

func WireTextTrackJobBindings(textTracks *TextTrackBundle, jobsBundle *JobsBundle) error {
	return buildbundle.WireTextTrackJobBindings(textTracks, jobsBundle)
}

func BuildLanguageRegistry(ml config.MultilingualConfig) (asset.LanguageRegistry, error) {
	return buildbundle.BuildLanguageRegistry(ml)
}

func SubtitleAcquisitionLanguages(ml config.MultilingualConfig) string {
	return buildbundle.SubtitleAcquisitionLanguages(ml)
}

func BuildMultilingualLanguageCSV(ml config.MultilingualConfig, filter func(asset.LanguageSpec) bool) (string, error) {
	return buildbundle.BuildMultilingualLanguageCSV(ml, filter)
}

func ActiveMultilingualConfig(cfg *config.Config) config.MultilingualConfig {
	return buildbundle.ActiveMultilingualConfig(cfg)
}

func ResolveTranslationModel(cfg *config.Config) string {
	return buildbundle.ResolveTranslationModel(cfg)
}

func ValidateCriticalHandlers(svc *appjobs.Service, log *zap.Logger, handlers []CriticalHandler) error {
	return buildbundle.ValidateCriticalHandlers(svc, log, handlers)
}

func newCanonicalAssetCommitter(db *sql.DB, log *zap.Logger) (persistence.CanonicalAssetWriter, error) {
	return buildbundle.NewCanonicalAssetCommitter(db, log)
}

func InitDatabases(ctx context.Context, cfg *config.Config, log *zap.Logger) (*Databases, error) {
	return buildbundle.InitDatabases(ctx, cfg, log)
}

func RunAllMigrations(dbs *Databases, log *zap.Logger) error {
	return buildbundle.RunAllMigrations(dbs, log)
}

func buildIndexDeps(cfg *config.Config) *IndexDeps { return buildbundle.BuildIndexDeps(cfg) }

func newPostgresMediaAssetLister(db *sql.DB) texttrackspkg.MediaAssetLister {
	return buildbundle.NewPostgresMediaAssetLister(db)
}

func wireYoutubeCatalogJobBindings(sync *SyncBundle, domains *DomainBundle, jobs *JobsBundle, log *zap.Logger) error {
	return buildbundle.WireYoutubeCatalogJobBindings(sync, domains, jobs, log)
}

func wireVoiceoverJobBindings(domains *DomainBundle, jobs *JobsBundle, log *zap.Logger) error {
	return buildbundle.WireVoiceoverJobBindings(domains, jobs, log)
}

func appendYoutubeCatalogCriticalValidators(sync *SyncBundle, domains *DomainBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
	buildbundle.AppendYoutubeCatalogCriticalValidators(sync, domains, jobs, validators)
}

func appendVoiceoverCriticalValidators(domains *DomainBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
	buildbundle.AppendVoiceoverCriticalValidators(domains, jobs, validators)
}

func mediaDuplicateGroupReaderFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaDuplicateGroupReader {
	return buildbundle.MediaDuplicateGroupReaderFromCommitter(committer)
}

func whisperBridgeVersion(scriptPath string) string {
	return buildbundle.WhisperBridgeVersion(scriptPath)
}

func mediaDetailsReaderFromCommitter(committer persistence.AssetCommitter) *pgmedia.AssetDetailsReader {
	return buildbundle.MediaDetailsReaderFromCommitter(committer)
}

func mediaDriveFileListerFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaDriveFileLister {
	return buildbundle.MediaDriveFileListerFromCommitter(committer)
}

func mediaEngineHandle(committer persistence.AssetCommitter) pgmedia.EngineHandle {
	return buildbundle.MediaEngineHandle(committer)
}

func newClipsRegistry(querySvc artifacts.AssetDetailsReader, processing persistence.AssetProcessingWriter, committer persistence.AssetCommitter, log *zap.Logger) *artifacts.ClipsRegistry {
	return buildbundle.NewClipsRegistry(querySvc, processing, committer, log)
}

func newYouTubeAssetWriter(outbox *OutboxBundle, repos *RepoBundle, committer persistence.AssetCommitter) youtube.AssetWriter {
	return buildbundle.NewYouTubeAssetWriter(outbox, repos, committer)
}

func NewLifecycleFromDeps(deps *AssetLifecycleDeps, log *zap.Logger) *lifecycle.Service {
	return buildbundle.NewLifecycleFromDeps(deps, log)
}

func buildSourceAwareDownloader(cfg *config.Config, db, cacheDB *sql.DB, log *zap.Logger) (assets.MediaDownloader, error) {
	return buildbundle.BuildSourceAwareDownloader(cfg, db, cacheDB, log)
}

var WireAcquisitionStager = buildbundle.WireAcquisitionStager

func wireDurableOverlayPublisher(root *ComposeRoot, direct scriptgen.OverlayArtifactPublisher) (scriptgen.OverlayArtifactPublisher, error) {
	return buildbundle.WireDurableOverlayPublisher(buildbundle.OverlayPublisherDeps{
		Outbox: root.Outbox, Jobs: root.Jobs, ObservabilityDB: root.ObservabilityDB,
	}, direct)
}

func newVisionVerdictClient(cfg *config.Config, scriptClient *client.Client) *client.Client {
	return buildbundle.NewVisionVerdictClient(cfg, scriptClient)
}
