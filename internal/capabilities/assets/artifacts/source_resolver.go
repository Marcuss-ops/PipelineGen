package artifacts

import (
	"context"
	"errors"
	"sort"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"

)

var ErrSourceCatalogDependencyUnavailable = errors.New("artifacts: source catalog dependency unavailable")

// SourceRepo is the application-owned repository port used by source-aware
// deletion and lookup flows. Concrete SQLite repositories are adapted in
// infrastructure and injected at the composition root.
type SourceRepo interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	GetByDriveFileID(ctx context.Context, driveFileID string) (*asset.Asset, error)
	Delete(ctx context.Context, id string) error
}

// SourceCatalog is the canonical source-to-repository registry. It owns
// source identity and dispatch policy, but never constructs infrastructure
// repositories itself.
type SourceCatalog struct {
	byCanonical map[string]SourceRepo
}

// NewSourceCatalog builds the registry from canonical repository ports in the
// fixed order artlist, clips, stock, voiceover, images. The sound_effect
// source is an alias of clips. Missing ports fail closed at composition time.
func NewSourceCatalog(repos ...SourceRepo) (*SourceCatalog, error) {
	if len(repos) != 5 {
		return nil, ErrSourceCatalogDependencyUnavailable
	}
	for _, repo := range repos {
		if repo == nil {
			return nil, ErrSourceCatalogDependencyUnavailable
		}
	}
	return &SourceCatalog{byCanonical: map[string]SourceRepo{
		"artlist":      repos[0],
		"clips":        repos[1],
		"stock":        repos[2],
		"voiceover":    repos[3],
		"images":       repos[4],
		"sound_effect": repos[1],
	}}, nil
}

// Resolve returns the repository port for a canonical source or alias.
func (c *SourceCatalog) Resolve(source string) (SourceRepo, bool) {
	if c == nil {
		return nil, false
	}
	canonical := c.Normalize(source)
	repo, ok := c.byCanonical[canonical]
	return repo, ok
}

// Normalize resolves a source alias through the kernel source catalog.
func (c *SourceCatalog) Normalize(source string) string {
	return detail.DefaultSourceCatalog().Canonical(source)
}

// MediaType returns the canonical media type for a source.
func (c *SourceCatalog) MediaType(source string) string {
	def, ok := detail.DefaultSourceCatalog().Definition(source)
	if !ok {
		return ""
	}
	return def.MediaType
}

// Names returns registered canonical names in deterministic order.
func (c *SourceCatalog) Names() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.byCanonical))
	for name := range c.byCanonical {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CanonicalSource delegates source identity to the kernel-owned catalog.
func CanonicalSource(source string) string {
	return detail.DefaultSourceCatalog().Canonical(source)
}

// IsValidSource reports whether source or alias is registered by the kernel
// catalog.
func IsValidSource(source string) bool {
	return CanonicalSource(source) != ""
}

// IsClipsSource reports whether a source uses the clips-family repository.
func IsClipsSource(source string) bool {
	switch CanonicalSource(source) {
	case "artlist", "clips", "stock", "sound_effect":
		return true
	default:
		return false
	}
}
