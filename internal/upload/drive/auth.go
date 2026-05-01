package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"velox/go-master/pkg/config"
)

// NewDriveServiceFromFiles creates a Google Drive service using credentials and token files from config.
func NewDriveServiceFromFiles(ctx context.Context, cfg *config.Config) (*drive.Service, error) {
	credentialsPath := cfg.Paths.CredentialsFile
	tokenPath := cfg.Paths.TokenFile

	if credentialsPath == "" || tokenPath == "" {
		return nil, fmt.Errorf("google drive credentials/token paths are required")
	}
	if _, err := os.Stat(credentialsPath); err != nil {
		return nil, fmt.Errorf("google credentials file not found: %w", err)
	}
	if _, err := os.Stat(tokenPath); err != nil {
		return nil, fmt.Errorf("google token file not found: %w", err)
	}

	credentials, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read google credentials: %w", err)
	}
	oauthCfg, err := google.ConfigFromJSON(credentials, drive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse google credentials: %w", err)
	}

	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read google token: %w", err)
	}
	var token oauth2.Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		return nil, fmt.Errorf("failed to parse google token: %w", err)
	}

	httpClient := oauth2.NewClient(ctx, oauthCfg.TokenSource(ctx, &token))
	return drive.NewService(ctx, option.WithHTTPClient(httpClient))
}

// ResolveArtlistRootFolderID determines the appropriate Drive folder ID for Artlist uploads based on priority.
func ResolveArtlistRootFolderID(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if folderID := strings.TrimSpace(cfg.Harvester.DriveFolderID); folderID != "" {
		return folderID
	}
	if folderID := strings.TrimSpace(cfg.Drive.ClipsRootFolder); folderID != "" {
		return folderID
	}
	if folderID := strings.TrimSpace(cfg.Drive.StockRootFolder); folderID != "" {
		return folderID
	}
	return ""
}
