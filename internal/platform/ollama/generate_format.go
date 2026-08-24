package ollama

import (
	"encoding/json"



	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// resolveGenerationFormat returns the canonical Ollama wire-shape Format
// value for a text-generation request.
//
// LLM-PLAIN-TEXT-CONTRACT wave (PR-3, July 2026): this pure helper
// replaces an inline conditional that was previously hard-coded in
// (*Generator).GenerateScript. The 3 logical branches implement the
// 4-case decision tree (2 OutputMode values × 2 Format presence values):
//
//  1. OutputMode != OutputModeScriptV1 (PlainText or empty):
//     return nil — Ollama stays in prose mode, no JSON constraint.
//     This is the canonical post-PR-2 path for the script pipeline:
//     engine_generate.go sets OutputModePlainText unconditionally,
//     and downstream SceneSynthesizer + scene binder derive structured
//     fields from the raw prose (no JSON envelope on the wire).
//
//  2. OutputMode == OutputModeScriptV1 AND caller-supplied Format empty:
//     return json.RawMessage(`"json"`) — force Ollama into native
//     JSON-mode so the model response is constrained to syntactically
//     valid JSON. This is the legacy defence for deprecated active
//     callers that still pass OutputModeScriptV1 in their current
//     request payload. Pre-wave callers were never updated to
//     PlainText; without this fallback the LLM emits prose and
//     downstream jsonextract decoders immediately raise
//     ErrModelOutputMalformed on the model output.
//     (Note: cached pre-wave rows skip GenerateScript entirely via
//     TranslateText.* cache fast-path; this fallback does NOT defend
//     cache hits, only the active GenerateScript call surface.)
//
//  3. OutputMode == OutputModeScriptV1 AND caller-supplied Format
//     non-empty (cases 4 of the 2x2 grid):
//     return req.Format verbatim (passthrough). Test rigs and future
//     non-script callers opt out of the auto-fill by pre-setting Format.
//
// Native json-mode does NOT enforce a schema — the plainTextInstruction
// prompt suffix does that (see engine_prompt.go). The wire-format
// trigger here is the FIRST half of the V1 contract defence.
//
// Format is a TOP-LEVEL body field on Ollama's `/api/chat` endpoint —
// it is NOT inside `options` (where Ollama would silently ignore it as
// a non-model parameter). See types.ChatRequest.Format and
// client_core.go::doChatRequest for the canonical wire wiring.
//
// Pure function (value parameter via Go semantics). A future refactor
// that flips to *types.TextGenerationRequest and mutates req would
// surface above the call site because req.Format is aliased both ways
// (the helper itself would still compile, but the caller-observable
// effect would leak). TestResolveGenerationFormat_NoInputMutation is
// the observable per-call guarantee.
func resolveGenerationFormat(req types.TextGenerationRequest) json.RawMessage {
	if len(req.Format) > 0 {
		return req.Format
	}
	if req.OutputMode == types.OutputModeScriptV1 {
		return json.RawMessage(`"json"`)
	}
	return nil
}
