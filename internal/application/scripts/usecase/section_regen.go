// Package scripts: section_regen is the "regenerate a single section" use
// case. Pre-PR4.F (June 2026) the logic lived inline in
// api/script/handler_flow_ops.go::RegenerateSection, mixing HTTP parsing,
// prompt construction, Ollama invocation, persistence, doc re-upload,
// and serialization all in one 120-line Gin handler.
package usecase

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
)

// ── Repository contract types (PR 4 — scriptado Section regen) ────────────

// ScriptRecord is the canonical scripts row used by SectionRegenerator.
type ScriptRecord struct {
	ID           int64
	Topic        string
	Language     string
	Template     string
	ModelUsed    string
	MetadataJSON string
	Duration     int
}

// ScriptSectionRecord is the canonical scripts_sections row used by
// SectionRegenerator.
type ScriptSectionRecord struct {
	ID           int64
	ScriptID     int64
	SectionTitle string
	Content      string
	SortOrder    int
}

// ── Typed dependency ports (SCRIPT-T03-002, 2026-07-04, Phase 9) ─────
//
// SCRIPT-T03-002 godlike/07 typed-error contract: the canonical
// regeneration pipeline consumes 3 typed ports:
//
//   - SectionGenerator: text-prompt → text-content (LLM call shape)
//   - DocumentBuilder: regenerated section text → optional Drive doc URL
//   - Logger: structured log surface (matches *zap.Logger shape)
//
// godlike/07 minimum-blast-radius: the production caller in
// `internal/app/wire_script_usecases.go::buildScriptUseCases` passes
// concrete `*ollama.Generator` + `DocClient` + `*zap.Logger` to the
// constructor. The constructor signature is `any` for `gen/doc/log`
// to preserve the existing call site compile-clean (changing the
// signature to typed ports would break the build because the
// production concretes do not implement the structural surface
// captured by these interfaces — that's the future migration's
// job, not the Phase 9 closure's). The typed ports below are the
// CANONICAL contract surface for the future CUTOVER-phase impl;
// today's stub accepts `any` and discards it.

// SectionGenerator is the typed port for the LLM call. Returns the
// regenerated section content (the only output the use case consumes).
type SectionGenerator interface {
	GenerateSection(ctx context.Context, prompt string) (content string, err error)
}

// DocumentBuilder is the typed port for the optional Drive doc
// re-upload side-effect. Returns the new doc URL on success; the
// use case is resilient to an empty result (no doc was rebuilt).
type DocumentBuilder interface {
	UpsertSectionDoc(ctx context.Context, scriptID, sectionID int64, content string) (docURL string, err error)
}

// Logger is the structured logging surface (mirrors *zap.Logger +
// search.Logger shapes — any concrete satisfying the 4-method set
// works; `*zap.Logger` satisfies it natively).
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
}

// SectionRegenerator is the use-case for regenerating a single script section.
//
// SCRIPT-T03-002 (2026-07-04, Phase 9 closure): the constructor
// preserves the pre-PR `any` surface for `gen/doc/log` to keep the
// existing wire site compiling. The typed ports above are the
// canonical contract for the future CUTOVER-phase impl; today's
// stub accepts `any` and discards it. The `Regenerate` method
// remains a Phase 1b stub (godlike/07 honest-limitation: a full impl
// would call SectionGenerator + persist via repo + UpsertSectionDoc,
// and that orchestration is forward-pointer to a future PR — the
// Phase 9 surface ships the typed contract + 4 typed-error sentinels
// + 1 stub sentinel so future callers can wire concrete adapters
// without further surface churn).
type SectionRegenerator struct {
	repo adapters.ScriptRepository
	gen  any
	doc  any
	log  any
}

// NewSectionRegenerator constructs a SectionRegenerator. The
// `gen`/`doc`/`log` parameters are typed as `any` for backward
// compat (the production wire site in wire_script_usecases.go
// passes concrete `*ollama.Generator`, `DocClient`, `*zap.Logger`).
// The typed ports above are the forward-pointer contract — when
// the CUTOVER-phase impl lands, callers wrap their concretes in
// thin adapters that satisfy the typed-port shape.
func NewSectionRegenerator(repo adapters.ScriptRepository, gen any, doc any, log any) *SectionRegenerator {
	return &SectionRegenerator{repo: repo, gen: gen, doc: doc, log: log}
}

// SectionRegenRequest is the request type for section regeneration.
type SectionRegenRequest struct {
	SectionID   int64  `json:"section_id"`
	ScriptID    int64  `json:"script_id"`
	RegenPrompt string `json:"regen_prompt,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Model       string `json:"model,omitempty"`
}

// ErrSectionNotFound is returned when a section is not found.
var ErrSectionNotFound = errors.New("section not found")

// ErrScriptNotFound is returned when a script is not found.
var ErrScriptNotFound = errors.New("script not found")

// ErrSectionScriptMismatch is returned when a section does not belong to the script.
var ErrSectionScriptMismatch = errors.New("section does not belong to the specified script")

// ErrEmptyGeneratorOutput is returned when the generator produces empty output.
var ErrEmptyGeneratorOutput = errors.New("generator produced empty output")

// ErrSectionRegenNotImplemented is the SCRIPT-T03-002 Phase 1b
// stub sentinel. Returns from Regenerate when the canonical
// implementation is not yet wired. godlike/07 typed-error contract:
// callers branch on errors.Is(err, ErrSectionRegenNotImplemented)
// to detect the stub state and surface a clear 501-like signal
// to clients (the handler in handler_flow_ops.go maps this to
// 503 Service Unavailable per the existing nil-tolerance).
var ErrSectionRegenNotImplemented = errors.New("section regeneration not implemented (Phase 1b stub; SCRIPT-T03-002 typed contract ships without canonical impl)")

// SectionRegenResult is the result of a section regeneration.
type SectionRegenResult struct {
	SectionID int64  `json:"section_id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
}

// Regenerate regenerates a single script section.
//
// SCRIPT-T03-002 (2026-07-04, Phase 9 closure): the implementation
// remains a Phase 1b stub. godlike/07 honest-limitation: a full
// implementation requires (a) loading the section via repo + the
// adjacent sections for prompt context, (b) constructing the
// regeneration prompt from req.Instruction + section content +
// adjacent context, (c) calling SectionGenerator.GenerateSection,
// (d) validating the generator output is non-empty (else return
// ErrEmptyGeneratorOutput), (e) persisting via repo.UpdateSectionContent,
// (f) optionally rebuilding the Drive doc via DocumentBuilder.
// That orchestration is the canonical CUTOVER-phase surface and
// ships in a follow-up PR; the Phase 9 closure delivers the typed
// contract + 4 typed-error sentinels so future callers can wire
// concrete adapters without further surface churn.
//
// The caller-facing contract: until the canonical impl lands, the
// stub returns the canonical sentinel `ErrSectionRegenNotImplemented`
// so callers can branch on it. The 4 typed-error sentinels
// (ErrSectionNotFound, ErrScriptNotFound, ErrSectionScriptMismatch,
// ErrEmptyGeneratorOutput) are reserved for the canonical impl and
// MUST be returned as errors.Is-equivalent.
func (s *SectionRegenerator) Regenerate(ctx context.Context, req SectionRegenRequest) (*SectionRegenResult, error) {
	return nil, ErrSectionRegenNotImplemented
}
