package videocreate

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	audio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	delivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Production adapters (the composition-root bindings) ───────────────
//
// Every adapter here binds ONE port to the canonical implementation the
// composition root already owns. The workflow itself stays free of
// concrete services (AGENTS.md Pattern 0); tests bind hermetic fakes
// against the SAME ports. No adapter spawns a media binary and none
// calls the workflow's own HTTP surface.

// childJobs binds ChildJobs to the canonical job registry. Enqueue is
// idempotent on (ChildClientID, idempotency key) — the broker answers a
// duplicate with the EXISTING child id, which is what makes §8 replay
// safety real instead of aspirational.
type childJobs struct{ svc job.Service }

// NewChildJobs binds the canonical job registry surface.
func NewChildJobs(svc job.Service) ChildJobs { return childJobs{svc: svc} }

func (a childJobs) HasHandler(jobType string) bool {
	if a.svc == nil || jobType == "" {
		return false
	}
	lookup, ok := a.svc.(interface{ HasHandler(string) bool })
	return ok && lookup.HasHandler(jobType)
}

func (a childJobs) EnqueueChild(ctx context.Context, req ChildJobRequest) (string, error) {
	if a.svc == nil {
		return "", fmt.Errorf("videocreate: child jobs service is not wired")
	}
	created, err := a.svc.Enqueue(ctx, &job.EnqueueRequest{
		Type:           req.JobType,
		Payload:        req.Payload,
		CorrelationID:  req.CorrelationID,
		Project:        req.Project,
		VideoName:      req.VideoName,
		ClientID:       ChildClientID,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return "", fmt.Errorf("videocreate: enqueue %s child: %w", req.JobType, err)
	}
	if created == nil || created.ID == "" {
		return "", fmt.Errorf("videocreate: enqueue %s child returned no job id", req.JobType)
	}
	return created.ID, nil
}

// WaitTerminal blocks until the child reaches a terminal state. The
// canonical Service surface is request/response, so this is a bounded
// poll — the same pattern the parent-completion aggregators use.
func (a childJobs) WaitTerminal(ctx context.Context, childJobID string) (*job.Job, error) {
	if a.svc == nil {
		return nil, fmt.Errorf("videocreate: child jobs service is not wired")
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := a.svc.Get(ctx, childJobID)
		if err != nil {
			return nil, fmt.Errorf("videocreate: get child %s: %w", childJobID, err)
		}
		if current != nil && a.svc.IsTerminal(current.Status) {
			return current, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("videocreate: wait child %s: %w", childJobID, ctx.Err())
		case <-ticker.C:
		}
	}
}

// aggregatorSearch binds MediaSearch to the canonical SearchAggregator
// (the exact backend /api/media/search serves). §10: the workflow
// CONSUMES the existing search + ranking and never re-implements either.
type aggregatorSearch struct{ agg *search.Aggregator }

// NewMediaSearch binds the canonical media search aggregator.
func NewMediaSearch(agg *search.Aggregator) MediaSearch { return aggregatorSearch{agg: agg} }

func (a aggregatorSearch) Search(ctx context.Context, req MediaSearchRequest) ([]MediaCandidate, error) {
	if a.agg == nil {
		return nil, fmt.Errorf("videocreate: media search aggregator is not wired")
	}
	result, err := a.agg.Search(ctx, search.Query{
		Text:       req.Topic,
		Sources:    req.Sources,
		MediaTypes: []string{"video"},
		Limit:      req.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("videocreate: media search: %w", err)
	}
	if result == nil {
		return nil, nil
	}
	out := make([]MediaCandidate, 0, len(result.Items))
	for _, item := range result.Items {
		out = append(out, MediaCandidate{
			AssetID:    item.AssetID,
			Source:     item.Source,
			SourceURL:  item.SourceURL,
			Title:      item.Title,
			MediaType:  item.MediaType,
			DurationMS: item.DurationMs,
			Score:      item.Score,
		})
	}
	return out, nil
}

// MediaPlaneExecutor is the narrow execution seam of the canonical
// media plane (rustexec VideoProcessor): the ONLY sanctioned owner of
// media binaries (render_audio_plan / mux_audio_copy / ffprobe). The
// interface keeps this capability free of platform imports.
type MediaPlaneExecutor interface {
	RenderAudioPlan(ctx context.Context, plan audio.CompiledAudioPlan, assets audio.ResolvedAudioAssets, output string) (audio.FinalAudioAsset, error)
	MuxFinalAudioCopy(ctx context.Context, video, finalAudio, output string, asset audio.FinalAudioAsset) error
	Probe(ctx context.Context, path string) (*mediaexec.MediaInfo, error)
}

// mediaPlane binds AudioMaster + MediaProber to one MediaPlaneExecutor.
type mediaPlane struct{ exec MediaPlaneExecutor }

// NewMediaPlane binds the canonical media plane to BOTH media ports.
func NewMediaPlane(exec MediaPlaneExecutor) (AudioMaster, MediaProber) {
	p := mediaPlane{exec: exec}
	return p, p
}

func (p mediaPlane) Master(ctx context.Context, req MasterRequest) (MasteredAudio, error) {
	if p.exec == nil {
		return MasteredAudio{}, fmt.Errorf("videocreate: media plane is not wired")
	}
	asset, err := p.exec.RenderAudioPlan(ctx, req.Plan, req.Assets, req.OutputPath)
	if err != nil {
		return MasteredAudio{}, fmt.Errorf("videocreate: render_audio_plan: %w", err)
	}
	return MasteredAudio{Asset: asset, Path: req.OutputPath}, nil
}

func (p mediaPlane) Mux(ctx context.Context, req MuxRequest) (MuxedVideo, error) {
	if p.exec == nil {
		return MuxedVideo{}, fmt.Errorf("videocreate: media plane is not wired")
	}
	if err := p.exec.MuxFinalAudioCopy(ctx, req.VideoPath, req.FinalAudio.Path, req.OutputPath, req.FinalAudio.Asset); err != nil {
		return MuxedVideo{}, fmt.Errorf("videocreate: mux_audio_copy: %w", err)
	}
	return MuxedVideo{Path: req.OutputPath}, nil
}

func (p mediaPlane) Probe(ctx context.Context, path string) (ProbeFacts, error) {
	if p.exec == nil {
		return ProbeFacts{}, fmt.Errorf("videocreate: media plane is not wired")
	}
	info, err := p.exec.Probe(ctx, path)
	if err != nil {
		return ProbeFacts{}, fmt.Errorf("videocreate: probe %s: %w", path, err)
	}
	if info == nil {
		return ProbeFacts{}, fmt.Errorf("videocreate: probe %s returned no facts", path)
	}
	var size int64
	if stat, statErr := os.Stat(path); statErr == nil {
		size = stat.Size()
	}
	return ProbeFacts{
		Path:             path,
		SizeBytes:        size,
		DurationMS:       info.Duration.Milliseconds(),
		Width:            info.Width,
		Height:           info.Height,
		FPS:              info.FPS,
		VideoCodec:       info.VideoCodec,
		AudioCodec:       info.AudioCodec,
		SampleRate:       info.SampleRate,
		Channels:         info.Channels,
		VideoStreamCount: info.VideoStreamCount,
		AudioStreamCount: info.AudioStreamCount,
	}, nil
}

// assemblerViaChildren binds Assembler to the CANONICAL production
// assembly path: the assembly.prepare / assembly.finalize job contract
// (internal/kernel/assembly — the copy-certified, packet-copy segment
// assembly the production lane certifies). The certified-but-unwired
// video.assemble.copy.v1 backend (rust transform_assemble.rs) is
// deliberately NOT wired: when the cutover decision is taken it becomes
// a second implementation of THIS port (§15).
type assemblerViaChildren struct{ children ChildJobs }

// NewAssemblerViaChildren binds the canonical assembly job contract.
func NewAssemblerViaChildren(children ChildJobs) Assembler {
	return assemblerViaChildren{children: children}
}

func (a assemblerViaChildren) Assemble(ctx context.Context, req AssembleRequest) (AssembleResult, error) {
	if a.children == nil {
		return AssembleResult{}, fmt.Errorf("videocreate: assembler children are not wired")
	}
	parentJobID, parentRunID := "", ""
	if req.ParentJob != nil {
		parentJobID, parentRunID = req.ParentJob.ID, req.ParentJob.CorrelationID
	}
	prepareKey := ChildKey(req.AssemblyID, "assemble:prepare")
	preparePayload, err := marshalChild(AssemblePrepareChildRequest{
		AssemblyID: req.AssemblyID,
		Segments:   req.Segments,
	}, parentJobID, parentRunID)
	if err != nil {
		return AssembleResult{}, err
	}
	prepareID, err := a.children.EnqueueChild(ctx, ChildJobRequest{
		JobType:        appjobs.TypeAssemblyPrepare,
		IdempotencyKey: prepareKey,
		CorrelationID:  ChildCorrelationID(parentRunID, appjobs.TypeAssemblyPrepare, prepareKey),
		Payload:        preparePayload,
	})
	if err != nil {
		return AssembleResult{}, fmt.Errorf("videocreate: assembly.prepare: %w", err)
	}
	finalizeKey := ChildKey(req.AssemblyID, "assemble:finalize")
	finalizePayload, err := marshalChild(AssembleFinalizeChildRequest{
		AssemblyID:  req.AssemblyID,
		Preparation: prepareID,
		Timeline:    req.Timeline,
	}, parentJobID, parentRunID)
	if err != nil {
		return AssembleResult{}, err
	}
	finalizeID, err := a.children.EnqueueChild(ctx, ChildJobRequest{
		JobType:        appjobs.TypeAssemblyFinalize,
		IdempotencyKey: finalizeKey,
		CorrelationID:  ChildCorrelationID(parentRunID, appjobs.TypeAssemblyFinalize, finalizeKey),
		Payload:        finalizePayload,
	})
	if err != nil {
		return AssembleResult{}, fmt.Errorf("videocreate: assembly.finalize: %w", err)
	}
	if _, err := a.children.WaitTerminal(ctx, prepareID); err != nil {
		return AssembleResult{}, fmt.Errorf("videocreate: assembly.prepare wait: %w", err)
	}
	finalize, err := a.children.WaitTerminal(ctx, finalizeID)
	if err != nil {
		return AssembleResult{}, fmt.Errorf("videocreate: assembly.finalize wait: %w", err)
	}
	if finalize.Status != job.StatusSucceeded {
		return AssembleResult{}, fmt.Errorf("videocreate: assembly.finalize child %s failed: %s", finalizeID, finalize.Error)
	}
	// The canonical finalize contract (kernel/assembly FinalizeResultV1).
	var res struct {
		ArtifactID   string `json:"artifact_id"`
		ArtifactPath string `json:"artifact_path"`
	}
	if err := childResult(finalize, &res); err != nil {
		return AssembleResult{}, fmt.Errorf("videocreate: assembly.finalize result: %w", err)
	}
	if res.ArtifactID == "" || res.ArtifactPath == "" {
		return AssembleResult{}, fmt.Errorf("videocreate: assembly.finalize produced no artifact_id/artifact_path")
	}
	return AssembleResult{
		ArtifactID:  res.ArtifactID,
		Path:        res.ArtifactPath,
		ChildJobIDs: []string{prepareID, finalizeID},
	}, nil
}

// deliveryPublisher binds ArtifactPublisher to the canonical delivery
// publisher (the same Drive spine every other capability publishes
// through, size-verification included).
type deliveryPublisher struct {
	pub         delivery.Publisher
	defaultDest delivery.DestinationKey
}

// NewDeliveryPublisher binds the canonical delivery publisher. The
// default destination is used when the request carries no
// delivery_destination_id (the workflow's canonical project
// destination, §18).
func NewDeliveryPublisher(pub delivery.Publisher, defaultDestination delivery.DestinationKey) ArtifactPublisher {
	return deliveryPublisher{pub: pub, defaultDest: defaultDestination}
}

func (a deliveryPublisher) Publish(ctx context.Context, req PublishRequest) (PublishedArtifact, error) {
	if a.pub == nil {
		return PublishedArtifact{}, fmt.Errorf("videocreate: delivery publisher is not wired")
	}
	dest := delivery.DestinationKey(req.DeliveryDestinationID)
	if dest == "" {
		dest = a.defaultDest
	}
	published, err := a.pub.Publish(ctx, delivery.PublishRequest{
		Destination: dest,
		LocalPath:   req.LocalPath,
		Filename:    req.Filename,
		ContentType: req.MIMEType,
		AssetID:     req.AssetID,
		ProjectID:   req.Project,
		SizeBytes:   req.SizeBytes,
		Description: "video.create final video",
	})
	if err != nil {
		return PublishedArtifact{}, fmt.Errorf("videocreate: publish %s: %w", req.Filename, err)
	}
	if published == nil {
		return PublishedArtifact{}, fmt.Errorf("videocreate: publish %s returned no result", req.Filename)
	}
	return PublishedArtifact{
		AssetID: req.AssetID, DriveRef: DriveRef{DriveFileID: published.FileID},
		MediaURL: published.WebViewLink, DownloadURL: published.DownloadLink,
		SHA256: req.SHA256, SizeBytes: req.SizeBytes,
	}, nil
}
