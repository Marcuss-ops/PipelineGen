// Package config provides configuration management for the VeloxEditing system.
package config

import (
	"fmt"
	"os"
	"path/filepath"
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

// Reload forces a configuration reload
func Reload() *Config {
	instance = load()
	return instance
}

// load loads configuration in three layers:
//   1. Defaults from struct tags (applied to zero-value fields)
//   2. YAML config file overrides (if present)
//   3. Environment variable overrides (always win)
func load() *Config {
	cfg := &Config{}

	// 1. Apply defaults from struct tags
	applyDefaults(cfg)

	// 2. Load from YAML config file if it exists
	configPath := getEnv("VELOX_CONFIG", "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err == nil {
			yaml.Unmarshal(data, cfg)
		}
	}

	// 3. Override with environment variables (highest priority)
	applyEnvVars(cfg)

	return cfg
}

// Save saves the current configuration to a YAML file
func (c *Config) Save(path string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
