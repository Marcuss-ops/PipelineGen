package main

import (
	"errors"
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/nlp"
)

// newNERBackend constructs the backend selected by name. The returned closer is
// always non-nil (a no-op when the backend owns no lifecycle of its own).
func newNERBackend(name, rustPath, spacyURL string) (scriptgen.NERBackend, func(), error) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "rust":
		adapter, err := rustexec.NewVisualNERAdapter(rustexec.NewExecutor(rustPath, "", nil))
		if err != nil {
			return nil, nil, err
		}
		return adapter, adapter.Close, nil
	case "spacy":
		if strings.TrimSpace(spacyURL) == "" {
			return nil, nil, errors.New("--spacy-url is required for --backend=spacy")
		}
		adapter, err := nlp.NewSpacyNERAdapter(spacyURL, nil)
		if err != nil {
			return nil, nil, err
		}
		return adapter, func() {}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported --backend %q (choose rust or spacy)", normalized)
	}
}
