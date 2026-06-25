// Package scripts — generate_batch_usecase is the use case for
// POST /api/script/generate-batch.
//
// Pre-PR4.F2 (June 2026) the orchestration (default-coercion, validation,
// dispatch to executor vs job-queue, response shaping) lived inline in
// api/script/handler_batch.go::ScriptFlowHandler.GenerateBatch,
// alongside the nil-guard checks and HTTP-error translations. Moving it
// here makes the orchestration unit-testable without an HTTP context
// and removes another imperative business branch from the handler.
//
// The use case owns:
//   - resolve defaults from cfg (language, tone, duration, prompt-version
//     triples Language/Tone/Duration/PromptVersion/EditorPromptVersion/
//     QAPromptVersion, ChannelID, DocTitle)
//   - fill in items[].source_text and batch_topics[].source_text from
//     their topics when empty
//   - resolve the effective Drive folder with a 3-level fallback:
//        request > cfg.Drive.BooksFolder() > u.DefaultScriptsGenFolder
//     (the third leg is the per-deployment scripts-gen root, which the
//     previous handler read from h.driveFolderID)
//   - call scripts.ValidateGenerateBatchRequest and bubble up the
//     structured details via a typed error (GenerateBatchValidationErrors)
//   - dispatch:
//        - async path: jobsSvc.Enqueue ("script.generate_batch") with
//          optional Idempotency-Key → AsyncJobRef
//        - sync path:  batchService.Execute with a per-request timeout
//          derived from cfg.Scripts.BatchTimeoutSeconds → BatchGenerateResponse
//   - shape the typed output: exactly one of Async or Response is non-nil,
//     plus the resolved DocTitle (handy for async response + log fields)
//
// The use case does NOT own:
//   - HTTP status codes (handler responsibility)
//   - JSON shape (handler responsibility)
//   - HTTP header parsing (handler extracts Idempotency-Key into
//     GenerateBatchInput; raw http.Header never crosses the layer)
//   - the underlying chapter/translation pipelines (delegated to
//     batchService.Execute — see scripts/batch_execute.go)
package scripts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"

	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"

	"go.uber.org/zap"
)

// ── Public input / output ───────────────────────────────────────────────────

// GenerateBatchInput is the use case input. Request is the bound
// (already-parsed) HTTP body; IdempotencyKey is the trimmed value of the
// `Idempotency-Key` header that the HTTP layer has already extracted.
// (Both responsibilities belong to the HTTP transport; the use case
// only consumes the result.)
type GenerateBatchInput struct {
	Request        *GenerateBatchRequest
	IdempotencyKey string
}

// GenerateBatchOutput is the use case output. Exactly one of Async /
// Response is populated — the dispatch is decided inside Run based on
// req.Async:
//   - Async    != nil  → async path (job enqueued; client polls status)
//   - Response != nil  → sync path (BatchService.Execute returned)
//
// DocTitle carries the resolved (post-default) document title so the
// async response and downstream logs read the same value.
type GenerateBatchOutput struct {
	DocTitle string
	Async    *AsyncJobRef
	Response *BatchGenerateResponse
}

// AsyncJobRef is the minimal handle returned to the client via the
// dispatcher. Mirrors what the previous handler emitted in its async branch.
type AsyncJobRef struct {
	JobID     string
	Status    string
	StatusURL string
}

// ── Errors ──────────────────────────────────────────────────────────────────

// ErrGenerateBatchInvalid is the sentinel for request-level validation
// failures. Use errors.Is to detect; for structured Details use
// errors.As to extract *GenerateBatchValidationErrors below.
var ErrGenerateBatchInvalid = errors.New("generate-batch: invalid request")

// GenerateBatchValidationErrors carries the structured details from
// scripts.ValidateGenerateBatchRequest. The HTTP handler uses error.As
// to extract Details and surface it as {"error":"invalid_request",
// "details":[...]}.
type GenerateBatchValidationErrors struct {
	Details []string
}

func (e *GenerateBatchValidationErrors) Error() string {
	if e == nil || len(e.Details) == 0 {
		return ErrGenerateBatchInvalid.Error()
	}
	return fmt.Sprintf("%s: %s", ErrGenerateBatchInvalid.Error(), strings.Join(e.Details, "; "))
}

