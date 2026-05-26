package generation

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
	"velox/go-master/internal/media/models"
)

// StyleRegistry manages a collection of generation styles
type StyleRegistry struct {
	styles map[string]models.GenerationStyle
	mu     sync.RWMutex
}

// NewStyleRegistry creates a new registry and loads styles from the given YAML file
func NewStyleRegistry(yamlPath string) (*StyleRegistry, error) {
	r := &StyleRegistry{
		styles: make(map[string]models.GenerationStyle),
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

	var container models.GenerationStyles
	if err := yaml.Unmarshal(data, &container); err != nil {
		return fmt.Errorf("failed to unmarshal styles: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset if loading again
	r.styles = make(map[string]models.GenerationStyle)
	for _, s := range container.Styles {
		r.styles[strings.ToLower(s.Name)] = s
	}

	return nil
}

// Get retrieves a style by name (case-insensitive)
func (r *StyleRegistry) Get(name string) (models.GenerationStyle, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.styles[strings.ToLower(name)]
	return s, ok
}

// List returns all available styles
func (r *StyleRegistry) List() []models.GenerationStyle {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]models.GenerationStyle, 0, len(r.styles))
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
	if strings.Contains(strings.ToLower(prompt), strings.ToLower(style.Description)) {
		return prompt
	}

	return fmt.Sprintf("%s, %s", prompt, style.Description)
}
