package styles

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	domain "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/styleerrors"
	"gopkg.in/yaml.v3"
)

var _ StyleResolver = (*StyleRegistry)(nil)

var ErrRegistryReadOnly = errors.New("styles.StyleRegistry: runtime Register not supported (YAML bootstrap only)")

// StyleRegistry is the canonical resolver/registry for image generation styles.
type StyleRegistry struct {
	styles map[string]domain.GenerationStyle
	mu     sync.RWMutex
}

func NewStyleRegistry(yamlPath string) (*StyleRegistry, error) {
	r := &StyleRegistry{styles: make(map[string]domain.GenerationStyle)}
	if err := r.Load(yamlPath); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *StyleRegistry) Load(yamlPath string) error {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("failed to read styles file: %w", err)
	}
	var container domain.GenerationStyles
	if err := yaml.Unmarshal(data, &container); err != nil {
		return fmt.Errorf("failed to unmarshal styles: %w", err)
	}
	next := make(map[string]domain.GenerationStyle, len(container.Styles))
	for i := range container.Styles {
		container.Styles[i].ID = domain.StyleID(container.Styles[i].Name)
		next[strings.ToLower(container.Styles[i].Name)] = container.Styles[i]
	}
	r.mu.Lock()
	r.styles = next
	r.mu.Unlock()
	return nil
}

func (r *StyleRegistry) Get(name string) (domain.GenerationStyle, bool) {
	if r == nil {
		return domain.GenerationStyle{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.styles[strings.ToLower(name)]
	return s, ok
}

func (r *StyleRegistry) Lookup(name string) (StyleDefinition, bool) { return r.Get(name) }

func (r *StyleRegistry) List() []domain.GenerationStyle {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.GenerationStyle, 0, len(r.styles))
	for _, s := range r.styles {
		out = append(out, s)
	}
	return out
}

func (r *StyleRegistry) ListEnabled() []domain.GenerationStyle {
	all := r.List()
	out := make([]domain.GenerationStyle, 0, len(all))
	for _, s := range all {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

func (r *StyleRegistry) Register(_ StyleDefinition) error { return ErrRegistryReadOnly }

func (r *StyleRegistry) ApplyStyle(prompt, styleName string, version int) (*StyleComposedPrompt, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: registry receiver is nil", styleerrors.ErrUnknownStyle)
	}
	name := strings.TrimSpace(styleName)
	if name == "" {
		return nil, fmt.Errorf("%w: styleName is empty", styleerrors.ErrUnknownStyle)
	}
	style, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", styleerrors.ErrUnknownStyle, name)
	}
	if !style.Enabled {
		return nil, fmt.Errorf("%w: %q", styleerrors.ErrStyleDisabled, name)
	}
	trimmedPrompt := strings.TrimSpace(prompt)
	trimmedSuffix := strings.TrimSpace(style.PromptSuffix)
	if trimmedPrompt == "" && trimmedSuffix == "" {
		return nil, fmt.Errorf("%w: %q", styleerrors.ErrEmptyPrompt, name)
	}
	if version > 0 && int(style.Version) != version {
		return nil, fmt.Errorf("%w: %q loaded=%d want=%d", styleerrors.ErrStyleVersionMismatch, name, int(style.Version), version)
	}
	destKey := strings.TrimSpace(style.DestinationKey)
	if destKey == "" {
		destKey = "ai-images/" + style.Name
	}
	composed := trimmedPrompt
	if trimmedSuffix != "" {
		if composed == "" {
			composed = trimmedSuffix
		} else {
			composed += ", " + trimmedSuffix
		}
	}
	return &StyleComposedPrompt{
		ComposedText: composed, StyleID: style.Name, StyleVersion: int(style.Version),
		PromptSuffix: style.PromptSuffix, NegativePrompt: style.NegativePrompt, DestinationKey: destKey,
	}, nil
}

func (r *StyleRegistry) Resolve(styleID, _, _ string) (ResolvedStyle, error) {
	if styleID == "" {
		return ResolvedStyle{}, nil
	}
	style, ok := r.Get(styleID)
	if !ok {
		return ResolvedStyle{}, fmt.Errorf("%w: %q", ErrStyleNotFound, styleID)
	}
	if !style.Enabled {
		return ResolvedStyle{}, fmt.Errorf("%w: %q", ErrStyleDisabled, styleID)
	}
	destKey := style.DestinationKey
	if destKey == "" {
		destKey = "ai-images/" + style.Name
	}
	return ResolvedStyle{
		ID: style.Name, Version: int(style.Version), PromptSuffix: style.PromptSuffix,
		NegativePrompt: style.NegativePrompt, DestinationKey: destKey, Enabled: true,
	}, nil
}

func (r *StyleRegistry) Validate(styleID, provider, model string) error {
	_, err := r.Resolve(styleID, provider, model)
	return err
}

func (r *StyleRegistry) Inner() *StyleRegistry { return r }

// GetStyle adapts the registry to SourceBackend for callers that want the generic resolver.
func (r *StyleRegistry) GetStyle(styleID string) (StyleSnapshot, error) {
	style, ok := r.Get(styleID)
	if !ok {
		return StyleSnapshot{}, ErrStyleNotFound
	}
	return StyleSnapshot{
		ID: style.Name, Version: int(style.Version), PromptSuffix: style.PromptSuffix,
		NegativePrompt: style.NegativePrompt, DestinationKey: style.DestinationKey, Enabled: style.Enabled,
	}, nil
}
