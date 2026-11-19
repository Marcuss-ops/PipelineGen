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
	"fmt"
	"path/filepath"
	"strings"
	"time"

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

// SequenceRequest is the canonical payload of a sequence phase job and the body
// of POST /api/clips/render/sequence.
type SequenceRequest struct {
	// RenderPhase is always "sequence" on a job payload. Empty on the HTTP
	// body, where the route already says it.
	RenderPhase RenderPhase    `json:"render_phase,omitempty"`
	Clips       []SequenceClip `json:"clips"`
	Output      SequenceOutput `json:"output,omitempty"`
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
		if sources != 1 {
			return fmt.Errorf("%w: clips[%d] needs exactly one of path, asset_id or job_id (got %d)", ErrInvalidRequest, i, sources)
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
	if r.Output.CRF < 0 || r.Output.CRF > 51 {
		return fmt.Errorf("%w: output.crf must be in [0,51]", ErrInvalidRequest)
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
	Jobs        SequenceJobLookup
	Assets      SequenceAssetResolver
	Encoder     media.EncoderPolicy
	Root        string
	PollDelayMS int64
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

	clips, waiting, err := w.resolveSequenceClips(ctx, req, assembly)
	if err != nil {
		return nil, err
	}
	if waiting != "" {
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
		OutputDir: outputDir,
		Encoder:   assembly.encoder(req.Output),
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
		"job_id":      j.ID,
		"phase":       "assembled",
		"artifacts":   entries,
		"paths":       paths,
		"duration_ms": plan.DurationMS,
		"output_dir":  outputDir,
		"groups":      len(plan.Groups),
	}, nil
}

func sequencePollDelay(assembly SequenceAssembly) time.Duration {
	if assembly.PollDelayMS <= 0 {
		return DefaultSequencePollDelay
	}
	return time.Duration(assembly.PollDelayMS) * time.Millisecond
}

// resolveSequenceClips turns each position into local bytes. It returns a
// non-empty `waiting` message when a sibling job has not settled yet.
func (w *Worker) resolveSequenceClips(ctx context.Context, req SequenceRequest, assembly SequenceAssembly) ([]resolvedSequenceClip, string, error) {
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
