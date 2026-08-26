package config

// RerankerConfig holds settings for the CrossEncoder reranking service.
// The reranker is an optional post-Qdrant reordering layer that improves
// semantic precision for all media types (clips, stock, artlist, images, voiceover).
type RerankerConfig struct {
	Enabled   bool    `yaml:"enabled" default:"false"`
	URL       string  `yaml:"url" default:"http://127.0.0.1:8091/rerank"`
	Model     string  `yaml:"model"`
	TopK      int     `yaml:"top_k" default:"30"`
	TimeoutMs int     `yaml:"timeout_ms" default:"150"`
	Weight    float64 `yaml:"weight" default:"0.35"`
}
