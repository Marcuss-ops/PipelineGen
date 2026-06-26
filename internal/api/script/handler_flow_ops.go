// Package script (api/script) — handler_flow_ops.go holds the small
// "ops-style" HTTP endpoints that aren't tied to the generation pipeline:
// section regeneration, LLM cache eviction, and media curation.
//
// PR4.F (June 2026) collapses the previous 120-line RegenerateSection
// into a thin transport. The prompt construction, Ollama invocation,
// persistence update, and Drive doc re-upload now live in
// application/scripts/section_regen.go::SectionRegenerator.
//
// PR4.F6 (June 2026) collapses the previous 60-line EvictCache into a
// thin transport. The LLM breaker reset, memory-cache eviction, and
// nil-memory fallback now live in
// application/scripts/cache_eviction_usecase.go::CacheEvictionUseCase.
// This file only parses path/body, calls the use cases, and maps
// domain errors to HTTP status codes.
//
// PR7 (June 2026): removed GenerateFromCatalog — superseded by
// POST /api/script/generate (unified endpoint, PR6).
package script

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

type RegenerateSectionRequest struct {
	Instruction string `json:"instruction" binding:"required"`
	Model       string `json:"model,omitempty"`
}

// RegenerateSection handles POST /api/script/:id/sections/:section_id/regenerate.
//
// PR4.F (June 2026): thin transport — all business logic now lives in
// scripts.SectionRegenerator. This handler is responsible only for:
//   - parsing path params + JSON body
//   - binding the typed request through a validate-on-bind check
//   - calling the use case
//   - translating domain errors into HTTP status codes
//   - serializing the typed result to JSON
//
// The handler is intentionally short. Adding logic here is a code smell —
// extend scripts.SectionRegenerator instead.
func (h *ScriptFlowHandler) RegenerateSection(c *gin.Context) {
	scriptIDStr := c.Param("id")
	sectionIDStr := c.Param("section_id")

	scriptID, err := strconv.ParseInt(scriptIDStr, 10, 64)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid script ID")
		return
	}
	sectionID, err := strconv.ParseInt(sectionIDStr, 10, 64)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid section ID")
		return
	}

	var req RegenerateSectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	if h.sectionRegen == nil {
		api.Error(c, http.StatusServiceUnavailable, "section regenerator not initialized")
		return
	}

	result, err := h.sectionRegen.Regenerate(c.Request.Context(), scripts.SectionRegenRequest{
		ScriptID:    scriptID,
		SectionID:   sectionID,
		Instruction: req.Instruction,
		Model:       req.Model,
	})
	if err != nil {
		h.mapRegenError(c, scriptID, sectionID, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"section_id": result.SectionID,
		"title":      result.Title,
		"content":    result.Content,
	})
}

// mapRegenError translates a use-case error into an HTTP response.
// Domain typed errors map to specific codes; everything else falls through
// to a 500 Internal Server Error. The error log at handler level (not the
// use case) carries the request_id so logs can be correlated with the
// client request, while the use case logs the structural error chain.
func (h *ScriptFlowHandler) mapRegenError(c *gin.Context, scriptID, sectionID int64, err error) {
	switch {
	case errors.Is(err, scripts.ErrSectionNotFound):
		api.Error(c, http.StatusNotFound, "section not found")
	case errors.Is(err, scripts.ErrScriptNotFound):
		api.Error(c, http.StatusNotFound, "script not found")
	case errors.Is(err, scripts.ErrSectionScriptMismatch):
		api.Error(c, http.StatusBadRequest, "section does not belong to the specified script")
	case errors.Is(err, scripts.ErrEmptyGeneratorOutput):
		api.Error(c, http.StatusInternalServerError, "received empty response from generator")
	default:
		if h.log != nil {
			h.log.Error("regenerate section failed",
				zap.Int64("script_id", scriptID),
				zap.Int64("section_id", sectionID),
				zap.Error(err))
		}
		api.InternalError(c, err)
	}
}

// EvictCacheRequest is the JSON body for POST /api/script/cache/evict.
type EvictCacheRequest struct {
	Titles []string `json:"titles,omitempty"`
}

// Curate handles POST /api/script/curate.
func (h *ScriptFlowHandler) Curate(c *gin.Context) {
	if h.jobsSvc == nil {
		api.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	var req scripts.JobPayloadCurate
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid payload")
		return
	}

	req.Query = strings.TrimSpace(req.Query)
	req.Title = strings.TrimSpace(req.Title)
	if req.Query == "" {
		req.Query = req.Title
	}
	if req.Query == "" {
		api.Error(c, http.StatusBadRequest, "query is required")
		return
	}
	if req.Title == "" {
		if grp := strings.TrimSpace(req.VoiceoverGroup); grp != "" {
			req.Title = grp
		} else {
			req.Title = req.Query
		}
	}
	req.Language = "en"
	req.Languages = scripts.NormalizeLanguages(req.Languages)
	if len(req.Languages) == 0 {
		req.Languages = []string{"en"}
	}
	req.Tone = strings.TrimSpace(req.Tone)
	if req.Tone == "" {
		req.Tone = "comedy"
	}
	if req.MaxClips <= 0 {
		req.MaxClips = 10
	}
	if req.MaxClips > 30 {
		req.MaxClips = 30
	}
	if req.TargetWords <= 0 {
		req.TargetWords = 2000
	}
	if req.MinScore <= 0 {
		req.MinScore = 0.5
	}

	api.EnqueueAsync(c, h.jobsSvc, &api.EnqueueInput{
		Type:       job.TypeMediaCurate,
		Payload:    req,
		Priority:   5,
		MaxRetries: 2,
	}, "Curation queued.")
}

// EvictCache handles POST /api/script/cache/evict.
//
// PR4.F6 (June 2026): thin transport — all business logic now lives in
// scripts.CacheEvictionUseCase. This handler is responsible only for:
//   - parsing the JSON body (with the empty-body-EOF special case so
//     callers can omit titles to mean "just reset breakers")
//   - trimming + filtering empty titles before the use case
//   - calling the use case
//   - translating domain errors into HTTP status codes
//   - serializing the typed result to JSON
//
// The handler is intentionally short. Adding logic here is a code smell —
// extend scripts.CacheEvictionUseCase instead.
func (h *ScriptFlowHandler) EvictCache(c *gin.Context) {
	var req EvictCacheRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Only EOF (empty body) is treated as "evict all". Malformed JSON
		// still gets a 400 so callers can debug.
		if err.Error() != "EOF" {
			api.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		req.Titles = nil
	}

	if h.cacheEviction == nil {
		api.Error(c, http.StatusServiceUnavailable, "cache eviction use case not initialized")
		return
	}

	result, err := h.cacheEviction.Run(c.Request.Context(), scripts.CacheEvictionInput{
		Titles: scripts.TrimAndFilterTitles(req.Titles),
	})
	if err != nil {
		h.mapCacheEvictionError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"deleted_count":    result.DeletedCount,
		"evicted_titles":   result.EvictedTitles,
		"circuit_breakers": "reset",
		"models_reset":     result.CircuitBreakersReset,
	})
}

// mapCacheEvictionError translates a use-case error into an HTTP response.
// Domain typed errors map to specific codes; everything else falls through
// to a 500 Internal Server Error. The use case logs the structural error
// chain; the handler does not add log noise — every status transition is
// logged exactly once.
func (h *ScriptFlowHandler) mapCacheEvictionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, scripts.ErrCacheEvictionMissing):
		api.Error(c, http.StatusServiceUnavailable, "memory service not initialized")
	default:
		api.InternalError(c, err)
	}
}
