package config

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
}
