package videocreate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	audio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	delivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	assetdetail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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
// transcriptReadiness polls the canonical text-track repository until the
// requested-language transcript is READY (bounded). The translation fan-out
// (asset.text.materialize) is triggered by the acquisition children; this
// wait only SEQUENCES the workflow's render fan-out behind that readiness.
type transcriptReadiness struct {
	repo    assetdetail.TextTrackRepository
	every   time.Duration
	timeout time.Duration
}

// NewTranscriptReadiness binds the canonical text-track repository. Poll
// cadence and deadline are the workflow's sequencing policy (2s / 5min).
func NewTranscriptReadiness(repo assetdetail.TextTrackRepository) TranscriptReady {
	return &transcriptReadiness{repo: repo, every: 2 * time.Second, timeout: 5 * time.Minute}
}

func (t *transcriptReadiness) WaitTranscriptReady(ctx context.Context, assetID, language string) error {
	if t == nil || t.repo == nil {
		return fmt.Errorf("videocreate: transcript readiness: text track repository is not wired")
	}
	lang := strings.TrimSpace(language)
	if lang == "" {
		lang = "en"
	}
	deadline := time.Now().Add(t.timeout)
	for {
		track, _, err := t.repo.FindReady(ctx, assetID, lang, assetdetail.TextTrackTranscript)
		if err != nil {
			return fmt.Errorf("videocreate: transcript readiness %q/%q: %w", assetID, lang, err)
		}
		if track != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("videocreate: transcript %q/%q not READY within %s (translation materialization did not converge)", assetID, lang, t.timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.every):
		}
	}
}

func NewMediaSearch(agg *search.Aggregator) MediaSearch { return aggregatorSearch{agg: agg} }

func (a aggregatorSearch) Search(ctx context.Context, req MediaSearchRequest) ([]MediaCandidate, error) {
	if a.agg == nil {
		return nil, fmt.Errorf("videocreate: media search aggregator is not wired")
	}
	// The durable parent is an internal SYSTEM principal (it is not a
	// tenant request): Actor.IsAdmin/IsSystem make the semantic backend's
	// filter skip the workspace must-clause, exactly like the admin HTTP
	// surface does (mediasearch handler extractActor). With a zero Actor
	// the semantic backend fails closed on the missing workspace and the
	// whole search dies ("all eligible backends failed"). Universe is
	// pinned to catalog: the workflow needs REGISTRY clips to extract,
	// never live provider (discovery) traffic.
	result, err := a.agg.Search(ctx, search.Query{
		Text:       req.Topic,
		Sources:    req.Sources,
		MediaTypes: []string{"video"},
		Limit:      req.Limit,
		Mode:       search.SearchModeHybrid,
		Universe:   search.SearchCatalog,
		Actor:      search.Actor{IsAdmin: true, IsSystem: true},
	})
	if err != nil {
		return nil, fmt.Errorf("videocreate: media search: %w", err)
	}
	if result == nil {
		return nil, nil
	}
	out := make([]MediaCandidate, 0, len(result.Items))
	for _, item := range result.Items {
		// Registry (catalog) hits carry their identity in the canonical
		// YouTube asset id; recover the source video + exact window so the
		// acquisition child re-extracts THAT clip and not a re-derived one.
		videoID, startSec, endSec := "", 0, 0
		if item.Source == "youtube" {
			if vid, s, e, _, perr := assetdetail.ParseYouTubeClipAssetID(item.AssetID); perr == nil {
				videoID, startSec, endSec = vid, s, e
			}
		}
		sourceURL := item.SourceURL
		if sourceURL == "" && videoID != "" {
			sourceURL = "https://www.youtube.com/watch?v=" + videoID
		}
		out = append(out, MediaCandidate{
			AssetID:       item.AssetID,
			Source:        item.Source,
			SourceURL:     sourceURL,
			SourceVideoID: videoID,
			StartSec:      startSec,
			EndSec:        endSec,
			Title:         item.Title,
			MediaType:     item.MediaType,
			DurationMS:    item.DurationMs,
			Score:         item.Score,
		})
	}
	return out, nil
}

// MediaPlaneExecutor is the narrow execution seam of the canonical
// media plane (rustexec VideoProcessor): the ONLY sanctioned owner of
// media binaries (render_audio_plan / assemble_copy / mux_audio_copy /
// ffprobe). The interface keeps this capability free of platform
// imports; the copy certification is the media capability's own shared
// contract type (mediaexec.CopyCertification).
type MediaPlaneExecutor interface {
	RenderAudioPlan(ctx context.Context, plan audio.CompiledAudioPlan, assets audio.ResolvedAudioAssets, output string) (audio.FinalAudioAsset, error)
	MuxFinalAudioCopy(ctx context.Context, video, finalAudio, output string, asset audio.FinalAudioAsset) error
	AssembleCopy(ctx context.Context, inputs []string, output string, cert mediaexec.CopyCertification) error
	Probe(ctx context.Context, path string) (*mediaexec.MediaInfo, error)
}

// mediaPlane binds AudioMaster + MediaProber to one MediaPlaneExecutor.
type mediaPlane struct{ exec MediaPlaneExecutor }

