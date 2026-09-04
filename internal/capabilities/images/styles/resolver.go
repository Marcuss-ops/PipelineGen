package styles

import (
	"fmt"
	"strings"
)

// SourceBackend abstracts style lookup independently of YAML/DB storage.
type SourceBackend interface {
	GetStyle(styleID string) (StyleSnapshot, error)
}

type StyleSnapshot struct {
	ID             string
	Version        int
	PromptSuffix   string
	NegativePrompt string
	DestinationKey string
	Enabled        bool
}

type StyleResolver interface {
	Resolve(styleID, provider, model string) (ResolvedStyle, error)
	Validate(styleID, provider, model string) error
}

const DefaultStyleID = "default"

func New(source SourceBackend) StyleResolver {
	if source == nil {
		return &failingResolver{err: ErrStyleNotFound}
	}
	return &resolver{source: source}
}

type failingResolver struct{ err error }

func (r *failingResolver) Resolve(_, _, _ string) (ResolvedStyle, error) { return ResolvedStyle{}, r.err }
func (r *failingResolver) Validate(_, _, _ string) error                 { return r.err }

type resolver struct{ source SourceBackend }

var _ StyleResolver = (*resolver)(nil)
var _ StyleResolver = (*failingResolver)(nil)

func (r *resolver) Resolve(styleID, _, _ string) (ResolvedStyle, error) {
	if r == nil || r.source == nil {
		return ResolvedStyle{}, ErrStyleNotFound
	}
	if strings.TrimSpace(styleID) == "" {
		styleID = DefaultStyleID
	}
	snap, err := r.source.GetStyle(styleID)
	if err != nil || snap.ID == "" {
		return ResolvedStyle{}, fmt.Errorf("%w: %s", ErrStyleNotFound, styleID)
	}
	if !snap.Enabled {
		return ResolvedStyle{}, fmt.Errorf("%w: %s", ErrStyleDisabled, styleID)
	}
	return ResolvedStyle{
		ID:             snap.ID,
		Version:        snap.Version,
		PromptSuffix:   snap.PromptSuffix,
		NegativePrompt: snap.NegativePrompt,
		DestinationKey: snap.DestinationKey,
		Enabled:        snap.Enabled,
	}, nil
}

func (r *resolver) Validate(styleID, provider, model string) error {
	_, err := r.Resolve(styleID, provider, model)
	return err
}
