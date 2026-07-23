package config

// LessonsConfig holds settings for the lesson generation service.
type LessonsConfig struct {
	Enabled             bool   `yaml:"enabled" default:"true"`
	DefaultModel        string `yaml:"default_model" default:"gemma4:e4b"`
	DefaultTone         string `yaml:"default_tone" default:"educational"`
	DefaultLanguage     string `yaml:"default_language" default:"it"`
	DefaultImageModel   string `yaml:"default_image_model" default:"flux-1-dev"`
	MaxParallelChapters int    `yaml:"max_parallel_chapters" default:"5"`
}
