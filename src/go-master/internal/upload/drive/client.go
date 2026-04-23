// Package drive provides Google Drive API integration for Agent 5.
package drive

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// Client gestisce le operazioni Google Drive
type Client struct {
	service     *drive.Service
	tokenSource oauth2.TokenSource
	tokenFile   string // Path to token file for refresh support
	credsData   []byte // OAuth credentials JSON
	scopes      []string
	mu          sync.RWMutex
	reqTimeout  time.Duration // Timeout for individual requests
	maxRetries  int          // Maximum retries for transient errors
}

// Config configurazione Drive
type Config struct {
	CredentialsFile string
	TokenFile       string
	Scopes          []string
	RequestTimeout  time.Duration // Timeout for individual API requests
	MaxRetries      int          // Maximum retries for transient errors
}

// DefaultConfig configurazione di default
func DefaultConfig() Config {
	return Config{
		CredentialsFile: "credentials.json",
		TokenFile:       "token.json",
		Scopes: []string{
			drive.DriveFileScope,
			drive.DriveMetadataScope,
		},
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
	}
}

// NewClient crea un nuovo client Drive con supporto per il refresh automatico del token
func NewClient(ctx context.Context, config Config) (*Client, error) {
	credentials, err := os.ReadFile(config.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	_, err = google.ConfigFromJSON(credentials, config.Scopes...)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	token, err := loadToken(config.TokenFile)
	if err != nil {
		logger.Warn("Token not found, need authentication", zap.Error(err))
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	oauthConfig, _ := google.ConfigFromJSON(credentials, config.Scopes...)
	tokenSource := oauthConfig.TokenSource(context.Background(), token)

	tokenSource = oauth2.ReuseTokenSource(token, &refreshingTokenSource{
		source:    tokenSource,
		tokenFile: config.TokenFile,
	})

	httpClient := oauth2.NewClient(context.Background(), tokenSource)

	service, err := drive.NewService(context.Background(), option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create Drive service: %w", err)
	}

	return &Client{
		service:     service,
		tokenSource: tokenSource,
		tokenFile:   config.TokenFile,
		credsData:   credentials,
		scopes:      config.Scopes,
		reqTimeout:  config.RequestTimeout,
		maxRetries:  config.MaxRetries,
	}, nil
}

// withTimeout returns a context with timeout if none is set
func (c *Client) withTimeout(ctx context.Context) context.Context {
	if _, ok := ctx.Deadline(); !ok {
		newCtx, _ := context.WithTimeout(ctx, c.reqTimeout)
		return newCtx
	}
	return ctx
}

// withRetry wraps a Drive API call with retry logic for transient errors
func (c *Client) withRetry(ctx context.Context, operation func() error) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 100 * time.Millisecond
			if backoff > 2*time.Second {
				backoff = 2 * time.Second
			}
			logger.Warn("Retrying Drive API call", zap.Int("attempt", attempt), zap.Duration("backoff", backoff))
			time.Sleep(backoff)
		}

		err := operation()
		if err == nil {
			return nil
		}
		lastErr = err

		if !isRetryableError(err) {
			return err
		}

		logger.Warn("Drive API call failed, may retry", zap.Int("attempt", attempt+1), zap.Int("max_retries", c.maxRetries), zap.Error(err))
	}
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "503")
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// GetDriveLink genera un link per un file/cartella
func GetDriveLink(fileID string) string {
	return fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID)
}

// GetFolderLink genera un link per una cartella
func GetFolderLink(folderID string) string {
	return fmt.Sprintf("https://drive.google.com/drive/folders/%s", folderID)
}
