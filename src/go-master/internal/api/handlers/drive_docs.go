package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// CreateDoc godoc
// @Summary Create a Google Doc
// @Description Create a new Google Doc with optional content
// @Tags drive
// @Accept json
// @Produce json
// @Param request body drive.CreateDocRequest true "Create doc request"
// @Success 200 {object} map[string]interface{}
// @Router /drive/create-doc [post]
func (h *DriveHandler) CreateDoc(c *gin.Context) {
	if h.docClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Docs client not initialized"})
		return
	}

	var req drive.CreateDocRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	doc, err := h.docClient.CreateDoc(c.Request.Context(), req.Title, req.Content, req.FolderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	logger.Info("Created Google Doc", zap.String("id", doc.ID))

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"doc_id":  doc.ID,
		"doc_url": doc.URL,
		"title":   doc.Title,
	})
}

// AppendDoc godoc
// @Summary Append to a Google Doc
// @Description Add content to an existing Google Doc
// @Tags drive
// @Accept json
// @Produce json
// @Param request body drive.AppendDocRequest true "Append doc request"
// @Success 200 {object} map[string]interface{}
// @Router /drive/append-doc [post]
func (h *DriveHandler) AppendDoc(c *gin.Context) {
	if h.docClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Docs client not initialized"})
		return
	}

	var req drive.AppendDocRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	docID := req.DocID
	docURL := req.DocURL

	// Build URL if only doc_id provided
	if docID != "" && docURL == "" {
		docURL = fmt.Sprintf("https://docs.google.com/document/d/%s/edit", docID)
	}

	// Extract doc_id from URL if only URL provided
	if docID == "" && docURL != "" {
		docID = drive.ExtractDocIDFromURL(docURL)
	}

	if docID == "" && docURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "doc_id or doc_url required"})
		return
	}

	var err error
	if docURL != "" {
		err = h.docClient.AppendToDocByURL(c.Request.Context(), docURL, req.Content)
	} else {
		err = h.docClient.AppendToDoc(c.Request.Context(), docID, req.Content)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"doc_url":     docURL,
		"chars_added": len(req.Content),
	})
}
