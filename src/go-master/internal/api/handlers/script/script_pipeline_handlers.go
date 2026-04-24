package script

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CreateDocument handles the creation of a script document in Google Docs.
func (h *ScriptPipelineHandler) CreateDocument(c *gin.Context) {
	var req CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	resp, err := h.createDocumentFromRequest(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// CreateDocumentPreview saves a local preview of the script document.
func (h *ScriptPipelineHandler) CreateDocumentPreview(c *gin.Context) {
	var req CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	req.PreviewOnly = true

	resp, err := h.createDocumentFromRequest(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// CreateDocumentFromSource creates a document using provided source text as script.
func (h *ScriptPipelineHandler) CreateDocumentFromSource(c *gin.Context) {
	var req CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(req.SourceText) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "source_text is required"})
		return
	}
	if strings.TrimSpace(req.Script) == "" {
		req.Script = req.SourceText
	}

	resp, err := h.createDocumentFromRequest(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}
