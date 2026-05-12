package ontology

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadRegistry loads the ontology registry from a YAML file.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read ontology file: %w", err)
	}

	var topics map[string]TopicRule
	if err := yaml.Unmarshal(data, &topics); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ontology YAML: %w", err)
	}

	return &Registry{Topics: topics}, nil
}
