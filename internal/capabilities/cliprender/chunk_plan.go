// chunk_plan.go owns the DETERMINISTIC partition of a sealed clip plan into
// render windows — the planning half of the chunk producer (I1).
//
// # WHY A PLAN AND NOT N JOBS
//
// One clip is one job, rendered start to finish by one worker on one device. The
// only lever that turns the timeline into parallel work is a partition that is
// (a) exact — every frame belongs to exactly one window, no gaps, no overlaps —
// and (b) reproducible, because the partition is what makes a chunk's identity,
// and an identity that moves between runs turns every retry into a duplicate
// render.
//
// # WHY THE WINDOWS ARE KEYFRAME-ALIGNED
//
// Chronon's segment assembly copies packets without decoding
// (assemble_segments), and RenderingGen's finalization gate refuses a family
// whose children are not copy-eligible, closed-GOP and keyframe-first. A window
// that starts mid-GOP renders fine on its own and then produces a stream that
// BREAKS at the concatenation boundary. So the partition places every window
// start on a multiple of the certified keyframe interval (the GOP the output
// contract pins, `ResolvedContract.KeyframeInterval` — 48 frames for the
// certified lane). The interval is an INPUT, not a constant here: the chunker
// must not be able to invent an alignment the encoder was not certified with.
//
// # WHAT THIS FILE DOES NOT DO
//
// It does not submit anything, and it does not decide how many windows are
// worth running: `RequestedChunks` is a request, and the plan reports the number
// of windows it could produce (a 10-frame clip cannot be split into five
// aligned windows, and pretending otherwise would mean unaligned boundaries).
// The producer that submits N child jobs plus the assembly-only anchor is the
// other half; nothing here is enabled by default.
package cliprender

import (
	"fmt"
	"strconv"
	"strings"
)

// ChunkContractVersion identifies the meaning of a chunk window. It is part of
// every chunk job id, so altering what a window means (for example switching
// from frame ranges to time ranges) mints new ids instead of silently
// re-pointing old ones at a different meaning.
const ChunkContractVersion = "clip-chunk.v1"

// Chunk is one deterministic window of a sealed plan's timeline. The range is
// HALF-OPEN [StartFrame, EndFrame) in plan frames, absolute (the plan carries no
// trim; a window selects, it does not rebase) — the same convention the queue's
// frame_range uses.
type Chunk struct {
	// Index is the window's position in the plan, 0-based. It is the
	// assembly order, and it is derived from the boundaries, never assigned by
	// the submitter.
	Index int
	// StartFrame is inclusive and keyframe-aligned for every index, including
	// 0 (which is trivially aligned).
	StartFrame int64
	// EndFrame is exclusive. The last window's EndFrame is exactly the plan's
	// frame count.
	EndFrame int64
	// JobID is the deterministic job identity of this window. It is derived
	// from the plan's content address and the window's boundaries, so a retry
	// addresses the SAME logical job (the queue answers 409 and the producer
	// rearms it) instead of minting a duplicate render.
	JobID string
}

// FrameCount is the number of frames the window renders.
func (c Chunk) FrameCount() int64 { return c.EndFrame - c.StartFrame }

// ChunkSet is the complete partition of one sealed plan.
type ChunkSet struct {
	// PlanSHA256 is the content address of the plan the windows belong to:
	// every window renders a sub-range of THIS plan, at this exact revision.
	PlanSHA256 string
	// PlanRunID is the clip lane's logical run id (the plan's RunID). It is
	// carried for the producer's bookkeeping and is deliberately NOT the job
	// identity of any window.
	PlanRunID string
	// TotalFrames is the plan's frame count; the windows cover it exactly.
	TotalFrames int64
	// AlignmentFrames is the keyframe interval the boundaries were placed on.
	AlignmentFrames int64
	// RequestedChunks is what the caller asked for; the actual number of
	// windows may be lower (and never higher) when the timeline cannot be cut
	// that finely while staying aligned.
	RequestedChunks int
	// Chunks are ordered by Index and cover [0, TotalFrames).
	Chunks []Chunk
	// ContractVersion is the chunk contract the windows were built under.
	ContractVersion string
}

// AnchorJobID is the identity of the assembly-only anchor job for a plan.
//
// The anchor is the job that owns the assembled artifact: it renders nothing,
// waits for the certified windows, validates the family (complete, disjoint,
// copy-eligible, keyframe-first) and assembles the final file. Its id is derived
// from the plan alone because there is exactly ONE assembled artifact per plan
// revision, however many windows produce it.
func (s ChunkSet) AnchorJobID() string {
	return anchorJobID(s.PlanSHA256)
}

