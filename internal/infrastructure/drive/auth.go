package drive

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type fallbackTokenSource struct {
	primary  oauth2.TokenSource
	fallback oauth2.TokenSource
}

func (f *fallbackTokenSource) Token() (*oauth2.Token, error) {
	token, err := f.primary.Token()
	if err == nil {
		return token, nil
	}

	if f.fallback != nil && isOAuthRefreshFallbackError(err) {
		return f.fallback.Token()
	}
	return nil, err
}

func isOAuthRefreshFallbackError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unauthorized_client") || strings.Contains(msg, "invalid_grant")
}

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

	// Use the refreshing token source when possible, but keep a static
	// bearer-token fallback for public or semi-stale token files. This lets
	// a valid access token continue to work even when the refresh token is
	// bound to a different OAuth client and Google's token endpoint rejects
	// the refresh flow with unauthorized_client/invalid_grant.
	refreshSource := oauthCfg.TokenSource(ctx, token)
	persistentSource := &refreshingTokenSource{
		source:    refreshSource,
		tokenFile: tokenPath,
	}
	httpSource := oauth2.TokenSource(persistentSource)
	if token.AccessToken != "" {
		httpSource = &fallbackTokenSource{
			primary:  persistentSource,
			fallback: oauth2.StaticTokenSource(token),
		}
	}

	httpClient := oauth2.NewClient(ctx, httpSource)
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
