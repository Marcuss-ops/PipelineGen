package drive

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

var (
	ErrTokenUnreadable         = errors.New("token file not readable")
	ErrTokenInvalidAccessToken = errors.New("token file invalid or missing access_token")
	ErrTokenUnavailable        = errors.New("token unavailable")
)

// ParseTokenFile reads a Google OAuth2 token file (the JSON shape produced
// by `scripts/generate_drive_token.py` / `oauth2.Token` serialisation) and
// extracts the access_token field.
//
// Exposed as a standalone pure helper so the health.DriveChecker can
// exercise it without spinning up the oauth2 refreshingTokenSource stack
// in auth.go. Centralising the JSON parse also keeps the on-disk shape
// version-stable (test pin) so future token-schema changes are caught at
// the linter + unit test layer rather than at runtime /healthz.
//
// fix(health) close-out (June 2026, problem #2 final cleanup): extracted
// from auth.go::loadToken + DriveChecker.CheckDrive. Both call sites now
// delegate here. The helper is intentionally minimal: malformed JSON,
// missing access_token, empty path, and read-error all surface as
// wrapped errors that callers can pattern-match with `errors.Is`.
func ParseTokenFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("drive.ParseTokenFile: path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%w: drive.ParseTokenFile: read %q: %w", ErrTokenUnreadable, path, err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return "", fmt.Errorf("%w: drive.ParseTokenFile: decode token JSON (%d bytes): %w", ErrTokenUnavailable, len(data), err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("%w", ErrTokenInvalidAccessToken)
	}
	return tok.AccessToken, nil
}