// NewMediaPlane binds the canonical media plane to the media ports: the
// audio master + mux surface (AudioMaster), the probe surface
// (MediaProber) and the VeloxEditing copy-assembly boundary (Assembler).
func NewMediaPlane(exec MediaPlaneExecutor) (AudioMaster, MediaProber, Assembler) {
	p := mediaPlane{exec: exec}
	return p, p, assemblerViaMediaPlane{exec: exec}
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

// assemblerViaMediaPlane binds Assembler to the VeloxEditing media
// plane: assemble_copy / video.assemble.copy.v1 (pipelinegen-muscles) —
// packet-copy concatenation of the copy-certified scene segments with
// zero decode, zero encode, zero compositing. It is the LIVE assembly
// boundary of the durable video.create workflow (the user's explicit
// cutover decision, 2026-09-23): RenderingGen's ParentFinalizer → daemon
// ASSEMBLE_SEGMENTS keeps assembling the chunks of ONE render job, this
// boundary assembles the independent scene segments of a final video.
// Both enforce the same copy-safety facts, so the two lanes cannot
// disagree about what is assemblable.
type assemblerViaMediaPlane struct{ exec MediaPlaneExecutor }

// NewAssemblerViaMediaPlane binds the VeloxEditing copy-assembly boundary.
func NewAssemblerViaMediaPlane(exec MediaPlaneExecutor) Assembler {
	return assemblerViaMediaPlane{exec: exec}
}

func (a assemblerViaMediaPlane) Assemble(ctx context.Context, req AssembleRequest) (AssembleResult, error) {
	if a.exec == nil {
		return AssembleResult{}, fmt.Errorf("videocreate: media plane is not wired")
	}
	if len(req.Segments) == 0 {
		return AssembleResult{}, fmt.Errorf("videocreate: assemble: no segments")
	}
	if strings.TrimSpace(req.OutputPath) == "" {
		return AssembleResult{}, fmt.Errorf("videocreate: assemble: output path is required")
	}
	cert, err := CopyCertificationFor(req.Segments)
	if err != nil {
		return AssembleResult{}, err
	}
	inputs := make([]string, 0, len(req.Segments))
	var totalMS int64
	for i, seg := range req.Segments {
		if !seg.CopyCertified {
			return AssembleResult{}, fmt.Errorf("videocreate: assemble: segment %d (%s) is not copy-certified (the assembler is copy-only)", i, seg.AssetID)
		}
		if strings.TrimSpace(seg.LocalPath) == "" {
			return AssembleResult{}, fmt.Errorf("videocreate: assemble: segment %d (%s) has no local materialization", i, seg.AssetID)
		}
		inputs = append(inputs, seg.LocalPath)
		totalMS += seg.DurationMS
	}
	if err := a.exec.AssembleCopy(ctx, inputs, req.OutputPath, cert); err != nil {
		return AssembleResult{}, fmt.Errorf("videocreate: assemble_copy: %w", err)
	}
	var size int64
	if stat, statErr := os.Stat(req.OutputPath); statErr == nil {
		size = stat.Size()
	}
	return AssembleResult{
		ArtifactID: req.AssemblyID + ":assembled_video",
		Path:       req.OutputPath,
		SizeBytes:  size,
		DurationMS: totalMS,
	}, nil
}

// CopyCertificationFor derives the shared copy-safety certification for
// one assembly batch from the segments' reported output-contract facts.
// Fail-closed on any disagreement between segments (the assembler is
// copy-only): every segment must carry the assembly-ready contract id
// and the IDENTICAL contract block, or the batch is refused before a
// Rust process starts.
//
// closed_gop / first_frame_keyframe are the assembly-ready output
// contract's own guarantees (the clip.render lane resolves
// VELOX_ASSEMBLY_READY_V1 with closed GOP + first-frame keyframe); the
// contract id on every segment IS that claim, and the Rust gate
// re-probes each input against these facts.
func CopyCertificationFor(segments []AssembleSegment) (mediaexec.CopyCertification, error) {
	if len(segments) == 0 {
		return mediaexec.CopyCertification{}, fmt.Errorf("videocreate: assemble: no segments")
	}
	first := segments[0]
	if first.ContractID != kernelmedia.AssemblyMediaContractID {
		return mediaexec.CopyCertification{}, fmt.Errorf("videocreate: assemble: segment contract %q is not the assembly-ready contract %s", first.ContractID, kernelmedia.AssemblyMediaContractID)
	}
	for i, seg := range segments {
		if seg.ContractID != first.ContractID {
			return mediaexec.CopyCertification{}, fmt.Errorf("videocreate: assemble: segment %d contract %q != %q (ASSEMBLY_INPUT_CONTRACT_MISMATCH)", i, seg.ContractID, first.ContractID)
		}
		if seg.Contract != first.Contract {
			return mediaexec.CopyCertification{}, fmt.Errorf("videocreate: assemble: segment %d contract block %+v != %+v (ASSEMBLY_INPUT_CONTRACT_MISMATCH)", i, seg.Contract, first.Contract)
		}
	}
	cert := mediaexec.CopyCertification{
		CopyEligible:       true,
		ProfileID:          first.ContractID,
		Codec:              first.Contract.VideoCodec,
		Width:              uint32(first.Contract.Width),
		Height:             uint32(first.Contract.Height),
		FPSNum:             uint32(first.Contract.FPSNum),
		FPSDen:             uint32(first.Contract.FPSDen),
		ClosedGOP:          true,
		FirstFrameKeyframe: true,
		ContractID:         first.ContractID,
	}
	if err := cert.Validate(); err != nil {
		return mediaexec.CopyCertification{}, fmt.Errorf("videocreate: assemble: %w", err)
	}
	return cert, nil
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
