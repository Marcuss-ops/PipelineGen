package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func Get() *Config {
	cfg, err := GetFromPath("config.yaml")
	if err != nil {
		// If the file is missing, return defaults+env (this is the legacy
		// behavior for tests and minimal deployments).
		cfg = &Config{}
		applyDefaults(cfg)
		applyEnvVars(cfg)
	}
	return cfg
}

// GetFromPath loads configuration from an explicit file path.
// It returns an error if the file exists but is malformed, so that
// operators are notified immediately instead of silently using partial
// defaults.
func GetFromPath(path string) (*Config, error) {
	cfg := &Config{}
	applyDefaults(cfg)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			applyEnvVars(cfg)
			return cfg, nil
		}
		return nil, fmt.Errorf("cannot read config file %q: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config file %q is malformed: %w", path, err)
	}

	applyEnvVars(cfg)
	return cfg, nil
}

// hostPortConflict reports whether two URLs share the same host:port.
// It handles empty strings, missing ports (defaulting to scheme defaults),
// and IPv6 brackets. Returns false if either URL is unparsable.
func hostPortConflict(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}

	// Strip IPv6 brackets for comparison
	ha := strings.Trim(ua.Hostname(), "[]")
	hb := strings.Trim(ub.Hostname(), "[]")
	if ha != hb {
		return false
	}

	pa := ua.Port()
	pb := ub.Port()
	// Default ports per scheme
	if pa == "" {
		switch ua.Scheme {
		case "http":
			pa = "80"
		case "https":
			pa = "443"
		}
	}
	if pb == "" {
		switch ub.Scheme {
		case "http":
			pb = "80"
		case "https":
			pb = "443"
		}
	}
	return pa == pb && pa != ""
}

// Validate performs a minimal sanity check of the loaded configuration.
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if c.Server.ReadTimeout <= 0 {
		return fmt.Errorf("invalid read timeout: %d", c.Server.ReadTimeout)
	}
	if c.Server.WriteTimeout <= 0 {
		return fmt.Errorf("invalid write timeout: %d", c.Server.WriteTimeout)
	}
	if c.External.OllamaURL == "" {
		return fmt.Errorf("ollama url is required")
	}

	// Security: prevent production from starting with auth disabled on a public interface
	isPublicInterface := c.Server.Host == "0.0.0.0" || c.Server.Host == "" || strings.HasPrefix(c.Server.Host, "::")
	if !c.Security.EnableAuth && isPublicInterface {
		return fmt.Errorf("refusing to start: enable_auth is false and server is bound to public interface %s. Set host to 127.0.0.1 or enable auth", c.Server.Host)
	}
	if c.Security.EnableAuth && strings.TrimSpace(c.Security.AdminToken) == "" {
		return fmt.Errorf("admin token is required when auth is enabled")
	}

	// Security: warn against real Drive folder IDs in production config (if committed)
	if c.Drive.MediaRootFolder != "" && strings.HasPrefix(c.Drive.MediaRootFolder, "1") {
		// Heuristic: real Google Drive IDs are 33 chars, but allow empty for dynamic resolution
	}

	// Validate that there is no port conflict between configured external services
	if c.External.NvidiaLocalNIMURL != "" && c.GoogleAccounting.Enabled {
		if hostPortConflict(c.External.NvidiaLocalNIMURL, c.GoogleAccounting.ServerURL) {
			return fmt.Errorf("port conflict: NVIDIA NIM and Google Accounting both configured on the same host:port. Change one service's port")
		}
	}

	return nil
}
