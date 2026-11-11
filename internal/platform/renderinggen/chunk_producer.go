package renderinggen

// chunk_producer.go owns the submit half of I1. The deterministic partition
// lives in cliprender; this adapter lowers one sealed plan into an atomic
// RenderingGen anchor+children family. It is opt-in until Chronon's parallel
// admission policy is certified on the target GPU.
//
// AUDIO-AWARE ASSEMBLY. Children carry the VIDEO-ONLY mapping of the plan
// (silent, copy-eligible segments that the packet-copy assembly can
// concatenate at keyframe-aligned boundaries); the anchor carries the FULL
// mapping, so the worker-side finalizer muxes the source audio exactly once
// over the assembled video. Splitting audio across children would cut AAC
// packets at video-GOP boundaries; keeping audio single-owner at the anchor
// removes that problem by construction (see
// docs/operations/TICKET-CHUNK-AUDIO-AWARE-ASSEMBLY.md).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// ChunkProducer creates a queue family in one transaction. The anchor is
// assembly-only: it owns the children and is never claimable by a render
// worker; the worker-side finalizer claims it after all chunks certify.
type ChunkProducer struct {
	submitter scriptgen.RenderQueueBatchSubmitter
}

func NewChunkProducer(submitter scriptgen.RenderQueueBatchSubmitter) (*ChunkProducer, error) {
	if submitter == nil {
		return nil, fmt.Errorf("chunk producer: atomic batch submitter is required")
	}
	return &ChunkProducer{submitter: submitter}, nil
}

// Submit partitions and submits a plan. alignmentFrames must be the certified
// output GOP/keyframe interval; the planner rejects invalid alignment rather
// than creating assembly-unsafe boundaries.
func (p *ChunkProducer) Submit(ctx context.Context, plan cliprender.ClipRenderPlanV1, requestedChunks int, alignmentFrames int64) (cliprender.ChunkSet, error) {
	if p == nil || p.submitter == nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: not configured")
	}
	if err := plan.Validate(); err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: validate plan: %w", err)
	}
	set, err := cliprender.BuildChunkSet(plan, requestedChunks, alignmentFrames)
	if err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: plan chunks: %w", err)
	}
	// rawPlan (WITH audio) is the anchor's document — the finalizer reads its
	// audio block + source asset to mux the family's single audio owner.
	rawPlan, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: map plan: %w", err)
	}
	// childPlan is the same plan WITHOUT the audio block: every child renders
	// a silent segment, so assembly never has to reason about audio packet
	// boundaries.
	childPlan, err := MapClipPlanToOverlayPlanVideoOnly(plan)
	if err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: map video-only child plan: %w", err)
	}
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: asset refs: %w", err)
	}
	if err := prefetchClipAssets(ctx, plan, refs); err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: prefetch assets: %w", err)
	}
	assets := make([]scriptgen.RenderQueueAsset, len(refs))
	for i, ref := range refs {
		assets[i] = scriptgen.NewRenderQueueAsset(
			kernelasset.Ref{SHA256: ref.Hash, AssetID: ref.LogicalPath},
			ref.LogicalPath, "")
	}
	anchorID := set.AnchorJobID()
	queue, ok := p.submitter.(scriptgen.RenderQueueClient)
	if !ok {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: shared prepared packages require a queue submit/get client")
	}
	preparedRef, err := p.prepareFamilyPackage(ctx, queue, anchorID, childPlan, assets, set.TotalFrames)
	if err != nil {
		return cliprender.ChunkSet{}, err
	}
	family := make([]scriptgen.RenderQueueJob, 0, len(set.Chunks)+1)
	family = append(family, scriptgen.RenderQueueJob{
		ID: anchorID, JobType: queueclient.JobTypeRenderSegment,
		OverlaySpec: rawPlan, Assets: assets,
	})
	for _, chunk := range set.Chunks {
		family = append(family, scriptgen.RenderQueueJob{
			ID: chunk.JobID, JobType: queueclient.JobTypeRenderSegment,
			ParentJobID: anchorID, ChunkIndex: chunk.Index,
			FrameRange:  &scriptgen.RenderFrameRange{Start: chunk.StartFrame, End: chunk.EndFrame},
			OverlaySpec: childPlan, Assets: assets, PreparedRenderPackage: preparedRef,
		})
	}
	if err := p.submitter.SubmitBatch(ctx, family); err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("chunk producer: submit family: %w", err)
	}
	return set, nil
}

func (p *ChunkProducer) prepareFamilyPackage(ctx context.Context, queue scriptgen.RenderQueueClient, anchorID string, plan json.RawMessage, assets []scriptgen.RenderQueueAsset, totalFrames int64) (*scriptgen.RenderQueuePreparedPackageRef, error) {
	prepareID := anchorID + ".prepare"
	prepareJob := scriptgen.RenderQueueJob{
		ID: prepareID, JobType: queueclient.JobTypeRenderPrepare,
		OverlaySpec: plan, Assets: assets,
	}
	if err := queue.Submit(ctx, prepareJob); err != nil {
		return nil, fmt.Errorf("chunk producer: submit family preparation: %w", err)
	}
	completed, _, err := scriptgen.WaitRenderQueueTerminal(ctx, queue, prepareID, 500*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("chunk producer: wait for family preparation: %w", err)
	}
	artifact := completed.Artifact
	if completed.State != "completed" || artifact == nil || artifact.Kind != "prepared_render_package" {
		return nil, fmt.Errorf("chunk producer: preparation job %s completed without a prepared render package", prepareID)
	}
	packageID := strings.TrimSpace(artifact.StorageKey)
	artifactSHA := strings.TrimSpace(artifact.SHA256)
	if packageID == "" {
		packageID = artifactSHA
	} else if artifactSHA != "" && packageID != artifactSHA {
		return nil, fmt.Errorf("chunk producer: preparation job %s returned conflicting package storage key and sha256", prepareID)
	}
	if !digest.IsCanonicalSHA256(packageID) || strings.TrimSpace(artifact.ChrononVersion) == "" || strings.TrimSpace(artifact.ProfileID) == "" {
		return nil, fmt.Errorf("chunk producer: preparation job %s returned incomplete package identity", prepareID)
	}
	identity, err := scriptgen.RenderRequestIdentityFor(prepareJob)
	if err != nil {
		return nil, fmt.Errorf("chunk producer: canonicalize family package request: %w", err)
	}
	return &scriptgen.RenderQueuePreparedPackageRef{
		PackageID: packageID, PlanSHA256: identity.PlanSHA256,
		AssetManifestSHA: renderAssetManifestSHA(assets), TotalFrames: totalFrames,
		CompilerVersion: artifact.ChrononVersion, ProfileID: artifact.ProfileID,
	}, nil
}

type renderAssetManifestEntry struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
}

func renderAssetManifestSHA(assets []scriptgen.RenderQueueAsset) string {
	entries := make([]renderAssetManifestEntry, 0, len(assets))
	for _, asset := range assets {
		entries = append(entries, renderAssetManifestEntry{
			Hash: strings.ToLower(strings.TrimSpace(asset.SHA256)), LogicalPath: strings.TrimSpace(asset.URL),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Hash != entries[j].Hash {
			return entries[i].Hash < entries[j].Hash
		}
		return entries[i].LogicalPath < entries[j].LogicalPath
	})
	encoded, _ := json.Marshal(entries)
	return sha256Hex(encoded)
}

func sha256Hex(data []byte) string {
	return digest.SHA256Bytes(data)
}
