package cliprender

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
