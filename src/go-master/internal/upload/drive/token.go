package drive

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"golang.org/x/oauth2"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// refreshingTokenSource wraps a token source and saves refreshed tokens to file
type refreshingTokenSource struct {
	source    oauth2.TokenSource
	tokenFile string
	mu        sync.Mutex
}

// Token returns a valid token, refreshing if necessary, and saves to file
func (r *refreshingTokenSource) Token() (*oauth2.Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	token, err := r.source.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	// Save refreshed token to file for persistence across restarts
	if r.tokenFile != "" {
		if err := SaveToken(r.tokenFile, token); err != nil {
			logger.Warn("Failed to save refreshed token", zap.Error(err))
		} else {
			logger.Debug("Refreshed OAuth token saved successfully")
		}
	}

	return token, nil
}

// loadToken carica il token OAuth da file
func loadToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Try to parse as generic map first to handle different field names
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	// Normalize field names for Go oauth2 library
	if accessToken, ok := raw["token"].(string); ok {
		raw["access_token"] = accessToken
		delete(raw, "token")
	}
	if tokenType, ok := raw["token_type"].(string); !ok || tokenType == "" {
		raw["token_type"] = "Bearer"
	}

	// Re-marshal with normalized names
	normalized, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}

	var token oauth2.Token
	if err := json.Unmarshal(normalized, &token); err != nil {
		return nil, err
	}

	return &token, nil
}

// SaveToken salva il token OAuth su file
func SaveToken(path string, token *oauth2.Token) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}