// WindowCount is the number of windows this plan was partitioned into. It is the
// honest answer to "how much parallelism does this clip have", which is not
// always what was requested.
func (s ChunkSet) WindowCount() int { return len(s.Chunks) }

// MaximumParallelWindows is the number of windows that can legitimately run at
// the same time: one per window, capped by what the caller asked for. It exists
// so a caller does not have to re-derive the "requested vs produced" rule.
func (s ChunkSet) MaximumParallelWindows() int {
	if s.WindowCount() < s.RequestedChunks {
		return s.WindowCount()
	}
	return s.RequestedChunks
}

// Validate is the gate every consumer of a chunk set must pass. It answers one
// question: is this family a partition of the plan, in an order that can be
// assembled?
//
// Every failure is fail-closed with the reason, because the alternative to a
// rejected family is a concatenated artifact that is missing frames or has a
// duplicated window — an artifact that looks like a successful render.
func (s ChunkSet) Validate() error {
	if !isSHA256Hex(s.PlanSHA256) {
		return fmt.Errorf("%w: chunk plan_sha256 %q is not a canonical digest", ErrInvalidClipPlan, s.PlanSHA256)
	}
	if strings.TrimSpace(s.PlanRunID) == "" {
		return fmt.Errorf("%w: chunk plan run id is required", ErrInvalidClipPlan)
	}
	if s.ContractVersion != ChunkContractVersion {
		return fmt.Errorf("%w: chunk contract %q is not %q", ErrInvalidClipPlan, s.ContractVersion, ChunkContractVersion)
	}
	if s.TotalFrames <= 0 {
		return fmt.Errorf("%w: chunk plan total frames %d must be positive", ErrInvalidClipPlan, s.TotalFrames)
	}
	if s.AlignmentFrames <= 0 {
		return fmt.Errorf("%w: chunk alignment %d must be the certified keyframe interval", ErrInvalidClipPlan, s.AlignmentFrames)
	}
	if s.RequestedChunks <= 0 {
		return fmt.Errorf("%w: requested chunk count %d must be positive", ErrInvalidClipPlan, s.RequestedChunks)
	}
	if len(s.Chunks) == 0 {
		return fmt.Errorf("%w: chunk plan has no windows", ErrInvalidClipPlan)
	}
	if len(s.Chunks) > s.RequestedChunks {
		return fmt.Errorf("%w: chunk plan produced %d windows for a request of %d",
			ErrInvalidClipPlan, len(s.Chunks), s.RequestedChunks)
	}
	if s.Chunks[0].StartFrame != 0 {
		return fmt.Errorf("%w: the first chunk starts at frame %d, not 0", ErrInvalidClipPlan, s.Chunks[0].StartFrame)
	}
	for i, c := range s.Chunks {
		if c.Index != i {
			return fmt.Errorf("%w: chunk %d carries index %d", ErrInvalidClipPlan, i, c.Index)
		}
		if i > 0 && c.StartFrame != s.Chunks[i-1].EndFrame {
			return fmt.Errorf("%w: chunk %d starts at %d after chunk %d ended at %d (gap or overlap)",
				ErrInvalidClipPlan, i, c.StartFrame, i-1, s.Chunks[i-1].EndFrame)
		}
		if c.StartFrame%s.AlignmentFrames != 0 {
			return fmt.Errorf("%w: chunk %d starts at frame %d, off the keyframe alignment %d",
				ErrInvalidClipPlan, i, c.StartFrame, s.AlignmentFrames)
		}
		if c.FrameCount() <= 0 {
			return fmt.Errorf("%w: chunk %d covers %d frames", ErrInvalidClipPlan, i, c.FrameCount())
		}
		if c.JobID != chunkJobID(s.PlanSHA256, c.StartFrame, c.EndFrame) {
			return fmt.Errorf("%w: chunk %d job id %q is not the identity of [%d,%d)",
				ErrInvalidClipPlan, i, c.JobID, c.StartFrame, c.EndFrame)
		}
	}
	if last := s.Chunks[len(s.Chunks)-1]; last.EndFrame != s.TotalFrames {
		return fmt.Errorf("%w: the last chunk ends at frame %d, not at the plan's %d frames",
			ErrInvalidClipPlan, last.EndFrame, s.TotalFrames)
	}
	return nil
}

