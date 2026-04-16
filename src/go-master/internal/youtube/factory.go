// Package youtube provides a unified YouTube client interface
package youtube

import (
	"fmt"
	"os"
	"sync"
)

// Factory creates YouTube client instances
type Factory struct {
	config  *Config
	clients map[string]Client
	mu      sync.RWMutex
}

// NewFactory creates a new client factory
func NewFactory(config *Config) *Factory {
	return &Factory{
		config:  config,
		clients: make(map[string]Client),
	}
}

// GetClient returns a YouTube client for the specified backend
func (f *Factory) GetClient(backend string) (Client, error) {
	f.mu.RLock()
	if client, ok := f.clients[backend]; ok {
		f.mu.RUnlock()
		return client, nil
	}
	f.mu.RUnlock()
	
	f.mu.Lock()
	defer f.mu.Unlock()
	
	// Double-check after acquiring write lock
	if client, ok := f.clients[backend]; ok {
		return client, nil
	}
	
	var client Client
	var err error
	
	switch backend {
	case "ytdlp", "":
		client = NewYtDlpBackend(f.config)
	case "native":
		return nil, fmt.Errorf("native backend not yet implemented")
	default:
		return nil, fmt.Errorf("unknown backend: %s", backend)
	}
	
	if err != nil {
		return nil, err
	}
	
	f.clients[backend] = client
	return client, nil
}

// DefaultClient returns the default configured client
func (f *Factory) DefaultClient() (Client, error) {
	return f.GetClient(f.config.Backend)
}

// NewClient creates a new YouTube client with the specified backend
func NewClient(backend string, config *Config) (Client, error) {
	switch backend {
	case "ytdlp", "":
		return NewYtDlpBackend(config), nil
	case "native":
		return nil, fmt.Errorf("native backend not yet implemented")
	default:
		return nil, fmt.Errorf("unknown backend: %s", backend)
	}
}

// NewDefaultClient creates a client with default configuration
func NewDefaultClient() (Client, error) {
	return NewYtDlpBackend(nil), nil
}

// Ensure directories exist
func ensureDirs(dirs ...string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	return nil
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Backend:                "ytdlp",
		YtDlpPath:             "yt-dlp",
		FFmpegPath:            "ffmpeg",
		DefaultFormat:         "best[ext=mp4]/best",
		DefaultMaxHeight:      1080,
		DefaultRetries:        3,
		MaxConcurrentDownloads: 5,
		GPUAcceleration:       false, // For AI text generation via Ollama
		GPUDevice:             0,
	}
}
