package config

import "fmt"

// ScriptDefaultsConfig contains generation defaults after YAML and env
// resolution. Consumers must receive these values from the bootstrap
// snapshot; they must not invent a second WPM or language default.
type ScriptDefaultsConfig struct {
	WordsPerMinute int    `yaml:"words_per_minute" env:"VELOX_SCRIPTS_WORDS_PER_MINUTE" default:"150"`
	SafetyLanguage string `yaml:"safety_language" env:"VELOX_SCRIPTS_SAFETY_LANGUAGE" default:"en"`
}

// VoiceoverDefaultsConfig contains request and pipeline defaults that are
// resolved once at bootstrap and then treated as immutable runtime policy.
type VoiceoverDefaultsConfig struct {
	DefaultFilenameTemplate string `yaml:"default_filename_template" env:"VELOX_VOICEOVER_FILENAME_TEMPLATE" default:"{slug}_{lang}.mp3"`
	DefaultStrategy         string `yaml:"default_strategy" env:"VELOX_VOICEOVER_STRATEGY" default:"verify"`
	DefaultLanguage         string `yaml:"default_language" env:"VELOX_VOICEOVER_LANGUAGE" default:"en"`
	DefaultParallelism      int    `yaml:"default_parallelism" env:"VELOX_VOICEOVER_DEFAULT_PARALLELISM" default:"3"`
	MaxParallelism          int    `yaml:"max_parallelism" env:"VELOX_VOICEOVER_MAX_PARALLELISM" default:"8"`
	ChunkConcurrency        int    `yaml:"chunk_concurrency" env:"VELOX_VOICEOVER_CHUNK_CONCURRENCY" default:"2"`
}

// ResolvedConfig is the post-bootstrap configuration snapshot. Its embedded
// Config is a value copy: callers receive a view of the snapshot and cannot
// mutate the loader's original value. Runtime code must only construct this
// type through Resolve or GetResolvedFromPath.
type ResolvedConfig struct {
	Config
}

// GetResolvedFromPath loads, validates, and freezes one configuration snapshot.
func GetResolvedFromPath(path string) (*ResolvedConfig, error) {
	cfg, err := GetFromPath(path)
	if err != nil {
		return nil, err
	}
	return cfg.Resolve()
}

// Resolve applies the final validation boundary and freezes a value copy.
func (c *Config) Resolve() (*ResolvedConfig, error) {
	if c == nil {
		return nil, fmt.Errorf("config: cannot resolve nil configuration")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &ResolvedConfig{Config: *c}, nil
}

// View returns an isolated compatibility view for legacy composition ports.
// New code should retain the ResolvedConfig snapshot instead of mutating it.
func (c *ResolvedConfig) View() *Config {
	if c == nil {
		return nil
	}
	copy := c.Config
	return &copy
}
