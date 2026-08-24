// Package jobs — fanout.go (PR-VOICEOVER-PARENT-CHILD-FANOUT, P0.3, June 2026).
//
// Restored after origin/main drift: the cleanup commits
// `4cb13c86 fix(p1.6): uniform HTTP/application boundaries — channels
// + voiceover` and `75b2550a chore(p0.6): close Active Concerns #11`
// removed these symbols, but the composition root at
// internal/app/composition.go::NewComposition still references
// `voiceoverjobs.NewFanoutVoiceoversUseCase(voiceoverjobs.FanoutDeps{
// JobsService: jobs.Service, Logger: log })`. The drift-drop
// turned the canonical typed-port parent-child-fanout path into an
// undefined-symbol build failure.
//
// This file restores the typed-port parent-child-fanout use case so
// the build returns to green. The wire shape matches exactly what
// generate_handler.go::toFanoutResultMap reads (field names + JSON
// tags kept stable) and what the composition root constructs in the
// late-bindings block after BuildDomainBundle.
//
// Why a typed port (Pattern 0, June 2026): the use case wires its
// only external collaborator (the canonical jobs.Service broker)
// through a struct dep so the composition root can inject the
// concrete and the use case stays free of any `internal/jobs`
// carrier. Future BACKFILL stages (idempotency, audit) layer onto
// this struct without touching the dispatcher.
package jobs

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	jobvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// voiceover.FilenameSpec is referenced through the voiceover package
// import above (BuildVoiceoverFilename at filename.go).

// Enqueuer is the narrow port used by FanoutVoiceoversUseCase to
// enqueue per-language child jobs. Per AGENTS.md Pattern 0, the
// application layer depends on a typed interface rather than the
// concrete *appjobs.Service — the composition root satisfies the
// port implicitly by passing *appjobs.Service (whose Enqueue method
// matches the signature exactly).
//
// Test injectability: handlers under internal/application/voiceover/
// jobs/generate_handler_test.go can now construct a FanoutUseCase
// with a stub Enqueuer, without standing up a full
// appjobs.NewService(repo, dispatcher, logger) (the heavyweight
// production wiring needed for dispatcher + lease semantics).
type Enqueuer interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// FanoutDeps wires dependencies for FanoutVoiceoversUseCase per
// AGENTS.md Pattern 0: typed ports, concrete adapter injected at
// the composition root (composition.go::NewComposition — wires
// `Enqueuer: jobs.Service` because *appjobs.Service satisfies
// Enqueuer implicitly).
type FanoutDeps struct {
	// Enqueuer is the canonical narrow port used to enqueue child
	// voiceover.generate_item jobs (one per (language, voice) pair).
	// MANDATORY — fail-fast per AGENTS.md WireUp pattern. The
	// composition root satisfies this port with *appjobs.Service.
	Enqueuer Enqueuer

	// Logger is OPTIONAL (nil-safe via zap.NewNop() in the constructor).
	Logger *zap.Logger
}

// FanoutResult is the canonical parent-job result carrying
// broker-level fan-out telemetry. Field names mirror the JSON tags
// the per-job dispatcher writes into job.Result so the aggregator
// (commit 2 of PR-VOICEOVER-PARENT-CHILD-FANOUT) can unmarshal a
// parent job's result back into a typed FanoutResult without an
// intermediate struct.
type FanoutResult struct {
	OK                 bool                         `json:"ok"`
	ParentJobID        string                       `json:"parent_job_id"`
	RequestID          string                       `json:"request_id"`
	TotalOutputs       int                          `json:"total_outputs"`
	EnqueuedCount      int                          `json:"enqueued_count"`
	FailedEnqueueCount int                          `json:"failed_enqueue_count"`
	ChildJobIDs        []string                     `json:"child_job_ids"`
	PerLanguage        []string                     `json:"per_language"`
	StageProgress      map[string]job.StageProgress `json:"stage_progress,omitempty"`
}

// Compile-time assertion: *appjobs.Service satisfies Enqueuer.
// Compile-time assertion (Pattern 0 narrow-port conformance):
// *appjobs.Service (the canonical job broker wired into FanoutDeps.Enqueuer
// by composition.go) MUST satisfy the Enqueuer interface. If the
// canonical broker's Enqueue() signature drifts, the compiler fails
// this assertion and composition wiring breaks loudly. The import
// is unused at runtime — kept only to anchor the assertion.
var _ Enqueuer = (*appjobs.Service)(nil)

// FanoutVoiceoversUseCase is the typed-port P0.3 parent-child-fanout
// use case. Execute iterates cmd.Languages, builds one
// GenerateVoiceoverItemCommand per (language, voice) pair, and
// enqueues each child via the canonical jobs.Service broker.
// NO goroutines are spawned here (PR-VOICEOVER-PARENT-CHILD-FANOUT
// P0.3 invariant); per-job-type Concurrency on the worker pool
// regulates sibling dispatch through the dispatcher.
type FanoutVoiceoversUseCase struct {
	deps FanoutDeps
}

