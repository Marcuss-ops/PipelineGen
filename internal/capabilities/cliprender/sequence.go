package cliprender

// sequence.go owns the third clip.render boundary: joining already-produced
// clips into one deliverable.
//
// A sequence job renders NOTHING. Every clip is either an explicit local
// artifact, a certified asset, or the output of a clip.render job the sequence
// waits for; the job resolves those bytes and runs the kernel's merge decision
// (kernel/media/clipsequence) through the canonical assembler
// (platform/media/clipmerge). That is the same program the velox-clipseq CLI
// runs, so the look of a join is identical whether it is assembled from the
// shell or from a batch request — the CLI is a convenience, never the owner.
//
// The shape is what makes batch assembly possible: `job_id` lets the batch
// endpoint submit the N renders and the assembly in ONE request, and the
// sequence job waits on its siblings with a deferral (a WAIT that spends no
// retry budget) instead of polling or pinning a lane.

import (
	"context"
	"errors"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
)

// MaxSequenceClips caps one assembly. It matches MaxClipRenderBatchItems: a
// batch can be assembled in one job, and nothing bigger can be requested
// through the batch surface either.
const MaxSequenceClips = MaxClipRenderBatchItems

// DefaultSequencePollDelay is how long a sequence attempt waits before asking
// again about a clip that has not settled. The deferral is free (it consumes no
// retry), so this is a politeness interval, not a timeout.
const DefaultSequencePollDelay = 15 * time.Second

// DefaultSequenceMaxWait bounds how long a deferral chain may wait for sibling
// renders. A sequence without a deadline could be deferred forever because a
// deferral spends no retry budget (CR-04).
const DefaultSequenceMaxWait = 30 * time.Minute

// ErrSequenceTimeout is the typed failure for a sequence whose siblings did not
// settle inside MaxWaitMS/the default deadline.
var ErrSequenceTimeout = errors.New("clip.sequence: max wait exceeded")

// SequenceClip is one position of the deliverable. Exactly one source is set.
type SequenceClip struct {
	// Path is an explicit local artifact the caller already holds.
	Path string `json:"path,omitempty"`
	// AssetID is a certified asset (a cache-hit batch item, or any produced
	// clip). It is materialized through the canonical asset materializer.
	AssetID string `json:"asset_id,omitempty"`
	// JobID is a clip.render job that produces this position. The sequence
	// job waits for it, then resolves its published asset.
	JobID string `json:"job_id,omitempty"`
	// BatchPosition names a position of the durable batch manifest carried by
	// Recovery. It is used by the batch endpoint so the sequence job can be
	// enqueued BEFORE its sibling renders and recover any item whose enqueue
	// was lost with the process (CR-04). It is resolved to a durable JobID by
	// the worker before the normal wait/materialize path runs.
	BatchPosition *int `json:"batch_position,omitempty"`
	// DurationMS is the clip's own length. Required with path; with asset_id
	// and job_id the certified duration is used when this is zero.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// TransitionIn joins this clip to the previous one. Nil is a hard cut.
	TransitionIn *clipsequence.Transition `json:"transition_in,omitempty"`
}

// SequenceOutput is where and how the merged artifacts are encoded.
type SequenceOutput struct {
	// Dir is the output directory. Empty uses the worker's sequence workspace.
	Dir string `json:"dir,omitempty"`
	// Codec overrides the resolved encoder codec (e.g. libx264).
	Codec string `json:"codec,omitempty"`
	// Preset overrides the encoder preset.
	Preset string `json:"preset,omitempty"`
	// CRF overrides the encoder quality (0 keeps the resolved default).
	CRF int `json:"crf,omitempty"`
}

// BatchRecoveryItem is the durable copy of one batch item needed to recreate
// its render job if the process died after the batch was accepted but before
// every item was enqueued. Position is the canonical (first-occurrence)
// position in the batch.
type BatchRecoveryItem struct {
	Position int           `json:"position"`
	Request  RenderRequest `json:"request"`
}

