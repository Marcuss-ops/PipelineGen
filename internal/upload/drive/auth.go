package drive

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"velox/go-master/internal/config"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// NewGoogleHTTPClient creates an OAuth2 HTTP client using credentials and token paths.
// It uses a refreshing token source that saves the token to disk upon refresh.
func NewGoogleHTTPClient(ctx context.Context, credentialsPath, tokenPath string, scopes ...string) (*http.Client, error) {
	if credentialsPath == "" || tokenPath == "" {
		return nil, fmt.Errorf("google credentials/token paths are required")
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

	if len(scopes) == 0 {
		scopes = []string{drive.DriveScope}
	}

	oauthCfg, err := google.ConfigFromJSON(credentials, scopes...)
	if err != nil {
		return nil, fmt.Errorf("failed to parse google credentials: %w", err)
	}

	token, err := loadToken(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse google token: %w", err)
	}

	// Use refreshing token source to persist refreshed tokens
	tokenSource := oauthCfg.TokenSource(ctx, token)
	persistentSource := &refreshingTokenSource{
		source:    tokenSource,
		tokenFile: tokenPath,
	}

	httpClient := oauth2.NewClient(ctx, persistentSource)
	if httpClient == nil {
		return nil, fmt.Errorf("failed to create google oauth client")
	}
	return httpClient, nil
}

// NewDriveServiceFromFiles creates a Google Drive service using credentials and token files from config.
func NewDriveServiceFromFiles(ctx context.Context, cfg *config.Config) (*drive.Service, error) {
	httpClient, err := NewGoogleHTTPClient(ctx, cfg.Paths.CredentialsFile, cfg.Paths.TokenFile, drive.DriveScope)
	if err != nil {
		return nil, err
	}
	return drive.NewService(ctx, option.WithHTTPClient(httpClient))
}

// ResolveArtlistRootFolderID returns the Drive folder ID for Artlist.
// Priority: DriveConfig.ArtlistFolder() > Harvester.DriveFolderID > "".
// Deprecated: prefer cfg.Drive.ArtlistFolder() directly.
func ResolveArtlistRootFolderID(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if folderID := cfg.Drive.ArtlistFolder(); folderID != "" {
		return folderID
	}
	// Legacy: harvester override (not covered by ArtlistFolder() -> DriveConfig)
	return strings.TrimSpace(cfg.Harvester.DriveFolderID)
}
