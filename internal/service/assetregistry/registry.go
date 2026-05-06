package assetregistry

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/pkg/models"
)

// Common errors
var (
	ErrUnsupportedSource = errors.New("unsupported asset source")
	ErrNotFound         = errors.New("asset not found")
)

// ClipRegistry defines the interface for clip-based asset operations
type ClipRegistry interface {
	SearchClips(ctx context.Context, term string) ([]*models.Clip, error)
	GetClip(ctx context.Context, id string) (*models.Clip, error)
	UpsertClip(ctx context.Context, clip *models.Clip) error
	CountClips(ctx context.Context) (int, error)
	LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error)
}

// ImageRegistry defines the interface for image asset operations
type ImageRegistry interface {
	Search(ctx context.Context, query string) ([]models.ImageAsset, error)
	Get(ctx context.Context, id string) (*models.ImageAsset, error)
	Upsert(ctx context.Context, asset *models.ImageAsset) error
}

// VoiceoverRecord represents a voiceover asset
type VoiceoverRecord = voiceovers.Record

// VoiceoverRegistry defines the interface for voiceover asset operations
type VoiceoverRegistry interface {
	Search(ctx context.Context, query string) ([]VoiceoverRecord, error)
	Get(ctx context.Context, id string) (*VoiceoverRecord, error)
	Upsert(ctx context.Context, record *VoiceoverRecord) error
}

// Registry holds all asset registries organized by source
type Registry struct {
	clips     map[AssetSource]ClipRegistry
	images    ImageRegistry
	voiceovers VoiceoverRegistry
	log       *zap.Logger
}

// NewRegistry creates a new central asset registry
func NewRegistry(log *zap.Logger) *Registry {
	return &Registry{
		clips: make(map[AssetSource]ClipRegistry),
		log:   log.Named("asset_registry"),
	}
}

// RegisterClipSource registers a clip repository for a specific source
func (r *Registry) RegisterClipSource(source AssetSource, repo ClipRegistry) {
	if !source.IsValid() {
		r.log.Warn("attempted to register invalid clip source", zap.String("source", source.String()))
		return
	}
	r.clips[source] = repo
	r.log.Info("registered clip source", zap.String("source", source.String()))
}

// RegisterImages registers the image repository
func (r *Registry) RegisterImages(repo ImageRegistry) {
	r.images = repo
	r.log.Info("registered image registry")
}

// RegisterVoiceovers registers the voiceover repository
func (r *Registry) RegisterVoiceovers(repo VoiceoverRegistry) {
	r.voiceovers = repo
	r.log.Info("registered voiceover registry")
}

// GetClip retrieves a clip by source and ID
func (r *Registry) GetClip(ctx context.Context, source AssetSource, id string) (*models.Clip, error) {
	repo, ok := r.clips[source]
	if !ok {
		return nil, ErrUnsupportedSource
	}
	return repo.GetClip(ctx, id)
}

// SearchClips searches clips across a specific source or all sources
func (r *Registry) SearchClips(ctx context.Context, source AssetSource, term string) ([]*models.Clip, error) {
	if source != "" {
		repo, ok := r.clips[source]
		if !ok {
			return nil, ErrUnsupportedSource
		}
		return repo.SearchClips(ctx, term)
	}

	// Search across all clip sources
	var results []*models.Clip
	for src, repo := range r.clips {
		clips, err := repo.SearchClips(ctx, term)
		if err != nil {
			r.log.Warn("failed to search clips", zap.String("source", src.String()), zap.Error(err))
			continue
		}
		results = append(results, clips...)
	}
	return results, nil
}

// UpsertClip upserts a clip to a specific source
func (r *Registry) UpsertClip(ctx context.Context, source AssetSource, clip *models.Clip) error {
	repo, ok := r.clips[source]
	if !ok {
		return ErrUnsupportedSource
	}
	return repo.UpsertClip(ctx, clip)
}

// CountClips counts clips for a specific source or all sources
func (r *Registry) CountClips(ctx context.Context, source AssetSource) (int, error) {
	if source != "" {
		repo, ok := r.clips[source]
		if !ok {
			return 0, ErrUnsupportedSource
		}
		return repo.CountClips(ctx)
	}

	total := 0
	for src, repo := range r.clips {
		count, err := repo.CountClips(ctx)
		if err != nil {
			r.log.Warn("failed to count clips", zap.String("source", src.String()), zap.Error(err))
			continue
		}
		total += count
	}
	return total, nil
}

// GetImage retrieves an image asset by ID
func (r *Registry) GetImage(ctx context.Context, id string) (*models.ImageAsset, error) {
	if r.images == nil {
		return nil, ErrUnsupportedSource
	}
	return r.images.Get(ctx, id)
}

// SearchImages searches image assets
func (r *Registry) SearchImages(ctx context.Context, query string) ([]models.ImageAsset, error) {
	if r.images == nil {
		return nil, ErrUnsupportedSource
	}
	return r.images.Search(ctx, query)
}

// GetVoiceover retrieves a voiceover asset by ID
func (r *Registry) GetVoiceover(ctx context.Context, id string) (*VoiceoverRecord, error) {
	if r.voiceovers == nil {
		return nil, ErrUnsupportedSource
	}
	return r.voiceovers.Get(ctx, id)
}

// SearchVoiceovers searches voiceover assets
func (r *Registry) SearchVoiceovers(ctx context.Context, query string) ([]VoiceoverRecord, error) {
	if r.voiceovers == nil {
		return nil, ErrUnsupportedSource
	}
	return r.voiceovers.Search(ctx, query)
}

// Sources returns all registered clip sources
func (r *Registry) Sources() []AssetSource {
	sources := make([]AssetSource, 0, len(r.clips))
	for src := range r.clips {
		sources = append(sources, src)
	}
	return sources
}
