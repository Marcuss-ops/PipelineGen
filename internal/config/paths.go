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
	candidates := []string{
		path,
		filepath.Join("src/go-master", path),
		filepath.Join("..", path),
		filepath.Join("..", "..", path),
		filepath.Join("..", "..", "..", path),
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, path),
			filepath.Join(home, "Downloads", path),
		)
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
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
