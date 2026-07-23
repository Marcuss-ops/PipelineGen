package config

// VLMConfig holds settings for the VLM (Vision-Language Model) sidecar integration.
type VLMConfig struct {
	Enabled      bool    `yaml:"enabled" default:"true"`
	URL          string  `yaml:"url" default:"http://localhost:8000"`
	Model        string  `yaml:"model" default:"nvidia/nemotron-nano-12b-v2-vl:free"`
	ModelVersion string  `yaml:"model_version" default:""`
	TimeoutMs    int     `yaml:"timeout_ms" default:"120000"`
	Weight       float64 `yaml:"weight" default:"0.3"`
}
