package cliprender

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media/clipsequence"
)

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
