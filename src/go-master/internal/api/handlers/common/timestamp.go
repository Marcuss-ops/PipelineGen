// Package handlers fornisce HTTP handlers per il timestamp mapping
package common

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/timestamp"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// TimestampHandler gestisce le richieste HTTP per timestamp mapping
type TimestampHandler struct {
	service *timestamp.Service
}

// NewTimestampHandler crea un nuovo handler per timestamp mapping
func NewTimestampHandler(service *timestamp.Service) *TimestampHandler {
	return &TimestampHandler{
		service: service,
	}
}

// MapTimestampToClipsRequest rappresenta una richiesta di mapping
type MapTimestampToClipsRequest struct {
	ScriptID           string                  `json:"script_id"`
	Segments           []timestamp.TextSegment `json:"segments"`
	MediaType          string                  `json:"media_type"`
	MaxClipsPerSegment int                     `json:"max_clips_per_segment"`
	MinScore           float64                 `json:"min_score"`
	IncludeDrive       bool                    `json:"include_drive"`
	IncludeArtlist     bool                    `json:"include_artlist"`
}

// MapTimestampToClips godoc
// @Summary Mappa segmenti di testo con timestamp a clip Drive/Artlist
// @Description Collega ogni segmento di testo (con timestamp) alle clip più pertinenti da Drive e Artlist
// @Tags timestamp
// @Accept json
// @Produce json
// @Param request body MapTimestampToClipsRequest true "Richiesta di mapping"
// @Success 200 {object} timestamp.MappingResponse
// @Router /api/timestamp/map [post]
func (h *TimestampHandler) MapTimestampToClips(c *gin.Context) {
	var req MapTimestampToClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request: " + err.Error(),
		})
		return
	}

	if len(req.Segments) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "No segments provided",
		})
		return
	}

	// Default values
	if !req.IncludeDrive && !req.IncludeArtlist {
		req.IncludeDrive = true
		req.IncludeArtlist = true
	}

	// Crea mapping request
	mappingReq := &timestamp.MappingRequest{
		ScriptID:           req.ScriptID,
		Segments:           req.Segments,
		MediaType:          req.MediaType,
		MaxClipsPerSegment: req.MaxClipsPerSegment,
		MinScore:           req.MinScore,
		IncludeDrive:       req.IncludeDrive,
		IncludeArtlist:     req.IncludeArtlist,
	}

	logger.Info("Timestamp mapping requested",
		zap.String("script_id", req.ScriptID),
		zap.Int("segments", len(req.Segments)),
		zap.Bool("include_drive", req.IncludeDrive),
		zap.Bool("include_artlist", req.IncludeArtlist),
	)

	// Esegui mapping
	mapping, err := h.service.MapSegmentsToClips(c.Request.Context(), mappingReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Mapping failed: " + err.Error(),
		})
		return
	}

	// Conta clip totali
	totalClips := 0
	for _, seg := range mapping.Segments {
		totalClips += seg.ClipCount
	}

	c.JSON(http.StatusOK, timestamp.MappingResponse{
		Success:        true,
		Mapping:        mapping,
		TotalSegments:  len(mapping.Segments),
		TotalClips:     totalClips,
		AverageScore:   mapping.AverageScore,
	})
}

// RegisterRoutes registra le route per timestamp mapping
func (h *TimestampHandler) RegisterRoutes(protected *gin.RouterGroup) {
	timestampGroup := protected.Group("/timestamp")
	{
		timestampGroup.POST("/map", h.MapTimestampToClips)
	}
}
