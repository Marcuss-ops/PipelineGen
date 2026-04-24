// Package handlers provides HTTP handlers for modular script pipeline endpoints.
package script

import (
	"github.com/gin-gonic/gin"

	"fmt"
	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipdb"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
)

type ScriptPipelineHandler struct {
	generator            *ollama.Generator
	docClient            *drive.DocClient
	stockDB              *stockdb.StockDB
	artlistDB            *artlistdb.ArtlistDB
	artlistIndex         *scriptdocs.ArtlistIndex
	artlistSrc           *clip.ArtlistSource
	clipDB               *clipdb.ClipDB
	driveClient          *drive.Client
	clipSearch           *clipsearch.Service
	clipIndexer          *clip.Indexer
	stockRootFolder      string
	artlistDriveFolderID string // Artlist Drive folder ID for document linking
}

func NewScriptPipelineHandler(
	gen *ollama.Generator,
	dc *drive.DocClient,
	sdb *stockdb.StockDB,
	ai *scriptdocs.ArtlistIndex,
	alDB *artlistdb.ArtlistDB,
	alSrc *clip.ArtlistSource,
	clipDB *clipdb.ClipDB,
	driveClient *drive.Client,
	clipSearch *clipsearch.Service,
	clipIndexer *clip.Indexer,
	stockRootFolder string,
	artlistDriveFolderID string,
) *ScriptPipelineHandler {
	if ai == nil {
		fmt.Println("DEBUG: NewScriptPipelineHandler: artlistIndex is NIL")
	} else {
		fmt.Printf("DEBUG: NewScriptPipelineHandler: artlistIndex is LOADED with %d clips\n", len(ai.Clips))
	}
	return &ScriptPipelineHandler{
		generator:            gen,
		docClient:            dc,
		stockDB:              sdb,
		artlistDB:            alDB,
		artlistIndex:         ai,
		artlistSrc:           alSrc,
		clipDB:               clipDB,
		driveClient:          driveClient,
		clipSearch:           clipSearch,
		clipIndexer:          clipIndexer,
		stockRootFolder:      stockRootFolder,
		artlistDriveFolderID: artlistDriveFolderID,
	}
}

func (h *ScriptPipelineHandler) RegisterRoutes(rg *gin.RouterGroup) {
	script := rg.Group("/script-pipeline")
	{
		script.POST("/generate-script", h.GenerateText)
		script.POST("/generate-doc", h.GenerateDocument)
		script.POST("/analyze", h.AnalyzeText)
		script.POST("/analyze/entities", h.AnalyzeEntities)
		script.POST("/analyze/timestamps", h.AnalyzeTimestamps)
		script.POST("/analyze/associations", h.AnalyzeAssociations)
		script.POST("/divide", h.DivideIntoSegments)
		script.POST("/plan-chapters", h.PlanChapters)
		script.POST("/extract-entities", h.ExtractEntities)
		script.POST("/associate-stock", h.AssociateStock)
		script.POST("/associate-artlist", h.AssociateArtlist)
		script.POST("/find-keyphrases", h.FindKeyPhrases)
		script.POST("/download-clips", h.DownloadClips)
		script.POST("/translate", h.Translate)
		script.POST("/create-doc", h.CreateDocument)
		script.POST("/create-doc-from-source", h.CreateDocumentFromSource)
		script.POST("/analyze/create-doc", h.AnalyzeCreateDoc)
		script.POST("/full", h.GenerateFullPipeline)
	}
}
