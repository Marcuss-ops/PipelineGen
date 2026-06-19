package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/batch"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	defaults "github.com/Marcuss-ops/PipelineGen/internal/platform"
	corid "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

func (h *ScriptFlowHandler) GetBatchProgress(c *gin.Context) {
	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	jobID := strings.TrimSpace(c.Query("job_id"))
	if jobID == "" {
		apiutil.BadRequest(c, "job_id query parameter is required")
		return
	}

	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		apiutil.NotFound(c, fmt.Sprintf("job not found: %v", err))
		return
	}

	events, err := h.jobsSvc.ListEvents(c.Request.Context(), jobID)
	if err != nil {
		h.log.Warn("failed to list job events for batch progress", zap.String("job_id", jobID), zap.Error(err))
		events = nil
	}

	var currentPhase string
	translationProgress := make(map[string]string)
	var chaptersTotal, chaptersDone int

	if len(events) > 0 {
		lastEvent := events[len(events)-1]
		currentPhase = lastEvent.Message

		for _, ev := range events {
			msg := ev.Message
			if strings.Contains(msg, "Translating to") {
				parts := strings.Split(msg, "Translating to ")
				if len(parts) > 1 {
					langCode := strings.TrimSuffix(strings.TrimSpace(parts[1]), "...")
					if langCode != "" {
						translationProgress[langCode] = "in_progress"
					}
				}
			}
			if strings.Contains(msg, "Completed!") {
				for k := range translationProgress {
					translationProgress[k] = "completed"
				}
			}
			if strings.Contains(msg, "Processing chapter") {
				if _, errFmt := fmt.Sscanf(msg, "Processing chapter %d of %d", &chaptersDone, &chaptersTotal); errFmt == nil && chaptersTotal > 0 {
					if chaptersDone > 0 {
						chaptersDone = chaptersDone - 1
					}
				}
			}
		}
	}

	resp := gin.H{
		"job_id":         job.ID,
		"status":         job.Status,
		"progress":       job.Progress,
		"current_phase":  currentPhase,
		"translations":   translationProgress,
		"chapters_total": chaptersTotal,
		"chapters_done":  chaptersDone,
	}

	if job.Status == jobservice.StatusFailed && job.Error != "" {
		resp["error"] = job.Error
	}

	if job.Status == jobservice.StatusSucceeded && len(job.Result) > 0 {
		var resultObj map[string]any
		if err := json.Unmarshal(job.Result, &resultObj); err == nil {
			summary := gin.H{}
			if docURL, ok := resultObj["doc_url"]; ok {
				summary["doc_url"] = docURL
			}
			if trans, ok := resultObj["translations"]; ok {
				summary["translations"] = trans
			}
			if fc, ok := resultObj["failed_chapter_count"]; ok {
				summary["failed_chapter_count"] = fc
			}
			resp["result_summary"] = summary
		}
	}

	apiutil.OK(c, resp)
}

