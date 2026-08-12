// Package app — adapters_qdrant_transport_health.go
//
// Composition-root adapter for systemhealth.QdrantEndpointPort.
//
// Sprint 3.4 step1 (godlike/06 SSOT — single canonical owner per fact):
// this is the only legal site to bind the 3 infrastructure-layer
// Qdrant types (disasterrecovery.HealthProbe, collections.CollectionManager,
// search.Searcher + search.TextEmbedder) into the application-layer
// port consumed by the transport handler. The api layer (transport,
// routes, server) consumes ONLY the port — no infrastructure import.
//
// Adapter ownership: this adapter owns the per-call deadlines (5s
// for Live, 10s for the full Ready sweep) so the transport handler
// is pure JSON passthrough. A future reviewer who wants to add a
// handler-side timeout will create nested WithTimeout calls; that's
// the wrong fix — increase the constants here.
//
// The handler-side wire shape is preserved 1:1 by:
//   - ready/not-ready strings  → Status field
//   - per-check gin.H blocks   → Checks map (alias / collection /
//     schema / canary)
//   - nil-configured branches  → Error field (handler renders the
//     legacy flat shape verbatim instead
//     of wrapping under Checks)
//
// The 4-check deep-readiness logic + 30s canary cache state
// (canaryMu/canaryCachedAt/canaryOK/canaryDetail) moved verbatim
// from the previous handler-local implementation.
package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/disasterrecovery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
)

// qdrantEndpointCanaryTTL is how long the semantic canary result
// stays cached. 30s matches the Searcher's alias-cache TTL.
const qdrantEndpointCanaryTTL = 30 * time.Second

// qdrantEndpointCanaryQuery is the synthetic search query used to
// prove the full pipeline (alias→collection→ANN) responds.
const qdrantEndpointCanaryQuery = "canary probe"

// qdrantEndpointAdapter implements systemhealth.QdrantEndpointPort
// against real Qdrant infra. Lives in the composition root because
// godlike/06 forbids the transport layer from importing infrastructure.
type qdrantEndpointAdapter struct {
	probe    *disasterrecovery.HealthProbe
	collMgr  *collections.CollectionManager
	searcher *search.Searcher
	embedder search.TextEmbedder

	// Canary cache. 30s TTL; prevents hammering Qdrant on every
	// orchestration-level readiness poll.
	canaryMu       sync.RWMutex
	canaryCachedAt time.Time
	canaryOK       bool
	canaryDetail   string
}

// newQdrantEndpointAdapter constructs a deep-readiness adapter.
// All deps may be nil; nil deps fail-closed according to the
// matching transport handler's not-configured semantics.
func newQdrantEndpointAdapter(
	probe *disasterrecovery.HealthProbe,
	collMgr *collections.CollectionManager,
	searcher *search.Searcher,
	embedder search.TextEmbedder,
) *qdrantEndpointAdapter {
	return &qdrantEndpointAdapter{
		probe:    probe,
		collMgr:  collMgr,
		searcher: searcher,
		embedder: embedder,
	}
}

// Live fast liveness check via Qdrant GET /collections.
// Adapter-owned 5s deadline (go doc on this file).
func (a *qdrantEndpointAdapter) Live(ctx context.Context) error {
	if a.probe == nil {
		return fmt.Errorf("qdrant health probe not configured")
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return a.probe.Probe(cctx)
}

// Ready produces the deep-readiness report with 4 named sub-checks
// (alias, collection, schema, canary) for the normal path, OR a
// flat Error-field short-circuit for the not-configured (nil-dep)
// branches so the handler can render the previous wire shape verbatim.
//
// Adapter-owned 10s full-sweep deadline.
func (a *qdrantEndpointAdapter) Ready(ctx context.Context) systemhealth.QdrantReadyReport {
	if a.collMgr == nil {
		return systemhealth.QdrantReadyReport{
			OK:     false,
			Status: "not ready",
			Error:  "qdrant collection manager not configured",
		}
	}
	if a.searcher == nil {
		return systemhealth.QdrantReadyReport{
			OK:     false,
			Status: "not ready",
			Error:  "qdrant searcher not configured",
		}
	}

	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	checks := make(map[string]any)
	allOK := true

	// ── Check 1: Alias present ─────────────────────────────────
	target, err := a.collMgr.GetActiveCollection(cctx)
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

	// ── Check 2: Collection populated ──────────────────────────
	if target != "" {
		info, infoErr := a.collMgr.InspectCollection(cctx, target)
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
				"ok":          true,
				"target":      target,
				"point_total": info.PointTotal,
			}
		}
	}

	// ── Check 3: Schema ok ─────────────────────────────────────
	diff, diffErr := a.collMgr.CompareActiveCollection(cctx)
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
	canaryOK, canaryDetail := a.sampledCanary(cctx)
	if !canaryOK {
		allOK = false
	}
	checks["canary"] = map[string]any{"ok": canaryOK, "detail": canaryDetail}

	return systemhealth.QdrantReadyReport{
		OK:     allOK,
		Status: qdrantEndpointStatus(allOK),
		Checks: checks,
	}
}

// sampledCanary returns the cached canary result, re-sampling if the
// cache is stale. The canary runs a real search query via
// Searcher.SearchByText to prove the full pipeline works.
// Mirrors the previous handler-side sampledCanary verbatim.
func (a *qdrantEndpointAdapter) sampledCanary(ctx context.Context) (bool, string) {
	// Fast path: read cache.
	a.canaryMu.RLock()
	if time.Since(a.canaryCachedAt) < qdrantEndpointCanaryTTL {
		ok, detail := a.canaryOK, a.canaryDetail
		a.canaryMu.RUnlock()
		return ok, detail
	}
	a.canaryMu.RUnlock()

	// Slow path: sample.
	a.canaryMu.Lock()
	defer a.canaryMu.Unlock()

	// Double-check: another goroutine may have populated the cache.
	if time.Since(a.canaryCachedAt) < qdrantEndpointCanaryTTL {
		return a.canaryOK, a.canaryDetail
	}

	if a.embedder == nil {
		a.canaryOK = true // embedder not configured — canary skipped, not failed
		a.canaryDetail = "text embedder not configured — canary skipped"
		a.canaryCachedAt = time.Now()
		return a.canaryOK, a.canaryDetail
	}

	results, err := a.searcher.SearchByText(ctx, qdrantEndpointCanaryQuery, a.embedder, "text", 3, 0.0)
	if err != nil {
		a.canaryOK = false
		a.canaryDetail = fmt.Sprintf("canary search failed: %v", err)
		a.canaryCachedAt = time.Now()
		return a.canaryOK, a.canaryDetail
	}

	a.canaryOK = true
	a.canaryDetail = fmt.Sprintf("canary search returned %d results", len(results))
	a.canaryCachedAt = time.Now()
	return a.canaryOK, a.canaryDetail
}

// qdrantEndpointStatus maps AllOK → canonical status string.
func qdrantEndpointStatus(allOK bool) string {
	if allOK {
		return "ready"
	}
	return "not ready"
}
