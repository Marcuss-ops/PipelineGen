package config

import "time"

// ClipIndexerConfig holds configuration for the ClipIndexer service.
// It provides the URL and launcher for the compute-only embedding sidecar.
type ClipIndexerConfig struct {
	Enabled               bool   `yaml:"enabled" default:"true"`
	ServerURL             string `yaml:"server_url" env:"VELOX_CLIP_INDEXER_SERVER_URL" default:"http://127.0.0.1:8001"`
	ScriptPath            string `yaml:"script_path" default:"scripts/start_embedding_server.sh"`
	PythonBin             string `yaml:"python_bin" default:"python3"`
	AutoIndexAfterArtlist bool   `yaml:"auto_index_after_artlist" default:"true"`
	// MaxConcurrentIndexing limits parallel Python subprocesses launched for clip indexing.
	// Delegates to ConcurrencyConfig.MaxConcurrentClipIndexing at wiring time.
	MaxConcurrentIndexing int `yaml:"max_concurrent_indexing" env:"VELOX_CONCURRENT_CLIP_INDEXING" default:"10"`
	// EmbedTimeoutSeconds is the deadline for ONE /embed call to the sidecar.
	//
	// The sidecar processes a bounded queue per inference slot on a SHARED
	// CPU, so a single document (50-200 ms of E5 inference) can legitimately
	// wait behind several queued inferences. A fixed client deadline turns
	// that ordinary backpressure into a spurious "context deadline exceeded",
	// so the bound is configurable and sized for the deployment's queue depth
	// rather than for one document. 0 falls back to the client default.
	EmbedTimeoutSeconds int `yaml:"embed_timeout_seconds" env:"VELOX_CLIP_INDEXER_EMBED_TIMEOUT_S" default:"60"`
}

// EmbedTimeout projects EmbedTimeoutSeconds onto a time.Duration, returning 0
// (meaning "use the client default") for a non-positive value so a bad config
// never disables the deadline entirely.
func (c ClipIndexerConfig) EmbedTimeout() time.Duration {
	if c.EmbedTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(c.EmbedTimeoutSeconds) * time.Second
}
