// Package images (api/images) — animate_handler.go holds the
// POST /api/images/animate handler. Image animation (zoom-out
// from an existing image) was an NVIDIA capability that has been
// removed. This handler returns 501 Not Implemented.
//
// Territory: legacy (removed NVIDIA capability). Per godlike/07
// no-fake-availability: the endpoint explicitly returns 501 rather
// than silently succeeding with no-op.
package images

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Animate handles POST /api/images/animate — image animation (NVIDIA
// capability removed). Returns 501 Not Implemented.
func (h *ImagesHandler) Animate(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{
		"error": "image animation capability not implemented (NVIDIA capability removed)",
	})
}
