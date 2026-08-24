package autotag

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/enrichment"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/application/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/vlm"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Percentages used for the canonical multi-frame video analysis:
// start, 25%, 50%, 75%, end.
var videoFramePercentages = []float64{0.0, 0.25, 0.50, 0.75, 1.0}

type Service struct {
	db          *sql.DB
	repo        asset.Repository
	vlmClient   *vlm.Client
	committer   persistence.AssetCommitter
	enrichState enrichment.EnrichStateMachinePort
	log         *zap.Logger

	// Optional video analysis ports. When all four are wired, video
	// assets are analysed with a multi-frame sampler + per-frame VLM +
	// keyframe embedding + keyframe indexing. When any is nil, video
	// falls back to the same single-shot path as images.
	videoSampler  indexing.PercentageFrameSampler
	visualVLM     indexing.VLMClient
	imageEmbedder qdrantsearch.ImageEmbedder
	frameIndexer  mediamemory.KeyframeVisualIndexer
}

// ServiceDeps groups the persistence, inference and video-analysis ports
// required by the auto-tagging workflow.
type ServiceDeps struct {
	DB            *sql.DB
	Repo          asset.Repository
	VLMClient     *vlm.Client
	Committer     persistence.AssetCommitter
	EnrichState   enrichment.EnrichStateMachinePort
	Log           *zap.Logger
	VideoAnalysis VideoAnalysisDeps
}

type VideoAnalysisDeps struct {
	Sampler       indexing.PercentageFrameSampler
	VLM           indexing.VLMClient
	ImageEmbedder qdrantsearch.ImageEmbedder
	FrameIndexer  mediamemory.KeyframeVisualIndexer
}

// NewService constructs an autotag.Service. The dispatcher is the
// canonical AssetMutationDispatcher (atomic media_assets UPSERT +
// asset.index.requested outbox event) used to persist VLM metadata
// changes and trigger Qdrant re-indexing. enrichState is the
// canonical state-machine port for media_assets.enrich_state
// transitions (PR-ENRICHMENT-STATE-MACHINE).
//
// The optional video-analysis ports (videoSampler, visualVLM,
// imageEmbedder, frameIndexer) enable the multi-frame VLM pipeline
// for video assets. Pass nil to all four to keep the legacy
// single-shot behaviour.
func NewService(deps ServiceDeps) *Service {
	return &Service{
		db:            deps.DB,
		repo:          deps.Repo,
		vlmClient:     deps.VLMClient,
		committer:     deps.Committer,
		enrichState:   deps.EnrichState,
		log:           deps.Log,
		videoSampler:  deps.VideoAnalysis.Sampler,
		visualVLM:     deps.VideoAnalysis.VLM,
		imageEmbedder: deps.VideoAnalysis.ImageEmbedder,
		frameIndexer:  deps.VideoAnalysis.FrameIndexer,
	}
}

// TagAsset analyzes a single asset with VLM and persists the updated
// metadata through the canonical AssetMutationDispatcher. The
// dispatcher performs an atomic SQLite UPSERT + emits an
// asset.index.requested outbox event; the outbox worker then handles
// Qdrant re-indexing, replacing the previous goroutine-based direct
// Qdrant call.
//
// For video assets, when the multi-frame analysis ports are wired,
// TagAsset extracts frames at 0%, 25%, 50%, 75% and 100% of the
// duration, runs VLM on each frame, aggregates the metadata, embeds
// the frames with SigLIP, and indexes each keyframe separately in
// pipelinegen_media_frames.
func (s *Service) TagAsset(ctx context.Context, a *asset.Asset) error {
	if a == nil {
		return fmt.Errorf("autotag: asset is nil")
	}
	s.log.Info("auto-tagging asset", zap.String("id", a.ID), zap.String("path", a.LocalPath()))

	if isVideoAsset(a) && s.videoSampler != nil && s.visualVLM != nil {
		return s.tagVideoMultiFrame(ctx, a)
	}

	return s.tagAssetSingle(ctx, a)
}

// isVideoAsset reports whether the asset media type should be treated
// as a video for the multi-frame VLM pipeline.
func isVideoAsset(a *asset.Asset) bool {
	if a == nil {
		return false
	}
	mt := strings.ToLower(string(a.MediaType))
	switch mt {
	case "video", "clip", "image_video":
		return true
	default:
		return strings.HasPrefix(mt, "video/")
	}
}

