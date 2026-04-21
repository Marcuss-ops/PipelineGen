// Package handlers provides HTTP handlers for Google Drive endpoints.
package handlers

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// DriveHandler handles Google Drive HTTP requests
type DriveHandler struct {
	driveClient *drive.Client
	docClient   *drive.DocClient
	credsFile   string
	tokenFile   string
}

// NewDriveHandler creates a new Drive handler
func NewDriveHandler(credentialsFile, tokenFile string) *DriveHandler {
	return &DriveHandler{
		credsFile: credentialsFile,
		tokenFile: tokenFile,
	}
}

// InitClient initializes the Drive client
func (h *DriveHandler) InitClient(ctx context.Context) error {
	if h.driveClient != nil {
		return nil
	}

	config := drive.Config{
		CredentialsFile: h.credsFile,
		TokenFile:       h.tokenFile,
		Scopes: []string{
			"https://www.googleapis.com/auth/drive",
			"https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/documents",
		},
	}

	client, err := drive.NewClient(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create Drive client: %w", err)
	}
	h.driveClient = client

	// Initialize Docs client
	docClient, err := drive.NewDocClient(ctx, client, h.credsFile, h.tokenFile)
	if err != nil {
		logger.Warn("Failed to create Docs client", zap.Error(err))
	} else {
		h.docClient = docClient
	}

	return nil
}

// GetDocClient returns the initialized DocClient (may be nil)
func (h *DriveHandler) GetDocClient() *drive.DocClient {
	return h.docClient
}

// RegisterRoutes registers Drive routes (protected — requires auth)
func (h *DriveHandler) RegisterRoutes(rg *gin.RouterGroup) {
	drive := rg.Group("/drive")
	{
		// Write operations
		drive.POST("/create-folder", h.CreateFolder)
		drive.POST("/create-folder-structure", h.CreateFolderStructure)
		drive.POST("/create-doc", h.CreateDoc)
		drive.POST("/append-doc", h.AppendDoc)
		drive.POST("/upload-clip", h.UploadClip)
		drive.POST("/upload-clip-simple", h.UploadClipSimple)
		drive.POST("/download-and-upload-clip", h.DownloadAndUploadClip)
	}
}

// RegisterPublicRoutes registers read-only Drive routes (no auth required)
func (h *DriveHandler) RegisterPublicRoutes(rg *gin.RouterGroup) {
	drive := rg.Group("/drive")
	{
		// Read-only endpoints
		drive.GET("/folders-tree", h.FoldersTree)
		drive.GET("/folder-content", h.FolderContent)
		drive.GET("/groups", h.GetGroups)
	}
}

// getClient gets or initializes the Drive client
func (h *DriveHandler) getClient(c *gin.Context) (*drive.Client, error) {
	if h.driveClient == nil {
		if err := h.InitClient(c.Request.Context()); err != nil {
			return nil, err
		}
	}
	return h.driveClient, nil
}

// GetDriveClient returns the initialized Drive client (for service injection)
func (h *DriveHandler) GetDriveClient() *drive.Client {
	return h.driveClient
}