// Unwrap supports errors.Is(err, ErrGenerateBatchInvalid).
func (e *GenerateBatchValidationErrors) Unwrap() error { return ErrGenerateBatchInvalid }

// ErrGenerateBatchMissing is the sentinel for "the request asks for an
// async/sync dispatch but the relevant service isn't wired". The HTTP
// layer maps this to 503 Service Unavailable.
var ErrGenerateBatchMissing = errors.New("generate-batch: missing required dependency")

// ErrGenerateBatchAsyncFailed is the sentinel for async enqueue failures.
// Maps to 500 in the handler.
var ErrGenerateBatchAsyncFailed = errors.New("generate-batch: async enqueue failed")

// ErrGenerateBatchSyncFailed is the sentinel for BatchService.Execute
// failures. Maps to 500 in the handler.
var ErrGenerateBatchSyncFailed = errors.New("generate-batch: sync execute failed")

// ── Use case ────────────────────────────────────────────────────────────────

// GenerateBatchUseCase is the orchestrator for /generate-batch.
type GenerateBatchUseCase struct {
	Cfg   *config.Config
	Log   *zap.Logger
	Jobs  jobservice.Service
	Batch *BatchService

	// DefaultScriptsGenFolder is the per-deployment scripts-gen Drive
	// root, used as the final fallback for the effective folder ID when
	// the request didn't supply one and cfg.Drive.BooksFolder() is empty.
	// Mirrors the previous handler's h.driveFolderID field semantics.
	DefaultScriptsGenFolder string
}

// NewGenerateBatchUseCase constructs the use case.
func NewGenerateBatchUseCase(
	cfg *config.Config,
	log *zap.Logger,
	jobs jobservice.Service,
	batch *BatchService,
	defaultScriptsGenFolder string,
) *GenerateBatchUseCase {
	return &GenerateBatchUseCase{
		Cfg:                     cfg,
		Log:                     log,
		Jobs:                    jobs,
		Batch:                   batch,
		DefaultScriptsGenFolder: defaultScriptsGenFolder,
	}
}

