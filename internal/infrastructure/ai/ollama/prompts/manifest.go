package prompts

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type promptManifest struct {
	Includes []string `yaml:"includes"`
}

// loadConfigData reads either a regular prompt YAML file or a manifest whose
// includes are merged by unique top-level key. Relative includes are resolved
// from the manifest directory.
func loadConfigData(path string) ([]byte, error) {
	return loadPromptFile(filepath.Clean(path), make(map[string]bool))
}

func loadPromptFile(path string, stack map[string]bool) ([]byte, error) {
	if stack[path] {
		return nil, fmt.Errorf("prompt manifest include cycle at %s", path)
	}
	stack[path] = true
	defer delete(stack, path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read prompts config %s: %w", path, err)
	}

	var manifest promptManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse prompts config %s: %w", path, err)
	}
	if len(manifest.Includes) == 0 {
		return data, nil
	}

	merged := make(map[string]any)
	for _, include := range manifest.Includes {
		includePath := include
		if !filepath.IsAbs(includePath) {
			includePath = filepath.Join(filepath.Dir(path), includePath)
		}
		childData, err := loadPromptFile(filepath.Clean(includePath), stack)
		if err != nil {
			return nil, err
		}
		var partial map[string]any
		if err := yaml.Unmarshal(childData, &partial); err != nil {
			return nil, fmt.Errorf("failed to parse prompt include %s: %w", includePath, err)
		}
		for key, value := range partial {
			if _, exists := merged[key]; exists {
				return nil, fmt.Errorf("duplicate prompt section %q while loading %s", key, path)
			}
			merged[key] = value
		}
	}

	result, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("failed to merge prompt manifest %s: %w", path, err)
	}
	return result, nil
}
