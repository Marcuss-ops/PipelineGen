package config

import (
	"os"
	"path/filepath"
)

func resolveRelativePath(path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	fallback := filepath.Join("src/go-master", path)
	if _, err := os.Stat(fallback); err == nil {
		return fallback
	}
	return path
}

// GetCredentialsPath returns the full path to the Google OAuth credentials file.
func (c *Config) GetCredentialsPath() string {
	return resolveRelativePath(c.Paths.CredentialsFile)
}

// GetTokenPath returns the full path to the Google OAuth token file.
func (c *Config) GetTokenPath() string {
	return resolveRelativePath(c.Paths.TokenFile)
}

// GetLogLevel returns the configured log level.
func (c *Config) GetLogLevel() string {
	return c.Logging.Level
}

// GetLogFormat returns the configured log format.
func (c *Config) GetLogFormat() string {
	return c.Logging.Format
}
