package clip

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// TriggerScan godoc
// @Summary Trigger a full clip index scan
// @Description Scan Google Drive and rebuild the clip index. Accessible from remote machines.
// @Tags clip-index
// @Accept json
// @Produce json
// @Param force query bool false "Force scan even if recently synced"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /clip/index/scan [post]
func (h *ClipIndexHandler) TriggerScan(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized. Check Drive credentials.",
		})
		return
	}

	// Use scanner if available for consistent results tracking
	if h.scanner != nil {
		result := h.scanner.TriggerManualScan(c.Request.Context())
		if !result.Success {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":          false,
				"error":       result.Error,
				"scan_result": result,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":          true,
			"message":     "Clip index scan completed successfully",
			"stats":       h.indexer.GetStats(),
			"last_sync":   h.indexer.GetLastSync(),
			"scan_result": result,
		})
		return
	}

	force := c.DefaultQuery("force", "false") == "true"

	// Check if scan is needed
	if !force && !h.indexer.NeedsSync(1*time.Hour) {
		lastSync := h.indexer.GetLastSync()
		c.JSON(http.StatusOK, gin.H{
			"ok":        true,
			"message":   "Index is recent, skipping scan",
			"last_sync": lastSync,
			"stats":     h.indexer.GetStats(),
		})
		return
	}

	// Run scan
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	err := h.indexer.ScanAndIndex(ctx)
	if err != nil {
		logger.Error("Failed to scan Drive for clips", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to scan Drive: " + err.Error(),
		})
		return
	}

	// Save to disk
	index := h.indexer.GetIndex()
	if err := h.indexStore.SaveIndex(index); err != nil {
		logger.Error("Failed to save clip index", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to save index: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"message":   "Clip index scan completed successfully",
		"stats":     h.indexer.GetStats(),
		"last_sync": h.indexer.GetLastSync(),
	})
}

// ClearIndex godoc
// @Summary Clear the clip index
// @Description Clear the current clip index and delete from disk. Accessible from remote machines.
// @Tags clip-index
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /clip/index/clear [delete]
func (h *ClipIndexHandler) ClearIndex(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	// Clear from memory
	h.indexer.SetIndex(&clip.ClipIndex{
		Version:      "1.0",
		RootFolderID: h.rootFolderID,
		Stats: clip.IndexStats{
			ClipsByGroup: make(map[string]int),
		},
	})

	// Delete from disk
	if err := h.indexStore.DeleteIndex(); err != nil {
		logger.Error("Failed to delete clip index file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to delete index file: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Clip index cleared successfully",
	})
}

// IncrementalScan runs an incremental scan
// @Summary Incremental scan
// @Description Scan only folders modified since last sync
// @Tags Clip Index
// @Post /clip/index/scan/incremental
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/scan/incremental [post]
func (h *ClipIndexHandler) IncrementalScan(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	// Use scanner if available for consistent results tracking
	if h.scanner != nil {
		result := h.scanner.TriggerIncrementalScan(c.Request.Context())
		if !result.Success {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":          false,
				"error":       result.Error,
				"scan_result": result,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":                true,
			"message":           "Incremental scan completed successfully",
			"folders_updated":   result.ClipsChanged,
			"total_clips":       result.TotalClips,
			"total_folders":     result.TotalFolders,
			"stats":             h.indexer.GetStats(),
			"scan_result":       result,
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	folders, clips, err := h.indexer.IncrementalScan(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Incremental scan failed: " + err.Error(),
		})
		return
	}

	// Save to disk
	index := h.indexer.GetIndex()
	if err := h.indexStore.SaveIndex(index); err != nil {
		logger.Error("Failed to save clip index", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":                true,
		"folders_updated":   folders,
		"clips_net_change":  clips,
		"total_clips":       len(index.Clips),
		"total_folders":     len(index.Folders),
		"stats":             h.indexer.GetStats(),
	})
}
