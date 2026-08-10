// Package scriptassets — handler.go: HTTP transport for the
// script_assets capability. Thin transport per AGENTS.md **Modular edit
// patterns** §Pattern 8 (API package: thin transport only). No
// business orchestration, no DB I/O, no provider-state mutation —
// only JSON shaping of the Service's Catalog() projection.
package scriptassets

import (
	"net/http"

	appscriptassets "github.com/Marcuss-ops/PipelineGen/internal/application/scriptassets"
	"github.com/gin-gonic/gin"
)

// Handler exposes the script_assets capability's read-only catalog
// endpoint. The Capability Standard slice is exercised via the
// /script-assets/catalog route; future per-script asset lookups land
// behind /script-assets/:scriptId/assets in a follow-up PR.
type Handler struct {
	svc *appscriptassets.Service
}

// NewHandler wires a Handler around the canonical Service. svc is
// required (no nil-tolerant transport — handlers must panic visibly
// at startup if wiring is broken, per the composition root's contract).
func NewHandler(svc *appscriptassets.Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes attaches the script_assets routes to the parent
// route group. The composition root mounts this handler on the
// /script-assets prefix via api.NewRouteModule — there are no nested
// sub-routes today. Matches the Channels precedent.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	if rg == nil || h == nil {
		return
	}
	rg.GET("/catalog", h.handleCatalog)
}

// handleCatalog returns the script_assets provider's static catalog
// as JSON. Response shape:
//
//	{
//	  "name": "script_assets",
//	  "capabilities": ["search", "script"],
//	  "media_type": "script"
//	}
//
// Stable shape for the stand-up capability; production enrichment
// adds per-language breakdowns and topic rankings in a follow-up PR.
func (h *Handler) handleCatalog(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "script_assets: service not wired",
		})
		return
	}
	name := "script_assets"
	if p := h.svc.Provider(); p != nil {
		name = p.Name()
	}
	capabilities := h.svc.Catalog()
	c.JSON(http.StatusOK, gin.H{
		"name":         name,
		"capabilities": capabilities,
		"media_type":   "script",
	})
}