// tagVideoMultiFrame runs the canonical 5-point video analysis:
// extract start/25%/50%/75%/end frames, VLM each, aggregate
// metadata, embed frames with SigLIP, and index each keyframe.
func (s *Service) tagVideoMultiFrame(ctx context.Context, a *asset.Asset) error {
	localPath := a.LocalPath()
	if localPath == "" {
		return fmt.Errorf("autotag: asset %s has no local_path", a.ID)
	}

	jobDir, err := os.MkdirTemp("", "vlm-video-")
	if err != nil {
		return fmt.Errorf("autotag: mkdir temp: %w", err)
	}
	defer os.RemoveAll(jobDir)

	start := time.Now()

	frames, err := s.videoSampler.ExtractPercentageFrames(ctx, localPath, videoFramePercentages, jobDir)
	if err != nil {
		a.SetVLMTagError(err.Error())
		a.SetVLMTagged("failed")
		if derr := s.persistVLM(ctx, a); derr != nil {
			return fmt.Errorf("vlm video sampler failed and dispatcher persistence failed: vlm=%w; dispatcher=%v", err, derr)
		}
		return fmt.Errorf("vlm video sampler: %w", err)
	}

	responses := make([]*indexing.VLMInferenceResponse, 0, len(frames))
	for i, f := range frames {
		resp, err := s.visualVLM.Infer(ctx, f.Path)
		if err != nil {
			a.SetVLMTagError(err.Error())
			a.SetVLMTagged("failed")
			if derr := s.persistVLM(ctx, a); derr != nil {
				return fmt.Errorf("vlm video inference failed (frame %d) and dispatcher persistence failed: vlm=%w; dispatcher=%v", i, err, derr)
			}
			return fmt.Errorf("vlm video inference (frame %d): %w", i, err)
		}
		responses = append(responses, resp)
	}

	duration := time.Since(start)

	// Aggregate canonical visible actions/entities from all frames.
	text, actions, _ := indexing.AggregateVLMResponses(responses)

	// Aggregate the rest of the per-frame metadata for storage.
	sceneTypeSet := make(map[string]struct{})
	moodSet := make(map[string]struct{})
	visualObjectSet := make(map[string]struct{})
	ocrSet := make(map[string]struct{})
	var dominantScene string
	for _, r := range responses {
		if r == nil {
			continue
		}
		if strings.TrimSpace(r.SceneType) != "" {
			sceneTypeSet[r.SceneType] = struct{}{}
			if dominantScene == "" {
				dominantScene = r.SceneType
			}
		}
		for _, m := range r.Mood {
			if strings.TrimSpace(m) != "" {
				moodSet[m] = struct{}{}
			}
		}
		for _, o := range r.VisualObjects {
			if strings.TrimSpace(o) != "" {
				visualObjectSet[o] = struct{}{}
			}
		}
		for _, t := range r.TextOnScreen {
			if strings.TrimSpace(t) != "" {
				ocrSet[t] = struct{}{}
			}
		}
	}

	// Build VLMTags from the aggregated per-frame metadata plus the
	// canonical actions returned by the shared aggregator.
	vlmTagSet := make(map[string]bool)
	for _, a := range actions {
		vlmTagSet[strings.ToLower(a)] = true
	}
	for s := range sceneTypeSet {
		vlmTagSet[strings.ToLower(s)] = true
	}
	for m := range moodSet {
		vlmTagSet[strings.ToLower(m)] = true
	}
	for o := range visualObjectSet {
		vlmTagSet[strings.ToLower(o)] = true
	}
	vlmTags := make([]string, 0, len(vlmTagSet))
	for t := range vlmTagSet {
		vlmTags = append(vlmTags, t)
	}
	a.VLMTags = vlmTags
	a.RebuildTags()

	// Persist aggregated metadata.
	a.SetVLMTagged("success")
	a.SetVLMModel(s.vlmClient.Model())
	a.SetVLMModelVersion(s.vlmClient.ModelVersion())
	a.SetVLMAnalysisDurationMs(int(duration.Milliseconds()))
	a.SetVLMFramesAnalyzed(len(frames))
	a.SetSceneType(dominantScene)

	sceneTypes := sortedStringKeys(sceneTypeSet)
	moods := sortedStringKeys(moodSet)
	visualObjects := sortedStringKeys(visualObjectSet)
	ocrTexts := sortedStringKeys(ocrSet)

	if len(sceneTypes) > 0 {
		a.SetVLMSceneTypes(joinJSON(sceneTypes))
	}
	if len(moods) > 0 {
		a.SetVLMMoods(joinJSON(moods))
	}
	if len(visualObjects) > 0 {
		a.SetVLMVisualObjects(joinJSON(visualObjects))
	}
	if len(ocrTexts) > 0 {
		a.SetVLMOCRText(joinJSON(ocrTexts))
		a.SetTextOnScreen(joinJSON(ocrTexts))
	}
	if text != "" {
		a.SetVLMAggregateDescription(text)
	}

	// Embed frames and index each keyframe separately.
	if s.imageEmbedder != nil && s.frameIndexer != nil {
		if err := s.indexKeyframes(ctx, a, frames, responses); err != nil {
			// Keyframe indexing is best-effort: we still want the VLM
			// metadata to be persisted, but we surface the failure in
			// the logs so the operator can investigate.
			s.log.Warn("autotag: keyframe indexing failed", zap.String("asset_id", a.ID), zap.Error(err))
		}
	}

	if err := s.persistVLM(ctx, a); err != nil {
		return fmt.Errorf("dispatcher.EnqueueAndIndex after video VLM: %w", err)
	}

	return nil
}

