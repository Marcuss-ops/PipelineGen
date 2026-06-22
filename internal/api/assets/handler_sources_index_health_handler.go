// Package sources — index health endpoint.
// GET /api/media/index-health
// Cross-checks SQLite records, embedding status, and Qdrant point counts
// to detect drift between the canonical DB and the vector search index.
//
// PR3-5b.4: when realtime.Service is configured, the canonical
// sqlite<->qdrant cross-check (sqlite_assets / sqlite_indexed /
// qdrant_points / missing_in_qdrant / orphan_in_qdrant / pending_outbox /
// dead_letter) is reported from realtime.IndexHealth. The legacy raw-SQL
// path is retained as a fallback for partial wiring (realtime disabled,
// test harnesses, or older deploys).
//
// PR3-5b.6: the pr3_5b payload exposes degraded_sources ([]string) so
// operators get per-leg names ("qdrant", "qdrant_info", "sqlite",
// "outbox") instead of a coarse Degraded=true. IndexHealth surface
// also gained those fields via the same change.
package assets

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// IndexHealth godoc
// @Summary Health check for semantic index consistency
// @Description Cross-checks SQLite records, embedding status, and Qdrant point counts
// @Tags system
// @Success 200 {object} apiutil.Response
// @Router /api/media/index-health [get]
func (h *Handler) IndexHealth(c *gin.Context) {
	ctx := c.Request.Context()

	// PR3-5b.4 path: realtime.Service is wired → delegate to the canonical
	// cross-check which reports all 7 monitoring fields (sqlite_assets,
	// sqlite_indexed, qdrant_points, missing_in_qdrant, orphan_in_qdrant,
	// pending_outbox, dead_letter) plus capped sample IDs.
	if h.realtimeSvc != nil {
		report, err := h.realtimeSvc.IndexHealth(ctx)
		if err != nil {
			h.log.Warn("realtime.IndexHealth failed; falling back to legacy raw-SQL path",
				zap.Error(err))
		} else if report != nil {
			c.JSON(http.StatusOK, gin.H{
				"ok":       report.OK,
				"degraded": report.Degraded,
				"pr3_5b": gin.H{
					"sqlite_assets":           report.SQLiteAssets,
					"sqlite_indexed":          report.SQLiteIndexed,
					"qdrant_points":           report.QdrantPoints,
					"missing_in_qdrant":       report.MissingInQdrant,
					"orphan_in_qdrant":        report.OrphanInQdrant,
					"pending_outbox":          report.PendingOutbox,
					"dead_letter":             report.DeadLetter,
					"missing_in_qdrant_ids":   report.MissingInQdrantIDs,
					"orphan_in_qdrant_ids":    report.OrphanInQdrantIDs,
					"qdrant_healthy":          report.QdrantHealthy,
					"checks_complete":         report.ChecksComplete,
					"degraded_sources":        report.DegradedSources,
					"sample_limit":            report.SampleLimit,
					"sample_saturated":        report.SampleSaturated,
					"counts_are_lower_bounds": report.CountsAreLowerBounds,
				},
				"legacy": gin.H{
					"db_total":           report.DBTotal,
					"with_embedding":     report.WithEmbedding,
					"db_to_qdrant_delta": report.DBToQdrantDelta,
					"stale_qdrant_ids":   report.StaleQdrantIDs,
				},
			})
			return
		}
	}

	// ── Legacy path (fallback when realtime.Service is unconfigured) ──
	var dbTotal, withEmb, withoutEmb, missingST, missingLang int
	if h.imagesRepo != nil {
		db := h.imagesRepo.DB()
		if db != nil {
			_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets").Scan(&dbTotal)
			_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]'").Scan(&withEmb)
			_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE embedding_json IS NULL OR embedding_json = '' OR embedding_json = '[]'").Scan(&withoutEmb)
			_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE search_text IS NULL OR search_text = ''").Scan(&missingST)
			_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE json_extract(metadata_json, '$.language') IS NULL OR json_extract(metadata_json, '$.language') = ''").Scan(&missingLang)
		}
	}

	// Qdrant counts — try vectorStore first, then fallback to realtimeSvc
	var qdrantPoints int64
	qdrantOK := false
	vs := h.vectorStore
	if vs == nil && h.realtimeSvc != nil {
		vs = h.realtimeSvc.VectorStore()
	}
	if vs != nil {
		if err := vs.Health(ctx); err == nil {
			qdrantOK = true
			// Use OperationCollectionInfo (alias-served) so the legacy
			// fallback reports the same point count family as the canonical
			// realtime.IndexHealth path. CollectionInfo delegates to
			// PhysicalCollectionInfo post-PR3-5b.5 which would skew this
			// payload toward the versioned-physical count during a
			// SwitchAlias swap.
			if info, err := vs.OperationCollectionInfo(ctx); err == nil && info != nil {
				qdrantPoints = info.PointsCount
			}
		}
	}

	embeddingSyncPct := 100.0
	if dbTotal > 0 {
		embeddingSyncPct = float64(withEmb) / float64(dbTotal) * 100.0
	}

	h.log.Info("index-health (legacy path)",
		zap.Int("db_total", dbTotal),
		zap.Int("with_embedding", withEmb),
		zap.Int64("qdrant_points", qdrantPoints),
		zap.Float64("sync_pct", embeddingSyncPct),
	)

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"checks": gin.H{
			"sqlite": gin.H{
				"total":               dbTotal,
				"with_embedding":      withEmb,
				"without_embedding":   withoutEmb,
				"missing_search_text": missingST,
				"missing_language":    missingLang,
			},
			"qdrant": gin.H{
				"healthy": qdrantOK,
				"points":  qdrantPoints,
			},
			"delta":              dbTotal - int(qdrantPoints),
			"embedding_sync_pct": embeddingSyncPct,
			"path":               "legacy-fallback",
		},
	})
}
