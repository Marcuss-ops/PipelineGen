package handlers

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/upload/drive"
)

// initDriveClient initializes the Google Drive client
func (h *ClipHandler) initDriveClient(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Double-check after acquiring lock
	if h.driveClient != nil {
		return nil
	}

	credsFile := h.credentialsFile
	if credsFile == "" {
		credsFile = "credentials.json"
	}

	tokenFile := h.tokenFile
	if tokenFile == "" {
		tokenFile = "token.json"
	}

	config := drive.Config{
		CredentialsFile: credsFile,
		TokenFile:       tokenFile,
		Scopes: []string{
			"https://www.googleapis.com/auth/drive",
			"https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/drive.readonly",
		},
	}

	client, err := drive.NewClient(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create Drive client: %w", err)
	}

	h.driveClient = client
	h.suggester = clip.NewSuggester(client, h.rootFolderID)

	return nil
}

// getDriveClient returns the Drive client, initializing if necessary
func (h *ClipHandler) getDriveClient(c *gin.Context) (*drive.Client, error) {
	h.mu.Lock()
	if h.driveClient != nil {
		h.mu.Unlock()
		return h.driveClient, nil
	}
	h.mu.Unlock()

	// Initialize outside the lock to avoid blocking other requests
	if err := h.initDriveClient(c.Request.Context()); err != nil {
		return nil, err
	}

	h.mu.Lock()
	client := h.driveClient
	h.mu.Unlock()
	return client, nil
}
