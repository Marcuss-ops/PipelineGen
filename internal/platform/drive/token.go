package drive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/oauth2"

	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"

	"go.uber.org/zap"
)

// refreshingTokenSource wraps a token source and persists the token to disk
// when — and only when — the access token actually changes.
type refreshingTokenSource struct {
	source    oauth2.TokenSource
	tokenFile string
	mu        sync.Mutex

	// persistedAccessToken is the access token most recently written to
	// tokenFile. oauth2 reuses a cached token until it expires, so the
	// historical unconditional SaveToken cost one disk write per Drive API
	// request and let concurrent requests truncate-write the same file at
	// once. It is updated only AFTER a successful write, so a transient write
	// failure is retried by the next call instead of being recorded as
	// persisted.
	persistedAccessToken string
}

// Token returns a valid token, refreshing if necessary, and persists it to
// disk only when the access token changed.
//
// The mutex intentionally spans the refresh AND the write: the write now
// happens only on an actual refresh, and serializing it removes concurrent
// same-path writes (two overlapping os.WriteFile truncations could interleave
// and leave an unparseable token file).
func (r *refreshingTokenSource) Token() (*oauth2.Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	token, err := r.source.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	// Save refreshed token to file for persistence across restarts.
	if r.tokenFile != "" && token.AccessToken != "" && token.AccessToken != r.persistedAccessToken {
		if err := SaveToken(r.tokenFile, token); err != nil {
			logger.Warn("Failed to save refreshed token", zap.Error(err))
		} else {
			r.persistedAccessToken = token.AccessToken
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
	var raw map[string]any
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

// SaveToken salva il token OAuth su file, atomicamente.
//
// The historical os.WriteFile truncated the destination before writing, so a
// crash — or a concurrent reader — could observe a truncated, unparseable token
// file and lose authentication until an operator re-ran the OAuth flow. Writing
// a sibling temp file and renaming it into place makes the replacement atomic
// on POSIX filesystems. os.CreateTemp already creates the file 0600, matching
// the credential's original mode.
func SaveToken(path string, token *oauth2.Token) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".token-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = "" // renamed into place; nothing left to clean up
	return nil
}
