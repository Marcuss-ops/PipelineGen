package script

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/service/scriptdocs"
)

// CreateDocument handles the creation of a script document in Google Docs.
func (h *ScriptPipelineHandler) CreateDocument(c *gin.Context) {
	var req CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	resp, err := h.createDocumentFromRequest(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// CreateDocumentPreview saves a local preview of the script document.
func (h *ScriptPipelineHandler) CreateDocumentPreview(c *gin.Context) {
	var req CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	req.PreviewOnly = true

	resp, err := h.createDocumentFromRequest(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// CreateDocumentFromSource creates a document using provided source text as script.
func (h *ScriptPipelineHandler) CreateDocumentFromSource(c *gin.Context) {
	var req CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(req.SourceText) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "source_text is required"})
		return
	}
	if strings.TrimSpace(req.Script) == "" {
		req.Script = req.SourceText
	}

	resp, err := h.createDocumentFromRequest(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ReviewDraft provides a preview-only draft of a potential document.
func (h *ScriptPipelineHandler) ReviewDraft(c *gin.Context) {
	var req ReviewDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	draft := CreateDocumentRequest{
		Title:       req.Title,
		Topic:       req.Topic,
		SourceText:  req.SourceText,
		Script:      req.SourceText,
		Language:    req.Language,
		Duration:    req.Duration,
		PreviewOnly: true,
	}
	topic := draft.Topic
	if strings.TrimSpace(topic) == "" {
		topic = draft.Title
	}
	h.enrichCreateDocumentRequest(c.Request.Context(), &draft, topic)

	c.JSON(http.StatusOK, ReviewDraftResponse{
		Ok:      true,
		Draft:   draft,
		Message: "Review the draft, edit it locally, then POST it to /script-pipeline/create-doc",
	})
}

// GenerateFullPipeline runs the entire automated script-to-doc flow.
func (h *ScriptPipelineHandler) GenerateFullPipeline(c *gin.Context) {
	var req FullPipelineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	tracker := GetProgressTracker()
	operationID := GenerateOperationID("full_pipeline")
	tracker.StartTracking(operationID)
	tracker.SendProgress(operationID, "start", "Starting full pipeline", 0.0, gin.H{"topic": req.Topic})

	reqContext := c.Request.Context()

	text := req.Text
	if text == "" {
		genReq := &ollama.TextGenerationRequest{
			SourceText: req.Topic,
			Title:      req.Topic,
			Language:   req.Language,
			Duration:   req.Duration,
			Tone:       "professional",
			Model:      "gemma3:4b",
		}
		result, err := h.generator.GenerateFromText(reqContext, genReq)
		if err != nil {
			text = req.Topic + " is an important character with an incredible story."
		} else {
			text = result.Script
		}
	}

	segments, _, err := h.buildSemanticSegments(reqContext, req.Topic, text, req.Duration, req.Language, 4)
	if err != nil {
		tracker.SendProgress(operationID, "error", "Failed to build segments", 0.0, gin.H{"error": err.Error()})
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	tracker.SendProgress(operationID, "segments_built", "Built semantic segments", 0.2, gin.H{"count": len(segments)})

	segments = enrichSegments(segments)
	tracker.SendProgress(operationID, "segments_enriched", "Enriched segments with keywords", 0.3, nil)

	if len(segments) == 0 {
		sentences := scriptdocs.ExtractSentences(text)
		avgDuration := 20
		for i, sentence := range sentences {
			segments = append(segments, Segment{
				Index:     i,
				Text:      sentence,
				StartTime: i * avgDuration,
				EndTime:   (i + 1) * avgDuration,
			})
		}
	}

	tracker.SendProgress(operationID, "extracting_entities", "Extracting entities", 0.4, nil)
	frasi, nomi, parole, images := h.extractEntitiesForPipeline(segments)
	tracker.SendProgress(operationID, "entities_extracted", "Entities extracted", 0.5, gin.H{"nomi": len(nomi)})

	tracker.SendProgress(operationID, "searching_clips", "Searching for clips", 0.6, nil)
	stockAssocs, driveAssocs, artlistAssocs, topicFolderID := h.searchClipsForPipeline(reqContext, req.Topic, segments)
	tracker.SendProgress(operationID, "clips_found", "Clips found", 0.7, gin.H{"stock": len(stockAssocs)})

	tracker.SendProgress(operationID, "building_doc", "Building document content", 0.8, nil)
	content := h.BuildDocumentContent(
		req.Topic, req.Topic, req.Duration, req.Language, text, segments,
		artlistAssocs, topicFolderID, req.Topic, driveAssocs, nil,
		frasi, nomi, parole, images, nil, nil, nil,
	)

	publishCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	tracker.SendProgress(operationID, "creating_doc", "Creating document", 0.9, nil)
	doc, err := h.docClient.CreateDoc(publishCtx, req.Topic, content, "")
	if err != nil {
		tracker.SendProgress(operationID, "error", "Failed to create document", 0.0, gin.H{"error": err.Error()})
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	tracker.SendProgress(operationID, "complete", "Pipeline completed", 1.0, gin.H{"doc_url": doc.URL})
	tracker.Complete(operationID)

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"doc_url":      doc.URL,
		"operation_id": operationID,
		"progress_url": "/api/script/progress/" + operationID,
	})
}