func (h *ScriptFlowHandler) GenerateBatch(c *gin.Context) {
	if h.generator == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "script generator not initialized")
		return
	}
	if h.batchService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "batch service not initialized")
		return
	}

	scriptsCfg := config.ScriptsConfig{}
	if h.cfg != nil {
		scriptsCfg = h.cfg.Scripts.WithDefaults()
	}

	req, ok := BindJSON[batch.GenerateBatchRequest](c)
	if !ok {
		return
	}
	if req.Language == "" {
		req.Language = scriptsCfg.DefaultLanguage
	}
	if req.Tone == "" {
		req.Tone = scriptsCfg.DefaultTone
	}
	if req.Duration <= 0 {
		req.Duration = scriptsCfg.DefaultDurationSeconds
	}
	if req.Model == "" && h.cfg != nil {
		req.Model = h.cfg.External.OllamaModel
	}
	req.PromptVersion = defaults.String(req.PromptVersion, batch.DefaultBookPromptVersion)
	req.EditorPromptVersion = defaults.String(req.EditorPromptVersion, batch.DefaultBookEditorPromptVersion)
	req.QAPromptVersion = defaults.String(req.QAPromptVersion, batch.DefaultBookQAPromptVersion)

	// ChannelID: optional in the request. Default to the batch channel from config
	// (cfg.scripts.batch_channel_id, default "default-batch") so a simpler request
	// body that omits channel_id still gets a valid memory-gate channel.
	if strings.TrimSpace(req.ChannelID) == "" {
		req.ChannelID = scriptsCfg.BatchChannelID
	}

	// items[].source_text: optional. Default to the topic so the LLM has source
	// material to work from. If the topic is also empty, the existing topic
	// validation in validateGenerateBatchRequest will surface a clear error.
	for i := range req.Items {
		if strings.TrimSpace(req.Items[i].SourceText) == "" {
			req.Items[i].SourceText = strings.TrimSpace(req.Items[i].Topic)
		}
	}
	for i := range req.BatchTopics {
		if strings.TrimSpace(req.BatchTopics[i].SourceText) == "" {
			req.BatchTopics[i].SourceText = strings.TrimSpace(req.BatchTopics[i].Topic)
		}
	}

	docTitle := strings.TrimSpace(req.DocTitle)
	if docTitle == "" {
		docTitle = "Untitled Batch Script"
	}

	supportedLanguages := batch.SupportedScriptLanguages(nil, "")
	if h.cfg != nil {
		supportedLanguages = batch.SupportedScriptLanguages(h.cfg.Multilingual.TranslateLanguages, h.cfg.Multilingual.SourceLanguage)
	}
	effectiveFolderID := strings.TrimSpace(req.DriveFolderID)
	if effectiveFolderID == "" {
		if h.cfg != nil {
			effectiveFolderID = strings.TrimSpace(h.cfg.Drive.BooksFolder())
		}
		if effectiveFolderID == "" {
			effectiveFolderID = h.driveFolderID
		}
	}

	if validationErrs := batch.ValidateGenerateBatchRequest(&req, effectiveFolderID, supportedLanguages); len(validationErrs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid_request", "details": validationErrs})
		return
	}

	if req.Async {
		if h.jobsSvc == nil {
			apiutil.InternalError(c, fmt.Errorf("job system not available"))
			return
		}
		h.log.Info("enqueuing async script generate batch job", zap.String("title", docTitle))

		payloadBytes, err := json.Marshal(req)
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to marshal job payload: %w", err))
			return
		}
		var payloadMap map[string]any
		if err := json.Unmarshal(payloadBytes, &payloadMap); err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to parse job payload map: %w", err))
			return
		}

		activeKey := "script_generate_batch_" + docTitle
		if idemKey := strings.TrimSpace(c.GetHeader("Idempotency-Key")); idemKey != "" {
			activeKey = "idem:" + idemKey
			h.log.Info("using Idempotency-Key for batch dedup",
				zap.String("title", docTitle),
				zap.String("idempotency_key", idemKey),
			)
		}

		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type:          "script.generate_batch",
			Priority:      5,
			ActiveKey:     activeKey,
			Payload:       payloadMap,
			CorrelationID: corid.FromContext(c.Request.Context()),
		})
		if err != nil {
			h.log.Error("failed to enqueue batch script job", zap.Error(err))
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, gin.H{
			"ok":         true,
			"async":      true,
			"job_id":     job.ID,
			"status":     string(job.Status),
			"message":    "Batch script generation enqueued. Poll /api/jobs/" + job.ID + "/full for status.",
			"status_url": "/api/jobs/" + job.ID + "/full",
		})
		return
	}

	requestTimeout := time.Duration(scriptsCfg.BatchTimeoutSeconds) * time.Second
	if req.RequestTimeout > 0 {
		requestTimeout = time.Duration(req.RequestTimeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	result, err := h.batchService.Execute(ctx, &req, nil)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}
