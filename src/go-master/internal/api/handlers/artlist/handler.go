// Package artlistpipeline handles the full Artlist pipeline: text → clips → video.
package artlistpipeline

import (
	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipcache"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/upload/drive"
)

// Handler orchestrates Artlist clip search, download, conversion, and Drive upload.
type Handler struct {
	artlistSrc    *clip.ArtlistSource
	artlistDB     *artlistdb.ArtlistDB
	driveClient   *drive.Client
	clipCache     *clipcache.ClipCache
	keywordPool   *KeywordPool
	statsStore    *StatsStore
	queryExpander *QueryExpander
	downloadDir   string
	ytDlpPath     string
	ffmpegPath    string
	outputDir     string
}

// New creates a new Artlist pipeline handler.
func New(
	artlistSrc *clip.ArtlistSource,
	artlistDB *artlistdb.ArtlistDB,
	driveClient *drive.Client,
	ollamaClient *ollama.Client,
	clipCache *clipcache.ClipCache,
	keywordPool *KeywordPool,
	statsStore *StatsStore,
	downloadDir, ytDlpPath, ffmpegPath, outputDir string,
) *Handler {
	return &Handler{
		artlistSrc:    artlistSrc,
		artlistDB:     artlistDB,
		driveClient:   driveClient,
		clipCache:     clipCache,
		keywordPool:   keywordPool,
		statsStore:    statsStore,
		queryExpander: NewQueryExpander(ollamaClient),
		downloadDir:   downloadDir,
		ytDlpPath:     ytDlpPath,
		ffmpegPath:    ffmpegPath,
		outputDir:     outputDir,
	}
}
