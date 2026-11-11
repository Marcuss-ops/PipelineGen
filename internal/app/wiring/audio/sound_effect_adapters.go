package audio

import (
	"context"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
)

// SoundEffectAdapters are the narrow capability ports built from platform
// repositories and the canonical metadata/outbox writers.
type SoundEffectAdapters struct {
	Clips       sfxports.ClipRepositoryPort
	Metadata    sfxports.SemanticMetadataWriterPort
	Destination sfxports.DestinationResolverPort
	Dispatcher  sfxports.DispatcherPort
}

// NewSoundEffectAdapters creates the sound-effect adapters at the composition
// boundary. Destination resolution remains independent of Drive publication.
func NewSoundEffectAdapters(
	clips *assets.ClipsRepository,
	metadata semantic.MetadataWriterPort,
	dispatcher *outbox.Dispatcher,
	mediaRoot string,
) SoundEffectAdapters {
	return SoundEffectAdapters{
		Clips:       clipsRepoAdapter{repo: clips},
		Metadata:    semanticWriterAdapter{writer: metadata},
		Destination: destinationResolver{mediaRoot: mediaRoot},
		Dispatcher:  newDispatcherAdapter(dispatcher),
	}
}

type clipsRepoAdapter struct{ repo *assets.ClipsRepository }

var _ sfxports.ClipRepositoryPort = clipsRepoAdapter{}

func (a clipsRepoAdapter) Upsert(ctx context.Context, clip *asset.Asset) error {
	return a.repo.Upsert(ctx, clip)
}

type semanticWriterAdapter struct{ writer semantic.MetadataWriterPort }

var _ sfxports.SemanticMetadataWriterPort = semanticWriterAdapter{}

func (a semanticWriterAdapter) Write(ctx context.Context, req sfxports.MetadataWriteRequest) (*sfxports.MetadataWriteResponse, error) {
	concreteReq := semantic.WriteRequest{
		AssetID: req.AssetID, AssetType: req.AssetType, MediaType: req.MediaType,
		Source: req.Source, Generator: req.Generator, Style: req.Style,
		Prompt: req.Prompt, LocalPath: req.LocalPath,
	}
	res, err := a.writer.Write(ctx, concreteReq)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Payload == nil {
		return nil, nil
	}
	return &sfxports.MetadataWriteResponse{SearchText: res.Payload.SearchText, Tags: res.Payload.Tags}, nil
}

type destinationResolver struct{ mediaRoot string }

var _ sfxports.DestinationResolverPort = destinationResolver{}

func (a destinationResolver) Resolve(req sfxports.AssetDestinationRequest) (sfxports.ResolvedDest, error) {
	source := req.Source
	if source == "" {
		source = "media"
	}
	subject := req.Group
	if subject == "" {
		subject = "unknown"
	}
	ext := req.Ext
	if ext == "" {
		ext = ".bin"
	}
	rel := filepath.Join(source, subject+ext)
	localPath := ""
	if a.mediaRoot != "" {
		localPath = filepath.Join(a.mediaRoot, rel)
	}
	return sfxports.ResolvedDest{LocalPath: localPath}, nil
}

type dispatcherAdapter struct{ dispatcher *outbox.Dispatcher }

var _ sfxports.DispatcherPort = (*dispatcherAdapter)(nil)

func newDispatcherAdapter(dispatcher *outbox.Dispatcher) sfxports.DispatcherPort {
	if dispatcher == nil {
		return nil
	}
	return &dispatcherAdapter{dispatcher: dispatcher}
}

func (a *dispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if a == nil || a.dispatcher == nil {
		return nil
	}
	return a.dispatcher.EnqueueAndIndex(ctx, clip, contentHash)
}
