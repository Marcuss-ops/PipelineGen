package wiring

import (
	"context"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
)

// sfxClipsRepoAdapter wraps *assets.ClipsRepository to satisfy
// sfxports.ClipRepositoryPort.
type sfxClipsRepoAdapter struct {
	repo *assets.ClipsRepository
}

var _ sfxports.ClipRepositoryPort = (*sfxClipsRepoAdapter)(nil)

func (a *sfxClipsRepoAdapter) Upsert(ctx context.Context, clip *asset.Asset) error {
	return a.repo.Upsert(ctx, clip)
}

// sfxSemanticWriterAdapter bridges the canonical semantic writer to the
// sound-effect capability's narrow metadata writer port.
type sfxSemanticWriterAdapter struct {
	w semantic.MetadataWriterPort
}

var _ sfxports.SemanticMetadataWriterPort = (*sfxSemanticWriterAdapter)(nil)

func (a *sfxSemanticWriterAdapter) Write(ctx context.Context, req sfxports.MetadataWriteRequest) (*sfxports.MetadataWriteResponse, error) {
	concreteReq := semantic.WriteRequest{
		AssetID:   req.AssetID,
		AssetType: req.AssetType,
		MediaType: req.MediaType,
		Source:    req.Source,
		Generator: req.Generator,
		Style:     req.Style,
		Prompt:    req.Prompt,
		LocalPath: req.LocalPath,
	}
	res, err := a.w.Write(ctx, concreteReq)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Payload == nil {
		return nil, nil
	}
	return &sfxports.MetadataWriteResponse{
		SearchText: res.Payload.SearchText,
		Tags:       res.Payload.Tags,
	}, nil
}

// sfxResolverAdapter computes the on-disk destination path for a generated
// sound effect without reaching through the Drive implementation.
type sfxResolverAdapter struct {
	mediaRoot string
}

var _ sfxports.DestinationResolverPort = (*sfxResolverAdapter)(nil)

func (a *sfxResolverAdapter) Resolve(req sfxports.AssetDestinationRequest) (sfxports.ResolvedDest, error) {
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

// sfxDispatcherAdapter keeps sound-effect writes on the canonical transactional
// asset mutation/outbox path.
type sfxDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

var _ sfxports.DispatcherPort = (*sfxDispatcherAdapter)(nil)

func newSfxDispatcherAdapter(disp *outbox.Dispatcher) sfxports.DispatcherPort {
	if disp == nil {
		return nil
	}
	return &sfxDispatcherAdapter{disp: disp}
}

func (a *sfxDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if a == nil || a.disp == nil {
		return nil
	}
	return a.disp.EnqueueAndIndex(ctx, clip, contentHash)
}
