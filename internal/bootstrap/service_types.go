package bootstrap

import (
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/assettree"
	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/catalogsync"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/indexing"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/service/voiceoversync"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/internal/upload/drive"
	gdrive "google.golang.org/api/drive/v3"
)

type services struct {
	scriptGen          *ollama.Generator
	docClient          drive.DocClient
	driveClient        *gdrive.Service
	utility            *common.UtilityHandler
	scriptsRepo        *scripts.ScriptRepository
	imageRepo          *images.Repository
	imageService       *imgservice.Service
	stockDriveRepo     *clips.Repository
	artlistRepo        *clips.Repository
	clipsOnlyRepo      *clips.Repository
	monitorsRepo       *monitors.Repository
	voiceoverService   *voiceover.Service
	voiceoverSync      *voiceoversync.Service
	indexingService    *indexing.Service
	catalogRepo        *catalog.Repository
	catalogSync        *catalogsync.Service
	assocService       *association.Service
	jobsRepo           *jobrepo.Repository
	jobsService        *jobservice.Service
	jobsDispatcher     *jobservice.Dispatcher
	mediaProcessor     processor.Processor
	youtubeClipService *youtubeclip.Service
	assetIndexService  *assetindex.Service
	assetTreeService   *assettree.Service
	assetResolver      *assetindex.Resolver
}
