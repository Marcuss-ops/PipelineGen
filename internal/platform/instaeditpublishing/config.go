package instaeditpublishing

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	defaultBaseURL = "https://api.instaedit.org"
	// APIKeyEnv and WorkspaceIDEnv are deliberately publishing-specific:
	// INSTAEDIT_API_KEY belongs to the Calendar integration, whose key needs
	// the `automation` permission. Reusing it would make the two scopes
	// overwrite each other.
	APIKeyEnv      = "INSTAEDIT_PUBLISHING_API_KEY"
	WorkspaceIDEnv = "INSTAEDIT_PUBLISHING_WORKSPACE_ID"
)

// Config is the workspace-scoped InstaEdit publishing configuration.
type Config struct {
	BaseURL     string
	APIKey      string
	WorkspaceID int64
}

// LoadConfig reads the publishing credentials from the process environment.
// The service entrypoint loads .env before boot; this package never reads or
// logs secret files itself.
func LoadConfig() (Config, error) {
	baseURL := firstNonEmpty(
		os.Getenv("INSTAEDIT_PUBLISHING_BASE_URL"),
		os.Getenv("INSTAEDIT_BASE_URL"),
		os.Getenv("INSTAEDIT_URL"),
		defaultBaseURL,
	)
	apiKey := strings.TrimSpace(os.Getenv(APIKeyEnv))
	if apiKey == "" {
		return Config{}, fmt.Errorf("%s is not configured", APIKeyEnv)
	}
	rawWorkspace := strings.TrimSpace(os.Getenv(WorkspaceIDEnv))
	if rawWorkspace == "" {
		return Config{}, fmt.Errorf("%s is not configured", WorkspaceIDEnv)
	}
	workspaceID, err := strconv.ParseInt(rawWorkspace, 10, 64)
	if err != nil || workspaceID <= 0 {
		return Config{}, fmt.Errorf("%s must be a positive integer", WorkspaceIDEnv)
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return Config{}, fmt.Errorf("invalid InstaEdit publishing base URL")
	}
	return Config{BaseURL: baseURL, APIKey: apiKey, WorkspaceID: workspaceID}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
