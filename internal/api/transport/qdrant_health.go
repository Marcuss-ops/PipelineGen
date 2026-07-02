// Package transport — Qdrant-specific health endpoints.
//
// HIGH #7 (Qdrant Verdetto, July 2026): exposes /qdrant/live and
// /qdrant/ready as dedicated Qdrant health probes, separate from the
// generic /health and /ready endpoints that aggregate DB+Drive+Qdrant.
//
// /qdrant/live: fast liveness — GET /collections via HealthProbe.
//   Returns 200 if Qdrant responds, 503 otherwise.
//
// /qdrant/ready: deep readiness — four checks:
//   1. Alias present    → runtime alias resolves to a target collection
//   2. Collection populated → PointTotal > 0 on the active collection
//   3. Schema ok        → CompareActiveCollection returns diff.Compatible
//   4. Semantic canary  → a real search query via Searcher.SearchByText
//      proves the full pipeline (alias→collection→ANN) responds.
//      The canary result is CACHED for 30s to avoid hammering Qdrant
//      on every orchestration-level readiness poll.
//
// The handler is thin transport (AGENTS.md Pattern 8): it delegates all
// Qdrant operations to the typed qdrant package primitives.
package transport

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// canaryTTL is how long the semantic canary result stays cached.
// 30s matches the Searcher's alias-cache TTL.
const canaryTTL = 30 * time.Second

// QdrantHealthHandler exposes /qdrant/live and /qdrant/ready.
// All dependencies are nil-safe — nil deps return 503 with a clear
// "not configured" error.
type QdrantHealthHandler struct {
	probe    *qdrant.HealthProbe
	collMgr  *qdrant.CollectionManager
	searcher *qdrant.Searcher
	embedder qdrant.TextEmbedder

	// Canary cache: sampled on the first Ready call and re-sampled
	// every canaryTTL. Prevents hammering Qdrant with the canary
	// query on every readiness poll from the orchestrator.
	canaryMu         sync.RWMutex
	canaryCachedAt   time.Time
	canaryOK         bool
	canaryDetail     string
}

// NewQdrantHealthHandler constructs a handler. All params may be nil;
// nil deps are handled gracefully at request time.
func NewQdrantHealthHandler(
	probe *qdrant.HealthProbe,
	collMgr *qdrant.CollectionManager,
	searcher *qdrant.Searcher,
	embedder qdrant.TextEmbedder,
) *QdrantHealthHandler {
	return &QdrantHealthHandler{
		probe:    probe,
		collMgr:  collMgr,
		searcher: searcher,
		embedder: embedder,
	}
}

// Live is GET /qdrant/live — fast liveness via GET /collections.
func (h *QdrantHealthHandler) Live(c *gin.Context) {
	if h.probe == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":     false,
			"status": "not live",
			"error":  "qdrant health probe not configured",
		})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if err := h.probe.Probe(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":     false,
			"status": "not live",
			"error":  err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"status": "live",
	})
}

// Ready is GET /qdrant/ready — deep readiness with four checks.
// Alias check, collection populated, schema ok, and a cached
// semantic canary probe.
func (h *QdrantHealthHandler) Ready(c *gin.Context) {
	// Fail early if critical deps are missing.
	if h.collMgr == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":     false,
			"status": "not ready",
			"error":  "qdrant collection manager not configured",
		})
		return
	}
	if h.searcher == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":     false,
			"status": "not ready",
			"error":  "qdrant searcher not configured",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	checks := make(map[string]any)
	allOK := true

	// ── Check 1: Alias present ──────────────────────────────────
	target, err := h.collMgr.GetActiveCollection(ctx)
	if err != nil || target == "" {
		allOK = false
		errMsg := "alias has no target"
		if err != nil {
			errMsg = err.Error()
		}
		checks["alias"] = map[string]any{"ok": false, "error": errMsg}
	} else {
		checks["alias"] = map[string]any{"ok": true, "target": target}
	}

	// ── Check 2: Collection populated ────────────────────────────
	if target != "" {
		info, infoErr := h.collMgr.InspectCollection(ctx, target)
		if infoErr != nil {
			allOK = false
			checks["collection"] = map[string]any{
				"ok":    false,
				"error": fmt.Sprintf("inspect %q: %v", target, infoErr),
			}
		} else if info.PointTotal == 0 {
			allOK = false
			checks["collection"] = map[string]any{
				"ok":     false,
				"target": target,
				"error":  "collection is empty (PointTotal=0)",
			}
		} else {
			checks["collection"] = map[string]any{
				"ok":         true,
				"target":     target,
				"point_total": info.PointTotal,
			}
		}
	}

	// ── Check 3: Schema ok ──────────────────────────────────────
	diff, diffErr := h.collMgr.CompareActiveCollection(ctx)
	if diffErr != nil {
		allOK = false
		checks["schema"] = map[string]any{"ok": false, "error": diffErr.Error()}
	} else if !diff.Compatible {
		allOK = false
		checks["schema"] = map[string]any{
			"ok":                   false,
			"compatible":           false,
			"missing_vectors":      diff.MissingVectors,
			"dimension_mismatches": len(diff.DimensionMismatches),
		}
	} else {
		checks["schema"] = map[string]any{"ok": true, "compatible": true}
	}

	// ── Check 4: Semantic canary (sampled + cached) ────────────
	canaryOK, canaryDetail := h.sampledCanary(ctx)
	if !canaryOK {
		allOK = false
	}
	checks["canary"] = map[string]any{"ok": canaryOK, "detail": canaryDetail}

	status := http.StatusOK
	if !allOK {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{
		"ok":     allOK,
		"status": statusString(allOK),
		"checks": checks,
	})
}

// sampledCanary returns the cached canary result, re-sampling if the
// cache is stale. The canary runs a real search query via
// Searcher.SearchByText to prove the full pipeline works.
func (h *QdrantHealthHandler) sampledCanary(ctx context.Context) (bool, string) {
	// Fast path: read cache.
	h.canaryMu.RLock()
	if time.Since(h.canaryCachedAt) < canaryTTL {
		ok, detail := h.canaryOK, h.canaryDetail
		h.canaryMu.RUnlock()
		return ok, detail
	}
	h.canaryMu.RUnlock()

	// Slow path: sample.
	h.canaryMu.Lock()
	defer h.canaryMu.Unlock()

	// Double-check: another goroutine may have populated the cache.
	if time.Since(h.canaryCachedAt) < canaryTTL {
		return h.canaryOK, h.canaryDetail
	}

	if h.embedder == nil {
		h.canaryOK = true // embedder not configured — canary skipped, not failed
		h.canaryDetail = "text embedder not configured — canary skipped"
		h.canaryCachedAt = time.Now()
		return h.canaryOK, h.canaryDetail
	}

	results, err := h.searcher.SearchByText(ctx, "canary probe", h.embedder, "text", 3, 0.0)
	if err != nil {
		h.canaryOK = false
		h.canaryDetail = fmt.Sprintf("canary search failed: %v", err)
		h.canaryCachedAt = time.Now()
		return h.canaryOK, h.canaryDetail
	}

	h.canaryOK = true
	h.canaryDetail = fmt.Sprintf("canary search returned %d results", len(results))
	h.canaryCachedAt = time.Now()
	return h.canaryOK, h.canaryDetail
}


