package nlp

import (
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"go.uber.org/zap"
)

// NewBackendRegistry constructs adapters that are provisioned in this process.
// The caller explicitly selects one name; absent challengers are not replaced.
func NewBackendRegistry(selected, rustPath, ffmpegPath, spacyURL string, log *zap.Logger) (*scriptgen.VisualNERBackendRegistry, error) {
	selected = strings.ToLower(strings.TrimSpace(selected))
	if selected == "" {
		selected = "rust"
	}
	if strings.TrimSpace(rustPath) == "" {
		rustPath = "bin/visualner"
	}
	rustAdapter, err := rustexec.NewVisualNERAdapter(rustexec.NewExecutor(rustPath, ffmpegPath, log))
	if err != nil {
		return nil, fmt.Errorf("build rust NER backend: %w", err)
	}
	available := map[string]scriptgen.NERBackend{"rust": rustAdapter}
	if strings.TrimSpace(spacyURL) != "" {
		spacyAdapter, adapterErr := NewSpacyNERAdapter(spacyURL, nil)
		if adapterErr != nil {
			return nil, adapterErr
		}
		available["spacy"] = spacyAdapter
	}
	registry, err := scriptgen.NewVisualNERBackendRegistry(available)
	if err != nil {
		return nil, err
	}
	if _, err := registry.Resolve(selected); err != nil {
		return nil, err
	}
	return registry, nil
}