// indexKeyframes generates SigLIP embeddings for the extracted frames
// and writes one point per frame into pipelinegen_media_frames.
func (s *Service) indexKeyframes(ctx context.Context, a *asset.Asset, frames []indexing.FrameSample, responses []*indexing.VLMInferenceResponse) error {
	paths := make([]string, 0, len(frames))
	for _, f := range frames {
		paths = append(paths, f.Path)
	}
	vectors, err := s.imageEmbedder.EmbedImages(ctx, paths)
	if err != nil {
		return fmt.Errorf("embed frames: %w", err)
	}
	if len(vectors) != len(frames) {
		return fmt.Errorf("embed frames: expected %d vectors, got %d", len(frames), len(vectors))
	}

	language := strings.ToLower(a.Language())
	if language == "" {
		language = "en"
	}

	for i, f := range frames {
		vec := vectors[i]
		if len(vec) == 0 {
			s.log.Warn("autotag: empty frame embedding", zap.String("asset_id", a.ID), zap.Int("frame_index", i))
			continue
		}
		tsMs := int64(f.Timestamp * 1000)
		if err := s.frameIndexer.IndexKeyframe(ctx, a.ID, tsMs, a.ID, language, vec, ""); err != nil {
			s.log.Warn("autotag: index keyframe failed",
				zap.String("asset_id", a.ID),
				zap.Int("frame_index", i),
				zap.Int64("ts_ms", tsMs),
				zap.Error(err))
		}
	}
	return nil
}

// sortedStringKeys returns the keys of a string set as a sorted slice.
func sortedStringKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// joinJSON marshals a string slice to JSON. On failure it falls back
// to joining with commas.
func joinJSON(v []string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return strings.Join(v, ", ")
	}
	return string(b)
}

