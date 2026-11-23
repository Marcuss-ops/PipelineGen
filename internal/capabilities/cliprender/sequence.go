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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/clipmerge"
	"go.uber.org/zap"
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

// Fingerprint is the deterministic identity of an assembly request. The
// transport uses it as the queue correlation so an identical retry returns the
// same assembly job, while a different transition/edit addresses a different
// one (CR-02). It is computed over the normalized payload, so wire key order
// and omitted defaults cannot split one assembly.
func (r *SequenceRequest) Fingerprint() (string, error) {
	if r == nil {
		return "", fmt.Errorf("%w: sequence request is nil", ErrInvalidRequest)
	}
	cp := *r
	cp.Clips = append([]SequenceClip(nil), r.Clips...)
	for i := range cp.Clips {
		if r.Clips[i].TransitionIn != nil {
			transition := *r.Clips[i].TransitionIn
			if r.Clips[i].TransitionIn.Leak != nil {
				leak := *r.Clips[i].TransitionIn.Leak
				transition.Leak = &leak
			}
			cp.Clips[i].TransitionIn = &transition
		}
		if r.Clips[i].BatchPosition != nil {
			position := *r.Clips[i].BatchPosition
			cp.Clips[i].BatchPosition = &position
		}
	}
	if r.Recovery != nil {
		recovery := *r.Recovery
		recovery.Items = append([]BatchRecoveryItem(nil), r.Recovery.Items...)
		cp.Recovery = &recovery
	}
	cp.Normalize()
	if err := cp.Validate(); err != nil {
		return "", fmt.Errorf("clip.render sequence fingerprint: %w", err)
	}
	// The per-item request fingerprints, not the full requests, are the
	// canonical batch identity: the same items must address the same sequence
	// job while the recovery copy stays a transport detail.
	var batchItems []string
	if cp.Recovery != nil {
		batchItems = make([]string, len(cp.Recovery.Items))
		for i := range cp.Recovery.Items {
			itemFP, err := cp.Recovery.Items[i].Request.Fingerprint()
			if err != nil {
				return "", fmt.Errorf("clip.render sequence fingerprint: batch item %d: %w", cp.Recovery.Items[i].Position, err)
			}
			batchItems[i] = itemFP
		}
	}
	// MaxWaitMS is a WAIT policy, not assembly identity: hashing it would make
	// the same clips with a different timeout address two queue entries and
	// assemble the same output twice (the same rule CR-01 applies to render
	// execution). RenderPhase is constant for this payload and is omitted too.
	type identity struct {
		Clips      []SequenceClip `json:"clips"`
		Output     SequenceOutput `json:"output"`
		BatchID    string         `json:"batch_id,omitempty"`
		BatchItems []string       `json:"batch_items,omitempty"`
	}
	payload, err := json.Marshal(identity{Clips: cp.Clips, Output: cp.Output, BatchID: batchIDOf(cp.Recovery), BatchItems: batchItems})
	if err != nil {
		return "", fmt.Errorf("clip.render sequence fingerprint: marshal: %w", err)
	}
	return digest.SHA256Bytes(payload), nil
}

// Normalize fills the defaults a caller may omit.
func (r *SequenceRequest) Normalize() {
	if r == nil {
		return
	}
	if r.RenderPhase == "" {
		r.RenderPhase = RenderPhaseSequence
	}
	r.Output.Dir = strings.TrimSpace(r.Output.Dir)
	r.Output.Codec = strings.TrimSpace(r.Output.Codec)
	r.Output.Preset = strings.TrimSpace(r.Output.Preset)
	for i := range r.Clips {
		r.Clips[i].Path = strings.TrimSpace(r.Clips[i].Path)
		r.Clips[i].AssetID = strings.TrimSpace(r.Clips[i].AssetID)
		r.Clips[i].JobID = strings.TrimSpace(r.Clips[i].JobID)
	}
	if r.Recovery != nil {
		r.Recovery.BatchID = strings.TrimSpace(r.Recovery.BatchID)
		for i := range r.Recovery.Items {
			r.Recovery.Items[i].Request.Normalize()
		}
	}
}

