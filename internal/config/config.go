// Package config provides configuration management for the PipelineGen system.
package config

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

var (
	instance *Config
	once     sync.Once
)

// Get returns the singleton Config instance
func Get() *Config {
	once.Do(func() {
		instance = load()
	})
	return instance
}

// getEnv retrieves an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// load loads configuration in three layers:
//  1. Defaults from struct tags (applied to zero-value fields)
//  2. YAML config file overrides (if present)
//  3. Environment variable overrides (highest priority)
func load() *Config {
	cfg := &Config{}

	// 1. Apply defaults from struct tags
	applyDefaults(cfg)

	// 2. Load from YAML config file if it exists
	configPath := getEnv("VELOX_CONFIG", "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				fmt.Printf("Warning: failed to parse YAML config: %v\n", err)
			}
		}
	}

	// 3. Override with environment variables (highest priority)
	applyEnvVars(cfg)

	return cfg
}

// Validate checks the configuration for common issues and returns an error if invalid
func (c *Config) Validate() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Check auth configuration
	if c.Security.EnableAuth && c.Security.AdminToken == "" {
		return fmt.Errorf("security.admin_token must be set when security.enable_auth is true")
	}

	// Check server configuration
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}

	// Check timeouts
	if c.Server.ReadTimeout <= 0 {
		return fmt.Errorf("server.read_timeout must be positive")
	}
	if c.Server.WriteTimeout <= 0 {
		return fmt.Errorf("server.write_timeout must be positive")
	}

	// Check Ollama URL
	if c.External.OllamaURL == "" {
		return fmt.Errorf("external.ollama_url must be set")
	}

	return nil
}