// tagAssetSingle runs the legacy single-shot VLM analysis used for
// images, audio and for video when the multi-frame ports are not wired.
func (s *Service) tagAssetSingle(ctx context.Context, a *asset.Asset) error {
	// 1. Call VLM sidecar and measure analysis duration.
	start := time.Now()
	vTags, usedModel, err := s.vlmClient.AutoTagLocal(ctx, a.LocalPath(), string(a.MediaType))
	duration := time.Since(start)
	if err != nil {
		// Mark as skipped in metadata so we don't keep retrying if it's a permanent failure (e.g. file corrupt)
		a.SetVLMTagError(err.Error())
		a.SetVLMTagged("failed")
		if derr := s.persistVLM(ctx, a); derr != nil {
			return fmt.Errorf("vlm autotag failed and dispatcher persistence failed: vlm=%w; dispatcher=%v", err, derr)
		}
		return fmt.Errorf("vlm autotag: %w", err)
	}

	// Prefer the model reported by the sidecar; fall back to the configured model.
	if usedModel == "" {
		usedModel = s.vlmClient.Model()
	}
	modelVersion := s.vlmClient.ModelVersion()
	if modelVersion == "" {
		modelVersion = usedModel
	}

	// 2. Build VLM-specific tags and keep the aggregated Tags view consistent.
	vlmTagSet := make(map[string]bool)
	for _, o := range vTags.VisualObjects {
		vlmTagSet[strings.ToLower(o)] = true
	}
	for _, m := range vTags.Mood {
		vlmTagSet[strings.ToLower(m)] = true
	}
	if vTags.SceneType != "" {
		vlmTagSet[strings.ToLower(vTags.SceneType)] = true
	}
	if vTags.Lighting != "" {
		vlmTagSet[strings.ToLower(vTags.Lighting)] = true
	}

	vlmTags := make([]string, 0, len(vlmTagSet))
	for t := range vlmTagSet {
		vlmTags = append(vlmTags, t)
	}
	a.VLMTags = vlmTags
	a.RebuildTags()

	// 3. Update metadata with full structured VLM info
	a.SetVLMTagged("success")
	a.SetVLMModel(usedModel)
	a.SetVLMModelVersion(modelVersion)
	a.SetVLMAnalysisDurationMs(int(duration.Milliseconds()))
	a.SetSceneType(vTags.SceneType)
	a.SetLighting(vTags.Lighting)
	a.SetComposition(vTags.Composition)

	if len(vTags.DominantColors) > 0 {
		colors, _ := json.Marshal(vTags.DominantColors)
		a.SetDominantColors(string(colors))
	}
	if len(vTags.TextOnScreen) > 0 {
		text, _ := json.Marshal(vTags.TextOnScreen)
		a.SetTextOnScreen(string(text))
	}

	// 4. Persist atomically through the canonical mutation dispatcher.
	// The dispatcher UPSERTs the row and emits an asset.index.requested
	// outbox event in a single transaction; the outbox worker will drive
	// Qdrant re-indexing. This replaces the previous repo.Upsert +
	// goroutine vectorStore.UpsertFromClip pattern.
	if err := s.persistVLM(ctx, a); err != nil {
		return fmt.Errorf("dispatcher.EnqueueAndIndex after VLM: %w", err)
	}

	return nil
}

// persistVLM resolves a deterministic content hash for the asset and
// dispatches the canonical EnqueueAndIndex. If the asset has no
// file_hash in metadata, it computes SHA256 from the local file and
// stores it back into the asset so the dispatcher's supersede gate
// and the outbox event_key are stable.
func (s *Service) persistVLM(ctx context.Context, a *asset.Asset) error {
	hash, err := s.contentHashFor(a)
	if err != nil {
		return err
	}
	if _, err := s.committer.CommitAndIndex(ctx, persistence.CommitRequest{
		AssetID: a.ID, Source: string(a.Source), Name: a.Name, Filename: a.Filename,
		MediaType: string(a.MediaType), ContentHash: hash, LifecycleState: string(a.LifecycleState),
		IndexState: a.GetMetadataString("index_state"), EmitIndexEvent: true,
		Metadata: persistence.TypedMetadata{Tags: a.Tags, Extra: a.Metadata},
	}); err != nil {
		return fmt.Errorf("CommitAndIndex: %w", err)
	}
	return nil
}

// contentHashFor returns the asset's file_hash if present, otherwise
// computes SHA256 of the local file and caches it on the asset.
func (s *Service) contentHashFor(a *asset.Asset) (string, error) {
	if a == nil {
		return "", fmt.Errorf("asset is nil")
	}
	if h := a.LegacyFileMD5(); h != "" {
		return h, nil
	}
	path := a.LocalPath()
	if path == "" {
		return "", fmt.Errorf("asset %s has no legacy_file_md5 and no local_path", a.ID)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s for content hash: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	hash := hex.EncodeToString(h.Sum(nil))
	a.SetLegacyFileMD5(hash)
	return hash, nil
}
