// Package transport — capabilities.go serves GET /api/capabilities.
//
// The endpoint exposes the set of mounted API capabilities (derived
// from the same WireRegistry used by /ready), the server version, and
// the canonical API version. It is intended for client discovery and
// health pre-flight checks.
package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CapabilitiesHandler serves GET /api/capabilities.
type CapabilitiesHandler struct {
	wire *WireRegistry
	// Version is the server binary version (e.g. "v1.2.3").
	Version string
	// APIVersion is the canonical API contract version (e.g. "v2").
	APIVersion string
}

// NewCapabilitiesHandler constructs a CapabilitiesHandler.
// A nil WireRegistry is safe: the handler will report all known
// capabilities as NOT_MOUNTED.
func NewCapabilitiesHandler(wire *WireRegistry, version, apiVersion string) *CapabilitiesHandler {
	return &CapabilitiesHandler{
		wire:       wire,
		Version:    version,
		APIVersion: apiVersion,
	}
}

// CapabilitiesResponse is the canonical JSON shape for
// GET /api/capabilities.
type CapabilitiesResponse struct {
	OK           bool              `json:"ok"`
	Version      string            `json:"version"`
	APIVersion   string            `json:"api_version"`
	Capabilities map[string]string `json:"capabilities"`
}

// Capabilities handles GET /api/capabilities.
func (h *CapabilitiesHandler) Capabilities(c *gin.Context) {
	c.JSON(http.StatusOK, CapabilitiesResponse{
		OK:           true,
		Version:      h.Version,
		APIVersion:   h.APIVersion,
		Capabilities: h.wire.All(),
	})
}
