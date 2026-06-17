package prompts

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"text/template"

	"velox/go-master/internal/ml/ollama/types"

	"gopkg.in/yaml.v3"
)

// Config holds all prompt templates loaded from the YAML config file.
type Config struct {
	System struct {
		Base      string            `yaml:"base"`
		Tones     map[string]string `yaml:"tones"`
		Languages map[string]string `yaml:"languages"`
	} `yaml:"system"`

	Description struct {
		System string `yaml:"system"`
		User   string `yaml:"user"`
	} `yaml:"description"`

	VisualPrompt struct {
		System string `yaml:"system"`
		User   string `yaml:"user"`
	} `yaml:"visual_prompt"`

	Translation struct {
		System string `yaml:"system"`
		User   string `yaml:"user"`
	} `yaml:"translation"`

	VideoMetadata struct {
		System string `yaml:"system"`
		User   string `yaml:"user"`
	} `yaml:"video_metadata"`

	ScriptGeneration struct {
		User                   string `yaml:"user"`
		OverridingInstructions string `yaml:"overriding_instructions"`
	} `yaml:"script_generation"`

	ScriptRegeneration struct {
		User string `yaml:"user"`
	} `yaml:"script_regeneration"`

	EntityExtraction string `yaml:"entity_extraction"`
	TimelineRouting  string `yaml:"timeline_routing"`
	Classification   string `yaml:"classification"`

	QAPass struct {
		System string `yaml:"system"`
		Body   string `yaml:"body"`
	} `yaml:"qa_pass"`

	CoherencePass struct {
		System string `yaml:"system"`
		Body   string `yaml:"body"`
	} `yaml:"coherence_pass"`

	Expand struct {
		Body string `yaml:"body"`
	} `yaml:"expand"`

	Compress struct {
		Body string `yaml:"body"`
	} `yaml:"compress"`

	QualityCompress string `yaml:"quality_compress"`

	MemoryEnriched struct {
		Sections struct {
			ChannelMemory     string `yaml:"channel_memory"`
			PastScripts       string `yaml:"past_scripts"`
			ResearchMemory    string `yaml:"research_memory"`
			AdditionalContext string `yaml:"additional_context"`
		} `yaml:"sections"`
		Instruction      string `yaml:"instruction"`
		UserRequestLabel string `yaml:"user_request_label"`
		WriteScriptLine  string `yaml:"write_script_line"`
		DetailsLine      string `yaml:"details_line"`
		LanguageLine     string `yaml:"language_line"`
		TruncatedSuffix  string `yaml:"truncated_suffix"`
	} `yaml:"memory_enriched"`

	MemoryFreshVariant struct {
		AngleShiftHeader string `yaml:"angle_shift_header"`
		AngleShiftBody   string `yaml:"angle_shift_body"`
		AvoidListHeader  string `yaml:"avoid_list_header"`
		AvoidListIntro   string `yaml:"avoid_list_intro"`
		AvoidListFooter  string `yaml:"avoid_list_footer"`
		FinalInstruction string `yaml:"final_instruction"`
	} `yaml:"memory_fresh_variant"`

	Filtering struct {
		StopPhrases      []string `yaml:"stop_phrases"`
		SpeakerLabels    []string `yaml:"speaker_labels"`
		MetaContentTypes []string `yaml:"meta_content_types"`
	} `yaml:"filtering"`
}

// MemorySection is a labeled group of memory items for the enriched prompt.
type MemorySection struct {
	Type  string   // "channel", "past", "research", "other"
	Items []string // formatted lines (already truncated by caller)
}

// Registry is the global prompt registry singleton.
var (
	registry     *Config
	registryOnce sync.Once
	registryErr  error
)

// Load loads the prompt config from the given YAML file path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read prompts config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse prompts config %s: %w", path, err)
	}
	return &cfg, nil
}

// MustLoad is like Load but panics on error.
func MustLoad(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		panic(err)
	}
	return cfg
}

// Init initializes the global registry once. Safe to call multiple times.
// After loading, it applies the filtering config to the types package.
func Init(path string) error {
	registryOnce.Do(func() {
		registry, registryErr = Load(path)
		if registryErr == nil && registry != nil {
			registry.ApplyFilteringConfig()
		}
	})
	return registryErr
}

// Get returns the global registry. Returns nil if Init was never called or failed.
func Get() *Config {
	return registry
}

// render executes a Go template string with the given data map.
func render(tmplStr string, data map[string]any) (string, error) {
	t, err := template.New("").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}
	return buf.String(), nil
}

// joinStrings joins a slice of strings with ", ".
func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ", "
		}
		result += s
	}
	return result
}

// ApplyFilteringConfig applies the filtering lists from the YAML config
// to the types package, overriding the hardcoded defaults.
func (c *Config) ApplyFilteringConfig() {
	if c == nil {
		return
	}
	if len(c.Filtering.StopPhrases) > 0 || len(c.Filtering.SpeakerLabels) > 0 || len(c.Filtering.MetaContentTypes) > 0 {
		types.SetFilteringConfig(types.FilteringConfig{
			StopPhrases:      c.Filtering.StopPhrases,
			SpeakerLabels:    c.Filtering.SpeakerLabels,
			MetaContentTypes: c.Filtering.MetaContentTypes,
		})
	}
}
