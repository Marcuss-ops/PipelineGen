// Package handlers provides HTTP handlers for modular script pipeline endpoints.
package handlers

import (
	"github.com/gin-gonic/gin"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
	"strings"
	"fmt"
)

type ScriptPipelineHandler struct {
	generator       *ollama.Generator
	docClient       *drive.DocClient
	stockDB         *stockdb.StockDB
	artlistDB       *artlistdb.ArtlistDB
	artlistIndex    *scriptdocs.ArtlistIndex
	artlistSrc      *clip.ArtlistSource
	driveClient     *drive.Client
	clipSearch      *clipsearch.Service
	clipIndexer     *clip.Indexer
	stockRootFolder string
}

func NewScriptPipelineHandler(
	gen *ollama.Generator,
	dc *drive.DocClient,
	sdb *stockdb.StockDB,
	ai *scriptdocs.ArtlistIndex,
	alDB *artlistdb.ArtlistDB,
	alSrc *clip.ArtlistSource,
	driveClient *drive.Client,
	clipSearch *clipsearch.Service,
	clipIndexer *clip.Indexer,
	stockRootFolder string,
) *ScriptPipelineHandler {
	if ai == nil {
		fmt.Println("DEBUG: NewScriptPipelineHandler: artlistIndex is NIL")
	} else {
		fmt.Printf("DEBUG: NewScriptPipelineHandler: artlistIndex is LOADED with %d clips\n", len(ai.Clips))
	}
	return &ScriptPipelineHandler{
		generator:       gen,
		docClient:       dc,
		stockDB:         sdb,
		artlistDB:       alDB,
		artlistIndex:    ai,
		artlistSrc:      alSrc,
		driveClient:     driveClient,
		clipSearch:      clipSearch,
		clipIndexer:     clipIndexer,
		stockRootFolder: stockRootFolder,
	}
}

func (h *ScriptPipelineHandler) RegisterRoutes(rg *gin.RouterGroup) {
	script := rg.Group("/script-pipeline")
	{
		script.POST("/generate-text", h.GenerateText)
		script.POST("/divide", h.DivideIntoSegments)
		script.POST("/extract-entities", h.ExtractEntities)
		script.POST("/associate-stock", h.AssociateStock)
		script.POST("/associate-artlist", h.AssociateArtlist)
		script.POST("/find-keyphrases", h.FindKeyPhrases)
		script.POST("/download-clips", h.DownloadClips)
		script.POST("/translate", h.Translate)
		script.POST("/create-doc", h.CreateDocument)
		script.POST("/full", h.GenerateFullPipeline)
	}
}

// Shared helper functions
func extractPhrases(text string) (string, string) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return "", ""
	}
	if len(words) <= 3 {
		return text, text
	}
	initial := strings.Join(words[:3], " ")
	final := strings.Join(words[len(words)-3:], " ")
	return initial, final
}