// ValidateEdgeSubmission is the HTTP gate for a sequence body. It rejects the
// two inputs that name the SERVER's filesystem: a per-clip `path` and
// `output.dir`.
//
// They are meaningful for a job produced locally (and remain accepted there),
// but over HTTP they are an arbitrary-file read plus an arbitrary-directory
// write under the service account — the sequence worker reads `path` verbatim
// and writes the merge into `output.dir`. Remote callers must name an
// `asset_id` or a `job_id`; the server owns where artifacts land.
func (r *SequenceRequest) ValidateEdgeSubmission() error {
	if r == nil {
		return fmt.Errorf("%w: sequence request is nil", ErrInvalidRequest)
	}
	for i, clip := range r.Clips {
		if strings.TrimSpace(clip.Path) != "" {
			return fmt.Errorf("%w: clips[%d].path is not accepted over HTTP; name an asset_id or a job_id", ErrInvalidRequest, i)
		}
	}
	if strings.TrimSpace(r.Output.Dir) != "" {
		return fmt.Errorf("%w: output.dir is not accepted over HTTP; the server owns the output location", ErrInvalidRequest)
	}
	return nil
}

// Validate fails fast on everything that can be decided without the bytes:
// clip count, one source per position, the first clip owning no transition, and
// each transition's preset / leak / duration window.
func (r *SequenceRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: sequence request is nil", ErrInvalidRequest)
	}
	if r.RenderPhase != RenderPhaseSequence && r.RenderPhase != "" {
		return fmt.Errorf("%w: render_phase must be %q (got %q)", ErrInvalidRequest, RenderPhaseSequence, r.RenderPhase)
	}
	if len(r.Clips) == 0 {
		return fmt.Errorf("%w: clips is required and must contain at least one clip", ErrInvalidRequest)
	}
	if len(r.Clips) > MaxSequenceClips {
		return fmt.Errorf("%w: sequence too large: %d clips (max %d)", ErrInvalidRequest, len(r.Clips), MaxSequenceClips)
	}
	for i, clip := range r.Clips {
		sources := 0
		for _, value := range []string{clip.Path, clip.AssetID, clip.JobID} {
			if value != "" {
				sources++
			}
		}
		if clip.BatchPosition != nil {
			if r.Recovery == nil {
				return fmt.Errorf("%w: clips[%d].batch_position requires a batch recovery manifest", ErrInvalidRequest, i)
			}
			sources++
		}
		if sources != 1 {
			return fmt.Errorf("%w: clips[%d] needs exactly one of path, asset_id, job_id or batch_position (got %d)", ErrInvalidRequest, i, sources)
		}
		if clip.DurationMS < 0 {
			return fmt.Errorf("%w: clips[%d].duration_ms must not be negative", ErrInvalidRequest, i)
		}
		if i == 0 && clip.TransitionIn != nil {
			return fmt.Errorf("%w: clips[0] cannot declare transition_in: there is no previous clip to join", ErrInvalidRequest)
		}
		if clip.TransitionIn != nil {
			if err := clipsequence.ValidateTransition(clip.TransitionIn); err != nil {
				return fmt.Errorf("%w: clips[%d]: %v", ErrInvalidRequest, i, err)
			}
		}
	}
	if r.Recovery != nil {
		if !isSHA256Hex(r.Recovery.BatchID) {
			return fmt.Errorf("%w: recovery.batch_id=%q is not a SHA-256 hex digest", ErrInvalidRequest, r.Recovery.BatchID)
		}
		if len(r.Recovery.Items) == 0 {
			return fmt.Errorf("%w: recovery.items must not be empty", ErrInvalidRequest)
		}
		if len(r.Recovery.Items) > MaxSequenceClips {
			return fmt.Errorf("%w: recovery.items too large: %d (max %d)", ErrInvalidRequest, len(r.Recovery.Items), MaxSequenceClips)
		}
		byPosition := make(map[int]bool, len(r.Recovery.Items))
		for i, item := range r.Recovery.Items {
			if item.Position < 0 || item.Position >= len(r.Clips) {
				return fmt.Errorf("%w: recovery.items[%d].position %d is outside 0..%d", ErrInvalidRequest, i, item.Position, len(r.Clips)-1)
			}
			if byPosition[item.Position] {
				return fmt.Errorf("%w: recovery.items[%d].position %d is declared twice", ErrInvalidRequest, i, item.Position)
			}
			byPosition[item.Position] = true
			if err := item.Request.Validate(); err != nil {
				return fmt.Errorf("%w: recovery.items[%d].request: %v", ErrInvalidRequest, i, err)
			}
		}
		for position := range byPosition {
			if r.Clips[position].BatchPosition == nil {
				return fmt.Errorf("%w: recovery.items position %d has no clip with batch_position", ErrInvalidRequest, position)
			}
		}
		for _, clip := range r.Clips {
			if clip.BatchPosition == nil {
				continue
			}
			if *clip.BatchPosition < 0 || *clip.BatchPosition >= len(r.Clips) {
				return fmt.Errorf("%w: clip.batch_position %d is outside 0..%d", ErrInvalidRequest, *clip.BatchPosition, len(r.Clips)-1)
			}
			if !byPosition[*clip.BatchPosition] {
				return fmt.Errorf("%w: clip.batch_position %d has no recovery item", ErrInvalidRequest, *clip.BatchPosition)
			}
		}
	}
	if r.Output.CRF < 0 || r.Output.CRF > 51 {
		return fmt.Errorf("%w: output.crf must be in [0,51]", ErrInvalidRequest)
	}
	if r.MaxWaitMS < 0 {
		return fmt.Errorf("%w: max_wait_ms must not be negative", ErrInvalidRequest)
	}
	return nil
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

