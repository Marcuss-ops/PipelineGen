package images

import imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"

// Root compatibility aliases. Canonical StyleRegistry ownership lives in images/styles.
type StyleRegistry = imagestyles.StyleRegistry

var ErrRegistryReadOnly = imagestyles.ErrRegistryReadOnly

func NewStyleRegistry(yamlPath string) (*StyleRegistry, error) {
	return imagestyles.NewStyleRegistry(yamlPath)
}