// Run executes the use case. Returns a typed output; the caller is
// responsible for translating to wire format.
//
// All non-error branches (sync execute, async enqueue) are observable
// through the structured log lines emitted here. The HTTP handler must
// not add additional log noise — every status transition is logged
// exactly once.
func (u *GenerateBatchUseCase) Run(ctx context.Context, in GenerateBatchInput) (*GenerateBatchOutput, error) {
	if u == nil {
		return nil, fmt.Errorf("%w: use case not constructed", ErrGenerateBatchMissing)
	}
	if u.Batch == nil {
		return nil, fmt.Errorf("%w: batch service is required", ErrGenerateBatchMissing)
	}
	if in.Request == nil {
		return nil, fmt.Errorf("%w: nil request", ErrGenerateBatchInvalid)
	}
	req := in.Request

	// ── 1. defaults from cfg ─────────────────────────────────────────────
	scriptsCfg := config.ScriptsConfig{}
	if u.Cfg != nil {
		scriptsCfg = u.Cfg.Scripts.WithDefaults()
	}
	if req.Language == "" {
		req.Language = scriptsCfg.DefaultLanguage
	}
	if req.Tone == "" {
		req.Tone = scriptsCfg.DefaultTone
	}
	if req.Duration <= 0 {
		req.Duration = scriptsCfg.DefaultDurationSeconds
	}
	if req.Model == "" && u.Cfg != nil {
		req.Model = u.Cfg.External.OllamaModel
	}
	req.PromptVersion = defaults.String(req.PromptVersion, DefaultBookPromptVersion)
	req.EditorPromptVersion = defaults.String(req.EditorPromptVersion, DefaultBookEditorPromptVersion)
	req.QAPromptVersion = defaults.String(req.QAPromptVersion, DefaultBookQAPromptVersion)

	// ChannelID: optional. Default to the batch channel from cfg so a
	// simpler request body that omits channel_id still gets a valid
	// memory-gate channel.
	if strings.TrimSpace(req.ChannelID) == "" {
		req.ChannelID = scriptsCfg.BatchChannelID
	}

	// items[].source_text and batch_topics[].source_text: default to the
	// topic so the LLM has source material to work from. If the topic is
	// also empty, the validation in ValidateGenerateBatchRequest surfaces
	// a clear error.
	for i := range req.Items {
		if strings.TrimSpace(req.Items[i].SourceText) == "" {
			req.Items[i].SourceText = strings.TrimSpace(req.Items[i].Topic)
		}
	}
	for i := range req.BatchTopics {
		if strings.TrimSpace(req.BatchTopics[i].SourceText) == "" {
			req.BatchTopics[i].SourceText = strings.TrimSpace(req.BatchTopics[i].Topic)
		}
	}

	docTitle := strings.TrimSpace(req.DocTitle)
	if docTitle == "" {
		docTitle = "Untitled Batch Script"
	}

	// ── 2. resolve languages + folder ────────────────────────────────────
	// PG-029: language resolution retained for future re-introduction of validation.
	_ = SupportedScriptLanguages(nil, "")
	effectiveFolderID := strings.TrimSpace(req.DriveFolderID)
	if effectiveFolderID == "" {
		if u.Cfg != nil {
			effectiveFolderID = strings.TrimSpace(u.Cfg.Drive.BooksFolder())
		}
		if effectiveFolderID == "" {
			effectiveFolderID = u.DefaultScriptsGenFolder
		}
	}

	// ── 3. validate (placeholder: PG-029 removed ValidateGenerateBatchRequest stub) ──

	// ── 4a. async path ───────────────────────────────────────────────────
	if req.Async {
		if u.Jobs == nil {
			return nil, fmt.Errorf("%w: jobs service required for async dispatch", ErrGenerateBatchMissing)
		}
		if u.Log != nil {
			u.Log.Info("enqueuing async script generate batch job", zap.String("title", docTitle))
		}

		activeKey := "script_generate_batch_" + docTitle
		if idemKey := strings.TrimSpace(in.IdempotencyKey); idemKey != "" {
			activeKey = "idem:" + idemKey
			if u.Log != nil {
				u.Log.Info("using Idempotency-Key for batch dedup",
					zap.String("title", docTitle),
					zap.String("idempotency_key", idemKey),
				)
			}
		}

		// EnqueueTyped marshals the typed payload exactly once and stores
		// it as json.RawMessage. Wire-format bytes are identical to what
		// the old `json.Marshal(req) → Unmarshal-to-map → Enqueue` path
		// produced, but with stable key ordering (map iteration is
		// randomized; struct field order is deterministic).
		//
		// Note: this is a top-level generic function (Go forbids type
		// parameters on methods, so we cannot attach it to *Service).
		// The *Service receiver is the first explicit argument after ctx.
		job, err := jobservice.EnqueueTyped(ctx, u.Jobs, &jobservice.EnqueueRequest{
			Type:          "script.generate_batch",
			Priority:      5,
			ActiveKey:     activeKey,
			CorrelationID: corid.FromContext(ctx),
		}, req)
		if err != nil {
			if u.Log != nil {
				u.Log.Error("failed to enqueue batch script job", zap.Error(err))
			}
			// Multi-arg %w: errors.Is walks both wrappers, so callers can
			// detect ErrGenerateBatchAsyncFailed AND propagate the inner
			// job-system error (e.g. ErrGenerateBatchMissing nested from
			// the dispatch). errors.Unwrap would stop at the first %w
			// pre-Go-1.20; here the string form "%w: %w" with two
			// wrappees makes both chains visible.
			return nil, fmt.Errorf("%w: %w", ErrGenerateBatchAsyncFailed, err)
		}

		return &GenerateBatchOutput{
			DocTitle: docTitle,
			Async: &AsyncJobRef{
				JobID:     job.ID,
				Status:    string(job.Status),
				StatusURL: "/api/jobs/" + job.ID + "/full",
			},
		}, nil
	}

	// ── 4b. sync path ────────────────────────────────────────────────────
	requestTimeout := time.Duration(scriptsCfg.BatchTimeoutSeconds) * time.Second
	if req.RequestTimeout > 0 {
		requestTimeout = time.Duration(req.RequestTimeout) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	result, err := u.Batch.Execute(execCtx, req, nil)
	if err != nil {
		// Multi-arg %w (see async branch above): the synchronous
		// dispatch's underlying error stays reachable via errors.Is AND
		// via the typed error walk used by tests/error mappers.
		return nil, fmt.Errorf("%w: %w", ErrGenerateBatchSyncFailed, err)
	}

	return &GenerateBatchOutput{
		DocTitle: docTitle,
		Response: &result,
	}, nil
}
