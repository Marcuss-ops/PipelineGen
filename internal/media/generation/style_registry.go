package generation

import (
	"fmt"
	"os"
	"strings"
	"sync"

	domainmedia "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"gopkg.in/yaml.v3"
)

// StyleRegistry manages a collection of generation styles
type StyleRegistry struct {
	styles map[string]domainmedia.GenerationStyle
	mu     sync.RWMutex
}

// NewStyleRegistry creates a new registry and loads styles from the given YAML file
func NewStyleRegistry(yamlPath string) (*StyleRegistry, error) {
	r := &StyleRegistry{
		styles: make(map[string]domainmedia.GenerationStyle),
	}

	if err := r.Load(yamlPath); err != nil {
		return nil, err
	}

	return r, nil
}

// Load reads styles from a YAML file
func (r *StyleRegistry) Load(yamlPath string) error {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("failed to read styles file: %w", err)
	}

	var container domainmedia.GenerationStyles
	if err := yaml.Unmarshal(data, &container); err != nil {
		return fmt.Errorf("failed to unmarshal styles: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset if loading again
	r.styles = make(map[string]domainmedia.GenerationStyle)
	for _, s := range container.Styles {
		r.styles[strings.ToLower(s.Name)] = s
	}

	return nil
}

// Get retrieves a style by name (case-insensitive)
func (r *StyleRegistry) Get(name string) (domainmedia.GenerationStyle, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.styles[strings.ToLower(name)]
	return s, ok
}

// List returns all available styles
func (r *StyleRegistry) List() []domainmedia.GenerationStyle {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]domainmedia.GenerationStyle, 0, len(r.styles))
	for _, s := range r.styles {
		res = append(res, s)
	}
	return res
}

// ApplyStyle appends the style description to the prompt if the style exists
func (r *StyleRegistry) ApplyStyle(prompt, styleName string) string {
	if styleName == "" {
		return prompt
	}

	style, ok := r.Get(styleName)
	if !ok {
		return prompt
	}

	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return style.Description
	}

	// Avoid duplicates if prompt already contains the description
	if textutil.ContainsCI(prompt, style.Description) {
		return prompt
	}

	return fmt.Sprintf("%s, %s", prompt, style.Description)
}