// batchIDOf returns the manifest batch id, or empty for a plain sequence.
func batchIDOf(recovery *SequenceBatchRecovery) string {
	if recovery == nil {
		return ""
	}
	return recovery.BatchID
}

// isSequencePayload reports whether a job payload belongs to the sequence
// phase. It reads only the phase key, so a sequence payload is never handed to
// the render decoder.
func isSequencePayload(raw json.RawMessage) bool {
	var envelope struct {
		RenderPhase RenderPhase `json:"render_phase"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false
	}
	return envelope.RenderPhase == RenderPhaseSequence
}

// resolvedSequenceClip is one clip with its local bytes and certified length.
type resolvedSequenceClip struct {
	assetID    string
	path       string
	durationMS int64
	transition *clipsequence.Transition
}

// handleSequence runs one assembly attempt.
//
// Waiting is expressed with job.DeferredAfter: a clip whose render has not
// settled hands the attempt back without spending retry budget, so a batch
// assembled in one request never burns its retries while the renders run.
func (w *Worker) handleSequence(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
	if w == nil || w.sequence == nil {
		return nil, fmt.Errorf("clip.render: sequence phase is not wired (WithSequence)")
	}
	assembly := w.sequence.in
	if assembly.Jobs == nil || assembly.Assets == nil {
		return nil, fmt.Errorf("clip.render: sequence phase requires a job lookup and an asset resolver")
	}

	var req SequenceRequest
	decoder := json.NewDecoder(bytes.NewReader(j.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return nil, fmt.Errorf("%w: sequence payload: %v", ErrInvalidJobPayload, err)
	}
	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, err
	}

	progress := safeProgress(tools)
	emit := safeEvent(tools)
	progress(5, "clip.sequence: resolving clips")

	if req.Recovery != nil {
		progress(8, "clip.sequence: recovering batch items")
		resolved, err := w.resolveBatchRecovery(ctx, req, assembly)
		if err != nil {
			return nil, err
		}
		req.Clips = resolved
	}

	clips, waiting, err := w.resolveSequenceClips(ctx, req, assembly)
	if err != nil {
		return nil, err
	}
	if waiting != "" {
		if sequenceWaitExceeded(j, req) {
			return nil, fmt.Errorf("%w: %s", ErrSequenceTimeout, waiting)
		}
		// A sibling render is still running: wait, do not fail.
		emit("clip.sequence.waiting", waiting, map[string]any{"job_id": j.ID})
		return nil, job.DeferredAfter(sequencePollDelay(assembly), waiting)
	}

	sequence := clipsequence.Sequence{Clips: make([]clipsequence.Clip, len(clips))}
	for i, clip := range clips {
		sequence.Clips[i] = clipsequence.Clip{
			AssetID:      clip.assetID,
			Path:         clip.path,
			DurationMS:   clip.durationMS,
			TransitionIn: clip.transition,
		}
	}
	plan, err := sequence.Plan()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}

	outputDir, err := assembly.OutputDir(req.Output.Dir, j.ID)
	if err != nil {
		return nil, err
	}
	progress(35, "clip.sequence: assembling")
	artifacts, err := clipmerge.Merge(ctx, sequence, plan, clipmerge.Options{
		OutputDir:      outputDir,
		Encoder:        assembly.encoder(req.Output),
		CanonicalVideo: assembly.CanonicalVideo,
		Logf: func(format string, args ...any) {
			w.log.Debug("clip.sequence: " + fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		return nil, fmt.Errorf("clip.render: sequence: %w", err)
	}
	progress(100, "clip.sequence: assembled")

	w.log.Info("clip.render.sequence.completed",
		zap.String("job_id", j.ID),
		zap.Int("clips", len(clips)),
		zap.Int("artifacts", len(artifacts)),
		zap.Int64("duration_ms", plan.DurationMS),
		zap.String("output_dir", outputDir),
	)

	entries := make([]map[string]any, 0, len(artifacts))
	paths := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		entries = append(entries, map[string]any{
			"group_id":     artifact.GroupID,
			"path":         artifact.Path,
			"duration_ms":  artifact.DurationMS,
			"merged":       artifact.Merged,
			"clip_indexes": artifact.ClipIndexes,
		})
		paths = append(paths, artifact.Path)
	}
	return job.Result{
		"job_id":          j.ID,
		"phase":           "assembled",
		"artifacts":       entries,
		"paths":           paths,
		"duration_ms":     plan.DurationMS,
		"output_dir":      outputDir,
		"groups":          len(plan.Groups),
		"local_only":      true,
		"delivery_status": "not_required",
	}, nil
}

// sequenceWaitExceeded reports whether the deferral chain has outlived its
// deadline. A missing durable creation time is not treated as an expired wait.
func sequenceWaitExceeded(j *job.Job, req SequenceRequest) bool {
	if j == nil || j.CreatedAt.IsZero() {
		return false
	}
	maxWait := DefaultSequenceMaxWait
	if req.MaxWaitMS > 0 {
		maxWait = time.Duration(req.MaxWaitMS) * time.Millisecond
	}
	return time.Since(j.CreatedAt) >= maxWait
}

func sequencePollDelay(assembly SequenceAssembly) time.Duration {
	if assembly.PollDelayMS <= 0 {
		return DefaultSequencePollDelay
	}
	return time.Duration(assembly.PollDelayMS) * time.Millisecond
}

// resolveBatchRecovery makes a batch sequence job self-healing. The sequence
// job is enqueued BEFORE the item renders, so its durable row is the proof that
// the batch was accepted. On every attempt it resolves each recovery position by
// the same deterministic correlation the HTTP handler uses and enqueues the item
// only when it is missing. Enqueue is idempotent on (type, correlation_id), so
// the handler racing this recovery cannot create a duplicate render.
//
// The request is an input, not a scratch buffer: it is taken by value, the
// clips are copied before any job id is stamped in, and the resolved slice is
// returned. Writing through `req.Clips[i]` in place would mutate the caller's
// backing array while it still holds the request it validated (percheck input
// immutability, BON-09).
func (w *Worker) resolveBatchRecovery(ctx context.Context, req SequenceRequest, assembly SequenceAssembly) ([]SequenceClip, error) {
	clips := append([]SequenceClip(nil), req.Clips...)
	if req.Recovery == nil {
		return clips, nil
	}
	if assembly.Batch.List == nil || assembly.Batch.Enqueue == nil {
		return nil, fmt.Errorf("clip.render: batch recovery requires job list/enqueue ports")
	}
	jobType := TypeClipRender
	jobs, err := assembly.Batch.List(ctx, job.Filter{
		Type:              &jobType,
		CorrelationPrefix: req.Recovery.BatchID + ":",
		Limit:             MaxClipRenderBatchItems*2 + 2,
	})
	if err != nil {
		return nil, fmt.Errorf("clip.render: list batch %s jobs: %w", req.Recovery.BatchID, err)
	}
	byCorrelation := make(map[string]string, len(jobs))
	for i := range jobs {
		byCorrelation[jobs[i].CorrelationID] = jobs[i].ID
	}
	for _, item := range req.Recovery.Items {
		position := item.Position
		if position < 0 || position >= len(clips) {
			return nil, fmt.Errorf("clip.render: batch recovery position %d is outside 0..%d", position, len(clips)-1)
		}
		request := item.Request
		request.Normalize()
		fingerprint, err := request.Fingerprint()
		if err != nil {
			return nil, fmt.Errorf("clip.render: batch recovery position %d fingerprint: %w", position, err)
		}
		correlation := req.Recovery.BatchID + ":" + digestPrefix(fingerprint)
		jobID := byCorrelation[correlation]
		if jobID == "" {
			enqueued, enqueueErr := assembly.Batch.Enqueue(ctx, &job.EnqueueRequest{
				Type:          TypeClipRender,
				CorrelationID: correlation,
				Payload:       &request,
				Scheduling:    &job.SchedulingDescriptor{JobType: TypeClipRender, ResourceClass: SchedulingResourceClass, ExecutionLane: ExecutionLaneSubmit},
			})
			if enqueueErr != nil {
				return nil, fmt.Errorf("clip.render: recover batch item %d: %w", position, enqueueErr)
			}
			if enqueued == nil || strings.TrimSpace(enqueued.ID) == "" {
				return nil, fmt.Errorf("clip.render: recover batch item %d: broker returned an empty job", position)
			}
			jobID = enqueued.ID
			byCorrelation[correlation] = jobID
			w.log.Info("clip.render.sequence.recovered_item",
				zap.Int("position", position),
				zap.String("job_id", jobID),
				zap.String("batch_id", req.Recovery.BatchID))
		}
		// Duplicate items share one canonical position: every clip that names
		// it must receive the same job id, not just the first occurrence.
		for i := range clips {
			if clips[i].BatchPosition != nil && *clips[i].BatchPosition == position {
				clips[i].JobID = jobID
				clips[i].BatchPosition = nil
			}
		}
	}
	for i := range clips {
		if clips[i].BatchPosition != nil {
			return nil, fmt.Errorf("clip.render: batch recovery left clip %d at position %d unresolved", i, *clips[i].BatchPosition)
		}
	}
	return clips, nil
}

// resolveSequenceClips turns each position into local bytes. It returns a
// non-empty `waiting` message when a sibling job has not settled yet.
func (w *Worker) resolveSequenceClips(ctx context.Context, req SequenceRequest, assembly SequenceAssembly) ([]resolvedSequenceClip, string, error) {
	// Pass 1: inspect every sibling BEFORE materializing anything. A later
	// running clip must not make this attempt download the earlier ready clips
	// again; the previous single pass materialized position 0..N-1 and only then
	// discovered position N was still rendering (CR-04).
	for i, clip := range req.Clips {
		if clip.JobID == "" {
			continue
		}
		child, err := assembly.Jobs.Get(ctx, clip.JobID)
		if err != nil {
			return nil, "", fmt.Errorf("clip.render: sequence clip %d: load job %s: %w", i, clip.JobID, err)
		}
		if child == nil {
			return nil, "", fmt.Errorf("clip.render: sequence clip %d: job %s does not exist", i, clip.JobID)
		}
		if !assembly.Jobs.IsTerminal(child.Status) {
			return nil, fmt.Sprintf("clip %d (job %s) is still %s", i, clip.JobID, child.Status), nil
		}
		if child.Status != job.StatusSucceeded {
			return nil, "", fmt.Errorf("clip.render: sequence clip %d: job %s ended %s: %s", i, clip.JobID, child.Status, child.Error)
		}
	}

	// Pass 2: every sibling is terminal, so materialize each position once.
	clips := make([]resolvedSequenceClip, len(req.Clips))
	for i, clip := range req.Clips {
		resolved := resolvedSequenceClip{assetID: clip.AssetID, path: clip.Path, durationMS: clip.DurationMS, transition: clip.TransitionIn}
		if clip.JobID != "" {
			child, err := assembly.Jobs.Get(ctx, clip.JobID)
			if err != nil {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: load job %s: %w", i, clip.JobID, err)
			}
			if child == nil {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: job %s does not exist", i, clip.JobID)
			}
			if !assembly.Jobs.IsTerminal(child.Status) {
				return nil, fmt.Sprintf("clip %d (job %s) is still %s", i, clip.JobID, child.Status), nil
			}
			if child.Status != job.StatusSucceeded {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: job %s ended %s: %s", i, clip.JobID, child.Status, child.Error)
			}
			assetID, durationMS, err := sequenceChildArtifact(child)
			if err != nil {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: %w", i, err)
			}
			resolved.assetID = assetID
			if resolved.durationMS == 0 {
				resolved.durationMS = durationMS
			}
		}
		if resolved.path == "" {
			if resolved.assetID == "" {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d has no resolvable artifact", i)
			}
			materialized, err := assembly.Assets.MaterializeAsset(ctx, resolved.assetID)
			if err != nil {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: materialize %s: %w", i, resolved.assetID, err)
			}
			if materialized == nil || materialized.LocalPath == "" {
				return nil, "", fmt.Errorf("clip.render: sequence clip %d: asset %s has no local bytes", i, resolved.assetID)
			}
			resolved.path = materialized.LocalPath
			if resolved.durationMS == 0 {
				resolved.durationMS = materialized.DurationMS
			}
			if resolved.assetID == "" {
				resolved.assetID = materialized.AssetID
			}
		}
		if resolved.durationMS <= 0 {
			return nil, "", fmt.Errorf("clip.render: sequence clip %d: duration_ms is required for a path (or pass an asset/job that certifies it)", i)
		}
		clips[i] = resolved
	}
	return clips, "", nil
}

// sequenceChildArtifact reads what a finished clip.render job produced: the
// published asset identity and its certified length.
func sequenceChildArtifact(child *job.Job) (string, int64, error) {
	var result struct {
		Asset *struct {
			AssetID string `json:"asset_id"`
		} `json:"asset"`
		Render *struct {
			DurationSec float64 `json:"duration_sec"`
		} `json:"render"`
	}
	if len(child.Result) > 0 {
		if err := json.Unmarshal(child.Result, &result); err != nil {
			return "", 0, fmt.Errorf("job %s result is not readable: %w", child.ID, err)
		}
	}
	if result.Asset == nil || result.Asset.AssetID == "" {
		return "", 0, fmt.Errorf("job %s reported no published asset", child.ID)
	}
	var durationMS int64
	if result.Render != nil && result.Render.DurationSec > 0 {
		durationMS = int64(result.Render.DurationSec*1000 + 0.5)
	}
	return result.Asset.AssetID, durationMS, nil
}

// OutputDir resolves the destination: the caller's directory when it names one,
// otherwise a per-job folder under the wired sequence root.
//
// A caller-named directory is CONFINED to the sequence root (ticket CR-19-04):
// the requested value arrives in a job payload, and an unconfined join would let
// that payload choose any writable directory on the host. A relative value is
// interpreted under the root, an absolute value must already be inside it, and
// anything that escapes (`..`) is a typed error rather than a silent overwrite.
func (a SequenceAssembly) OutputDir(requested, jobID string) (string, error) {
	root := a.Root
	if root == "" {
		root = "sequence"
	}
	if requested == "" {
		return filepath.Join(root, "job-"+jobID), nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve sequence root %q: %v", ErrInvalidRequest, root, err)
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absRoot, candidate)
	}
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(absRoot, candidate)
	if err != nil {
		return "", fmt.Errorf("%w: resolve sequence output.dir %q: %v", ErrInvalidRequest, requested, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: sequence output.dir %q escapes the assembly root %q", ErrInvalidRequest, requested, absRoot)
	}
	return candidate, nil
}

// encoder resolves the encode policy: caller overrides on top of the wired
// media policy, so a sequence is delivered with the same encoder family as the
// clips it joins unless the caller explicitly asks otherwise.
func (a SequenceAssembly) encoder(output SequenceOutput) media.EncoderPolicy {
	policy := a.Encoder
	if output.Codec != "" {
		policy.Codec = output.Codec
	}
	if output.Preset != "" {
		policy.Preset = output.Preset
	}
	if output.CRF > 0 {
		policy.CRF = output.CRF
	}
	return policy
}