// NewFanoutVoiceoversUseCase constructs the use case. Enqueuer is
// MANDATORY (panic on nil — fail-fast per AGENTS.md WireUp pattern).
// Logger is OPTIONAL (nil-safe via zap.NewNop()).
//
// Why Enqueuer-not-JobsService (Pattern 0 narrow port, June 2026):
// the use case only needs the Enqueue method. Taking the full
// *appjobs.Service would force tests to construct the dispatcher +
// lease machinery — invasive. The narrow port keeps the use case
// dependency surface just-above-zero.
func NewFanoutVoiceoversUseCase(deps FanoutDeps) *FanoutVoiceoversUseCase {
	if deps.Enqueuer == nil {
		panic("voiceover.Jobs.NewFanoutVoiceoversUseCase: Enqueuer is required (FanoutDeps.Enqueuer)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &FanoutVoiceoversUseCase{deps: deps}
}

// Execute fans out N per-item children via the canonical job broker.
// Dispatch contract (P0.3, godlike/07 — no fake availability):
//   - nil cmd → return (nil, error).
//   - cmd.Validate failure → return (nil, error).
//   - ANY child Enqueue failure → (result, error) with result.OK=false
//     and the partial enqueue counts populated (operators see exactly
//     which siblings landed + which failed).
//   - Full Enqueue success → (result, nil).
//
// Step 5 (P0.3 items-model recovery, June 2026): the loop iterates
// cmd.Items (not cmd.Languages — that field was removed from
// GenerateVoiceoversCommand). Each item has its own text/lang/voice/
// filename so each child carries the exact per-item payload.
//
// Payload shape: EnqueueRequest.Payload is `any` (internal/application/
// jobs/types.go). We pass the struct directly so the dispatcher can
// marshal it as JSON; passing pre-marshalled bytes would cause a
// re-marshal into a base64 string and break the consumer's JSON
// round-trip.
func (u *FanoutVoiceoversUseCase) Execute(ctx context.Context, parentJobID string, cmd *voiceover.GenerateVoiceoversCommand) (*FanoutResult, error) {
	if cmd == nil {
		return nil, fmt.Errorf("FanoutVoiceoversUseCase.Execute: nil cmd (parent_job_id=%s)", parentJobID)
	}
	if err := cmd.Validate(); err != nil {
		return nil, fmt.Errorf("FanoutVoiceoversUseCase.Execute: validate (parent_job_id=%s): %w", parentJobID, err)
	}

	total := len(cmd.Items)
	// P0.6 request_id threading: use the caller-supplied request_id
	// when available (threaded from API → CorrelationID by
	// GenerateJobHandler). Fall back to parentJobID (the only stable
	// identifier always available). Never call BuildRequestID() —
	// generating a new random ID at every layer is the root cause of
	// the audit finding: "API request_id (A) → correlation (A) →
	// fanout generates B → child ignores B → GenerateBatch generates C".
	requestID := cmd.RequestID
	if requestID == "" {
		requestID = parentJobID
	}
	result := &FanoutResult{
		OK:           true,
		ParentJobID:  parentJobID,
		RequestID:    requestID,
		TotalOutputs: total,
		ChildJobIDs:  make([]string, 0, total),
		PerLanguage:  make([]string, 0, total),
	}

	for idx, itemSpec := range cmd.Items {
		// Per-item textHash: each item may have a different text so the
		// hash is NOT shared across siblings. This protects per-item
		// dedupe (Step 5 invariant: never merge items with different
		// texts into the same ActiveKey footprint).
		//
		// PR-VO-TYPED-PRIMITIVES (July 2026): the canonical impl is
		// voiceover.ComputeTextHash (the pre-refactor textHashSHA256
		// helper here was a byte-equivalent duplicate, now collapsed).
		itemTextHash := voiceover.ComputeTextHash(itemSpec.Text)

		// E4: per-item filename via canonical BuildVoiceoverFilename with
		// the per-item textHash threaded into the {hash} token slot (so
		// two items with same lang+voice but different text get different
		// filenames, no sibling collision). cmd.Validate already gates
		// Text/Language non-empty above, so the error path is unreachable
		// in production.
		//
		// Graceful degradation (godlike/07 minimal-blast-radius): a
		// filename build failure is a SECONDARY concern — it must degrade
		// this single item to a failed fan-out entry, never panic the
		// whole parent job/worker.
		filename := itemSpec.Filename
		var ferr error
		if filename == "" {
			filename, ferr = voiceover.BuildVoiceoverFilename(voiceover.FilenameSpec{
				Text:     itemSpec.Text,
				Language: itemSpec.Language,
				TextHash: string(itemTextHash),
			})
		}
		if ferr != nil {
			u.deps.Logger.Warn("FanoutUseCase: child filename build failed",
				zap.String("parent_job_id", parentJobID),
				zap.Int("item_index", idx),
				zap.String("language", string(itemSpec.Language)),
				zap.Error(ferr))
			result.FailedEnqueueCount++
			result.PerLanguage = append(result.PerLanguage, string(itemSpec.Language))
			result.ChildJobIDs = append(result.ChildJobIDs, "")
			result.OK = false
			continue
		}

		item := voiceover.GenerateVoiceoverItemCommand{
			ParentJobID:   parentJobID,
			RequestID:     requestID,
			Text:          itemSpec.Text,
			TextHash:      itemTextHash,
			Language:      itemSpec.Language,
			Voice:         itemSpec.Voice,
			Filename:      filename,
			Required:      itemSpec.Required,
			Destination:   cmd.Destination,
			Strategy:      cmd.Strategy,
			RemoveSilence: cmd.RemoveSilence,
			Timing:        cmd.Timing,
			Metadata:      cmd.Metadata,
			// ThreadingCampaign 2026-07-08: propagate the parent's
			// Project field down to every child so the
			// delivery.Publisher path builder sees `cmd.Project` per
			// per-item payload. Without this line, the child struct's
			// fallback (per its goddoc) kicks in: pre-PR-12 FolderID
			// OR canonical voiceover ID — neither surfaces the
			// canonical `{project}/{language}/` Drive subdir layout.
			Project: cmd.Project,
		}
		if err := item.Validate(); err != nil {
			u.deps.Logger.Warn("FanoutUseCase: child command validation failed",
				zap.String("parent_job_id", parentJobID),
				zap.Int("item_index", idx),
				zap.String("language", string(itemSpec.Language)),
				zap.Error(err))
			result.FailedEnqueueCount++
			result.PerLanguage = append(result.PerLanguage, string(itemSpec.Language))
			result.ChildJobIDs = append(result.ChildJobIDs, "")
			result.OK = false
			continue
		}

		// P0.5 idempotency: child ActiveKey covers parentJobID + index +
		// item textHash + item language + item voice so the broker's
		// FindActiveByKey returns the existing child instead of enqueuing
		// a duplicate when the parent is retried after a partial fan-out.
		// Per-item text hash means two items with identical (lang, voice)
		// but different texts are NOT folded into one key (Step 5 fix).
		childActiveKey := fmt.Sprintf("voiceover:item:%s:%d:%s:%s:%s",
			parentJobID, idx, itemTextHash, itemSpec.Language, itemSpec.Voice)

		enqueued, err := u.deps.Enqueuer.Enqueue(ctx, &job.EnqueueRequest{
			Type:          jobvoiceover.TypeGenerateItem,
			Payload:       item,
			CorrelationID: fmt.Sprintf("%s:item:%d", requestID, idx),
			ActiveKey:     childActiveKey,
		})
		if err != nil {
			u.deps.Logger.Warn("FanoutUseCase: enqueue child failed",
				zap.String("parent_job_id", parentJobID),
				zap.Int("item_index", idx),
				zap.String("language", string(itemSpec.Language)),
				zap.Error(err))
			result.FailedEnqueueCount++
			result.PerLanguage = append(result.PerLanguage, string(itemSpec.Language))
			result.ChildJobIDs = append(result.ChildJobIDs, "")
			result.OK = false
			continue
		}
		result.EnqueuedCount++
		result.PerLanguage = append(result.PerLanguage, string(itemSpec.Language))
		result.ChildJobIDs = append(result.ChildJobIDs, enqueued.ID)
	}

	u.deps.Logger.Info("FanoutUseCase: enqueue done",
		zap.String("parent_job_id", parentJobID),
		zap.String("request_id", requestID),
		zap.Int("total", total),
		zap.Int("enqueued", result.EnqueuedCount),
		zap.Int("failed", result.FailedEnqueueCount))

	stageStatuses := make([]job.StageLanguageStatus, 0, total)
	for i, language := range result.PerLanguage {
		status := job.StageQueued
		childJobID := ""
		if i < len(result.ChildJobIDs) {
			childJobID = result.ChildJobIDs[i]
		}
		if childJobID == "" {
			status = job.StageFailed
		}
		stageStatuses = append(stageStatuses, job.StageLanguageStatus{
			Stage: job.StageVoiceover, Language: language, Status: status, JobID: childJobID,
		})
	}
	result.StageProgress = job.AggregateStageProgressByStage(stageStatuses)
	if !result.OK {
		err := fmt.Errorf("FanoutVoiceoversUseCase.Execute: parent_job_id=%s partial fan-out (%d/%d failed)",
			parentJobID, result.FailedEnqueueCount, total)
		return result, err
	}
	return result, nil
}

// buildItemFilenameCanonical — REMOVED in E4 (June 2026). The
// per-language fanout now calls voiceover.BuildVoiceoverFilename
// directly inline at the loop top with the per-item textHash
// threaded into the {hash} token slot. The one-line canonical call
// absorbs both the previous buildItemFilename+slug pair AND the
// per-item uniqueness invariant (text+lang+hash triplet).

// textHashSHA256 — REMOVED in PR-VO-TYPED-PRIMITIVES (July 2026).
// The canonical impl is now voiceover.ComputeTextHash
// (internal/application/voiceover/texthash.go). The pre-refactor
// helper was a byte-equivalent duplicate of
// voiceover.ComputeTextHash (per the audit-pin in the canonical
// impl's package doc) and the consolidation collapses both
// into the typed envelope + canonical impl.
