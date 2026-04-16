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

// Reload forces a configuration reload
func Reload() *Config {
	instance = load()
	return instance
}

// load loads configuration from environment and config file
func load() *Config {
	cfg := &Config{}

	// Set defaults first
	setDefaults(cfg)

	// Load from config file if exists
	configPath := getEnv("VELOX_CONFIG", "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err == nil {
			yaml.Unmarshal(data, cfg)
		}
	}

	// Override with environment variables
	loadFromEnv(cfg)

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
