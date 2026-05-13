package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers/script"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/apiutil"
)

func (h *ScriptDocsHandler) generate(c *gin.Context) {
	startedAt := time.Now()
	zap.L().Info("script-docs generate started")

	if h.generator == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "script generator not initialized")
		return
	}

	req, ok := apiutil.BindJSON[script.ScriptDocsRequest](c)
	if !ok {
		return
	}

	req.Normalize()
	zap.L().Info("script-docs request normalized",
		zap.String("topic", req.Topic),
		zap.Int("duration", req.Duration),
		zap.String("language", req.Language),
		zap.String("template", req.Template),
	)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Minute)
	defer cancel()

	generateStarted := time.Now()
	document, err := script.BuildScriptDocument(ctx, h.generator, req, h.dataDir, h.pythonScriptsDir, h.nodeScraperDir, h.StockDriveRepo, h.ArtlistRepo, h.clipsOnlyRepo, h.artlistService, h.imgService, h.assocService, h.clipResolver)
	if err != nil {
		zap.L().Error("script document generation failed",
			zap.Error(err),
			zap.Duration("elapsed", time.Since(generateStarted)),
			zap.String("topic", req.Topic),
		)
		apiutil.InternalError(c, err)
		return
	}
	zap.L().Info("script document generated",
		zap.Duration("elapsed", time.Since(generateStarted)),
		zap.Int("timeline_segments", len(document.Timeline.Segments)),
		zap.String("topic", req.Topic),
	)

	// Trigger background harvest for search suggestions
	var harvestJobIDs []string
	if h.persistSvc != nil {
		harvestJobIDs = h.persistSvc.TriggerBackgroundHarvest(ctx, document)
	}

	// Wait for harvest jobs if requested
	if req.WaitForHarvest && len(harvestJobIDs) > 0 && h.jobsService != nil {
		zap.L().Info("waiting for background harvest jobs", zap.Int("count", len(harvestJobIDs)))
		waitForJobs(ctx, h.jobsService, harvestJobIDs)

		// RE-BUILD document to include newly downloaded assets
		zap.L().Info("re-building script document after harvest")
		newDoc, err := script.BuildScriptDocument(ctx, h.generator, req, h.dataDir, h.pythonScriptsDir, h.nodeScraperDir, h.StockDriveRepo, h.ArtlistRepo, h.clipsOnlyRepo, h.artlistService, h.imgService, h.assocService, h.clipResolver)
		if err == nil {
			document = newDoc
		} else {
			zap.L().Warn("failed to re-build document after harvest", zap.Error(err))
		}
	}

	var docID, docURL string
	if h.docClient != nil {
		docStarted := time.Now()
		zap.L().Info("google doc creation started",
			zap.String("title", document.Title),
			zap.String("folder_id", h.stockRootFolder),
		)
		doc, err := h.docClient.CreateDoc(ctx, document.Title, document.Content, h.stockRootFolder)
		if err != nil {
			zap.L().Error("doc creation failed during generation",
				zap.Error(err),
				zap.Duration("elapsed", time.Since(docStarted)),
				zap.Duration("total_elapsed", time.Since(startedAt)),
				zap.String("title", document.Title),
			)
			apiutil.InternalError(c, fmt.Errorf("failed to create Google Doc: %v", err))
			return
		}
		zap.L().Info("google doc created",
			zap.Duration("elapsed", time.Since(docStarted)),
			zap.Duration("total_elapsed", time.Since(startedAt)),
			zap.String("doc_id", doc.ID),
		)
		docID = doc.ID
		docURL = doc.URL
	}

	// Save script to database if repository is available
	if h.scriptsRepo != nil {
		h.saveScriptToDB(ctx, req, document)
	}

	// Generate voiceover if requested
	var voResult interface{}
	if req.Voiceover && h.voService != nil {
		voiceoverStarted := time.Now()
		filename := req.Topic + ".mp3"
		res, err := h.voService.Generate(ctx, narrativeOnly(document.Content), req.Language, filename)
		if err != nil {
			zap.L().Warn("voiceover generation failed", zap.Error(err), zap.Duration("elapsed", time.Since(voiceoverStarted)))
		} else {
			zap.L().Info("voiceover generation completed", zap.Duration("elapsed", time.Since(voiceoverStarted)))
			voResult = res
		}
	}

	apiutil.OK(c, gin.H{
		"doc_id":       docID,
		"doc_url":      docURL,
		"docs_url":     docURL,
		"title":        document.Title,
		"full_content": document.Content,
		"timeline":     document.Timeline,
		"voiceover":    voResult,
	})

	zap.L().Info("script-docs generate completed",
		zap.Duration("total_elapsed", time.Since(startedAt)),
		zap.String("topic", req.Topic),
		zap.String("doc_id", docID),
	)
}

func narrativeOnly(content string) string {
	marker := "## 🎙️ Narrator"
	if idx := strings.Index(content, marker); idx != -1 {
		part := content[idx+len(marker):]
		if nextIdx := strings.Index(part, "## 🎬 Timeline"); nextIdx != -1 {
			return strings.TrimSpace(part[:nextIdx])
		}
		return strings.TrimSpace(part)
	}
	return content
}

func waitForJobs(ctx context.Context, svc *jobservice.Service, jobIDs []string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			allDone := true
			for _, id := range jobIDs {
				job, err := svc.Get(ctx, id)
				if err != nil {
					continue
				}
				if !job.Status.IsTerminal() {
					allDone = false
					break
				}
			}
			if allDone {
				return
			}
		}
	}
}
