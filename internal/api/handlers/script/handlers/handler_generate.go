package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers/script"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/service/artlist"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/models"
)

func (h *ScriptDocsHandler) generate(c *gin.Context, forcePreview bool) {
	startedAt := time.Now()
	zap.L().Info("script-docs generate started",
		zap.Bool("preview", forcePreview),
	)

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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	generateStarted := time.Now()
	document, err := script.BuildScriptDocument(ctx, h.generator, req, h.dataDir, h.pythonScriptsDir, h.nodeScraperDir, h.StockDriveRepo, h.ArtlistRepo, h.clipsOnlyRepo, h.artlistService, h.imgService, h.assocService)
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

	if forcePreview {
		path, err := h.savePreview(document.Title, document.Content)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, gin.H{
			"preview_only": true,
			"title":        document.Title,
			"full_content": document.Content,
			"preview_path": path,
			"timeline":     document.Timeline,
		})
		return
	}

	var docID, docURL string
	if h.docClient == nil {
		zap.L().Error("doc creation requested but google docs client is not initialized")
		apiutil.Error(c, http.StatusServiceUnavailable, "google docs client not initialized; cannot publish document")
		return
	}

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

	// Save script to database if repository is available
	if h.scriptsRepo != nil {
		h.saveScriptToDB(ctx, req, document)
	}

	// Generate voiceover if requested
	var voResult interface{}
	if req.Voiceover && h.voService != nil {
		voiceoverStarted := time.Now()
		filename := strings.ReplaceAll(req.Topic, " ", "_") + ".mp3"
		res, err := h.voService.Generate(ctx, narrativeOnly(document.Content), req.Language, filename)
		if err != nil {
			zap.L().Warn("voiceover generation failed", zap.Error(err), zap.Duration("elapsed", time.Since(voiceoverStarted)))
		} else {
			zap.L().Info("voiceover generation completed", zap.Duration("elapsed", time.Since(voiceoverStarted)))
			voResult = res
		}
	}

	// Trigger background harvest for search suggestions
	h.triggerBackgroundHarvest(ctx, document)

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
	marker := types.MarkerNarrator
	if idx := strings.Index(content, marker); idx != -1 {
		part := content[idx+len(marker):]
		if nextIdx := strings.Index(part, types.MarkerTimeline); nextIdx != -1 {
			return strings.TrimSpace(part[:nextIdx])
		}
		return strings.TrimSpace(part)
	}
	return content
}

// saveScriptToDB saves the generated script to the database
func (h *ScriptDocsHandler) saveScriptToDB(ctx context.Context, req script.ScriptDocsRequest, document *script.ScriptDocument) {
	sections := make([]scripts.ScriptSectionRecord, 0, len(document.Sections))
	for i, sec := range document.Sections {
		if sec.Title == "🧾 Metadata" {
			continue
		}
		sections = append(sections, scripts.ScriptSectionRecord{
			SectionType:  sec.Title,
			SectionTitle: sec.Title,
			Content:      sec.Body,
			SortOrder:    i,
		})
	}

	scriptRec := &scripts.ScriptRecord{
		Topic:          req.Topic,
		Duration:       req.Duration,
		Language:       req.Language,
		Template:       req.Template,
		Mode:           "modular",
		NarrativeText:  document.Content,
		TimelineJSON:   "",
		EntitiesJSON:   "",
		MetadataJSON:   "",
		FullDocument:   document.Content,
		ModelUsed:      "",
		OllamaBaseURL:  "",
		Version:        1,
		ParentScriptID: nil,
		IsDeleted:      false,
	}
	if client := h.generator.GetClient(); client != nil {
		scriptRec.ModelUsed = client.Model()
		scriptRec.OllamaBaseURL = client.BaseURL()
	}

	scriptID, err := h.scriptsRepo.SaveScript(scriptRec, sections, nil)
	if err != nil {
		zap.L().Error("Failed to save script to database", zap.Error(err))
		return
	}

	zap.L().Info("Script saved to database", zap.Int64("script_id", scriptID), zap.String("topic", req.Topic))
}

func (h *ScriptDocsHandler) savePreview(title, content string) (string, error) {
	dir := h.dataDir
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	scriptsDir := filepath.Join(dir, "scripts")
	// Ensure the scripts directory exists
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create scripts directory: %w", err)
	}

	path := script.BuildPreviewPath(scriptsDir, title)
	if err := script.WritePreview(path, title, content); err != nil {
		return "", err
	}
	return path, nil
}

// triggerBackgroundHarvest enqueues jobs for background harvesting based on search suggestions
func (h *ScriptDocsHandler) triggerBackgroundHarvest(ctx context.Context, document *script.ScriptDocument) {
	if h.artlistService == nil || h.jobsService == nil || document == nil || document.Timeline == nil {
		return
	}

	uniqueTags := make(map[string]struct{})
	for _, seg := range document.Timeline.Segments {
		for _, tag := range seg.SearchSuggestions {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				uniqueTags[tag] = struct{}{}
			}
		}
	}

	if len(uniqueTags) == 0 {
		return
	}

	zap.L().Info("enqueueing background harvest jobs for suggestions", zap.Int("tag_count", len(uniqueTags)))

	jobCodec := artlist.JobCodec{}
	for tag := range uniqueTags {
		req := &artlist.RunTagRequest{
			Term:         tag,
			Limit:        3,
			Strategy:     "verify",
			ClipDuration: 7,
			Width:        1920,
			Height:       1080,
			FPS:          30,
		}
		payload := jobCodec.PayloadFromRequest(req)

		job, err := h.jobsService.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type:     models.JobTypeArtlistRun,
			Payload:  payload,
			Priority: 5, // Lower priority for background tasks
		})
		if err != nil {
			zap.L().Error("failed to enqueue background harvest job", zap.String("tag", tag), zap.Error(err))
		} else {
			zap.L().Info("enqueued background harvest job", zap.String("tag", tag), zap.String("job_id", job.ID))
		}
	}
}