// PlanFrameCount resolves the plan's frame count from its duration and frame
// rate. Fail-closed: a plan whose timeline cannot be expressed in whole frames
// is not chunkable (the alternative is a window boundary that lands between
// frames).
func PlanFrameCount(plan ClipRenderPlanV1) (int64, error) {
	if plan.DurationMS <= 0 {
		return 0, fmt.Errorf("%w: plan duration %dms cannot be partitioned", ErrInvalidClipPlan, plan.DurationMS)
	}
	num, den := plan.Output.FPSNum, plan.Output.FPSDen
	if num <= 0 || den <= 0 {
		return 0, fmt.Errorf("%w: plan frame rate %d/%d cannot be partitioned", ErrInvalidClipPlan, num, den)
	}
	// Rounded to the nearest frame: 1000ms at 24000/1001 is 23.976 frames, and
	// the render's own frame count is the rounded one.
	frames := (plan.DurationMS*int64(num) + 500*int64(den)) / (1000 * int64(den))
	if frames <= 0 {
		return 0, fmt.Errorf("%w: plan duration %dms at %d/%d fps is %d frames",
			ErrInvalidClipPlan, plan.DurationMS, num, den, frames)
	}
	return frames, nil
}

// BuildChunkSet partitions a sealed plan into at most `requestedChunks`
// keyframe-aligned windows.
//
// `alignmentFrames` is the certified keyframe interval of the output contract
// (the GOP the encoder was certified to produce). It is required: a caller that
// does not know it cannot know where a window may start.
//
// The result is deterministic — same plan, same request, same alignment, same
// windows and same ids — and exact: the windows are ordered, disjoint, start on
// alignment boundaries, and cover [0, frames) with the last window ending at the
// plan's frame count.
func BuildChunkSet(plan ClipRenderPlanV1, requestedChunks int, alignmentFrames int64) (ChunkSet, error) {
	if err := plan.Validate(); err != nil {
		return ChunkSet{}, err
	}
	if requestedChunks <= 0 {
		return ChunkSet{}, fmt.Errorf("%w: requested chunk count %d must be positive", ErrInvalidClipPlan, requestedChunks)
	}
	if alignmentFrames <= 0 {
		return ChunkSet{}, fmt.Errorf("%w: chunk alignment must be the certified keyframe interval (got %d)",
			ErrInvalidClipPlan, alignmentFrames)
	}
	frames, err := PlanFrameCount(plan)
	if err != nil {
		return ChunkSet{}, err
	}

	// Windows that start on a keyframe: the last one may be shorter than the
	// alignment (the timeline's tail), which is fine — what must be aligned is
	// where a window STARTS, because that is the frame the assembly concatenates
	// at.
	units := (frames + alignmentFrames - 1) / alignmentFrames
	windows := int64(requestedChunks)
	if windows > units {
		windows = units
	}

	chunks := make([]Chunk, 0, windows)
	base := units / windows
	extra := units % windows
	var unit int64
	for i := int64(0); i < windows; i++ {
		span := base
		if i < extra {
			span++
		}
		start := unit * alignmentFrames
		unit += span
		end := unit * alignmentFrames
		if end > frames {
			end = frames
		}
		chunks = append(chunks, Chunk{
			Index:      int(i),
			StartFrame: start,
			EndFrame:   end,
			JobID:      chunkJobID(plan.PlanSHA256, start, end),
		})
	}

	set := ChunkSet{
		PlanSHA256:      strings.ToLower(plan.PlanSHA256),
		PlanRunID:       plan.RunID,
		TotalFrames:     frames,
		AlignmentFrames: alignmentFrames,
		RequestedChunks: requestedChunks,
		Chunks:          chunks,
		ContractVersion: ChunkContractVersion,
	}
	if err := set.Validate(); err != nil {
		return ChunkSet{}, err
	}
	return set, nil
}

// chunkJobID is the canonical identity of one window: plan content address +
// boundaries + chunk contract. It contains no randomness and no clock, which is
// the whole point — the same window of the same plan is the same job forever, so
// a retry cannot create a second render of the same frames.
func chunkJobID(planSHA256 string, startFrame, endFrame int64) string {
	return "chunk-" + digestPrefix(planSHA256) + "-" +
		strconv.FormatInt(startFrame, 10) + "-" +
		strconv.FormatInt(endFrame, 10) + "-" + ChunkContractVersion
}

// anchorJobID is the canonical identity of a plan's assembly anchor.
func anchorJobID(planSHA256 string) string {
	return "anchor-" + digestPrefix(planSHA256) + "-" + ChunkContractVersion
}

// digestPrefix is the readable short form of a content address used inside job
// ids. It is a PREFIX of the digest, never a re-hash: two different plans whose
// ids collide would have to share their first 16 hex characters, and the full
// digest still travels in the plan itself.
func digestPrefix(planSHA256 string) string {
	d := strings.ToLower(strings.TrimSpace(planSHA256))
	if len(d) > 16 {
		return d[:16]
	}
	return d
}