// SequenceBatchRecovery is the batch manifest carried by a sequence job. The
// sequence job is enqueued BEFORE the item renders, so its existence is the
// durable proof that an accepted batch can still be completed: on every
// attempt the worker resolves each BatchPosition by its deterministic
// correlation and enqueues the item when it is missing.
type SequenceBatchRecovery struct {
	// BatchID is the canonical BatchFingerprint of the request.
	BatchID string `json:"batch_id"`
	// Items are the cold items, one per canonical position. Cache hits are not
	// recoverable work (their bytes are already certified), so they travel as
	// asset ids on the clips instead.
	Items []BatchRecoveryItem `json:"items"`
}

// SequenceRequest is the canonical payload of a sequence phase job and the body
// of POST /api/clips/render/sequence.
type SequenceRequest struct {
	// RenderPhase is always "sequence" on a job payload. Empty on the HTTP
	// body, where the route already says it.
	RenderPhase RenderPhase    `json:"render_phase,omitempty"`
	Clips       []SequenceClip `json:"clips"`
	Output      SequenceOutput `json:"output,omitempty"`
	// Recovery is set only by the batch endpoint: it makes this sequence job
	// the durable manifest that recovers missing item jobs (CR-04).
	Recovery *SequenceBatchRecovery `json:"recovery,omitempty"`
	// MaxWaitMS bounds the deferral chain. Zero uses DefaultSequenceMaxWait.
	MaxWaitMS int64 `json:"max_wait_ms,omitempty"`
}

// SequenceAssetResolver materializes a certified asset's bytes locally. The
// Preparer implements it: an assembly reads artifacts, it never renders.
type SequenceAssetResolver interface {
	MaterializeAsset(ctx context.Context, assetID string) (*MaterializedAsset, error)
}

// SequenceJobLookup is the minimum job surface a sequence needs to wait on its
// siblings and read what they produced.
type SequenceJobLookup interface {
	Get(ctx context.Context, id string) (*job.Job, error)
	IsTerminal(status job.Status) bool
}

// SequenceAssembly is the wiring a worker needs to assemble sequences: where
// siblings are read from, where their bytes come from, and how to encode.
type SequenceAssembly struct {
	Jobs   SequenceJobLookup
	Assets SequenceAssetResolver
	// Batch is the durable enqueue/list surface used by a sequence job that
	// carries a batch recovery manifest. It is optional for non-batch
	// assemblies; a recovery payload without it fails closed.
	Batch BatchRecoveryPorts
	// Encoder is the caller's resolved policy. CanonicalVideo overrides it with
	// the certified recipe when the assembled sequence is an assembly input.
	Encoder media.EncoderPolicy
	// CanonicalVideo pins the merged video stream to the certified H.264
	// recipe. The composition root sets it for the sequence lane; tests and the
	// velox-clipseq diagnostic keep the caller's policy unless they opt in.
	CanonicalVideo bool
	Root           string
	PollDelayMS    int64
}

// WithSequence attaches the sequence phase. It is optional: a composition root
// that does not call it keeps POST /render/sequence and the batch `sequence`
// block failing closed instead of silently assembling nothing.
func (w *Worker) WithSequence(in SequenceAssembly) *Worker {
	if w == nil {
		return w
	}
	if in.PollDelayMS <= 0 {
		in.PollDelayMS = int64(DefaultSequencePollDelay / time.Millisecond)
	}
	w.sequence = &sequenceAssembly{in: in}
	return w
}

type sequenceAssembly struct {
	in SequenceAssembly
}

// BatchRecoveryPorts is the narrow job surface a recovery manifest needs: list
// the durable jobs of one batch and enqueue a missing item idempotently. The
// composition root satisfies it with job.Service.List/Enqueue.
type BatchRecoveryPorts struct {
	List    func(context.Context, job.Filter) ([]job.Job, error)
	Enqueue func(context.Context, *job.EnqueueRequest) (*job.Job, error)
}
