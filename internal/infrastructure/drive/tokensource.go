package drive

import (
	"encoding/json"
	"fmt"
	"os"
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
		return "", fmt.Errorf("drive.ParseTokenFile: read %q: %w", path, err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return "", fmt.Errorf("drive.ParseTokenFile: decode token JSON (%d bytes): %w", len(data), err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("drive.ParseTokenFile: token file missing access_token field")
	}
	return tok.AccessToken, nil
}
