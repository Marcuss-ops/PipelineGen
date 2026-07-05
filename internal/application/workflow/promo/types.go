// Package promo provides a multi-language voiceover generation workflow.
// Extracted from voiceover/promo.go (PR 6, June 2026).
package promo

import "errors"

// PR-VO-A5/A6 (June 2026): typed sentinel errors for operator-grep
// stability. The legacy impl formatted Result.Error via ad-hoc string
// literals ("translation failed: %v") which were NOT sealed and would
// break every dashboard grep on a future refactor. Sealed below —
// callers can `errors.Is(err, promo.ErrTranslationFailed)` and the
// wrapped error message starts with the literal "promo:" prefix.
var (
	// ErrTranslationFailed wraps any translator error. The wrapped
	// sentinel is surfaced in resp.Results[i].Error with the canonical
	// "promo: translation failed: <%v>" prefix. tests in
	// generate_test.go use strings.HasPrefix + the ErrTranslationFailed
	// signature combination so dashboards + unit tests stay in lockstep.
	ErrTranslationFailed = errors.New("promo: translation failed")

	// ErrVoiceoverFailed wraps any voGen.Generate error. Surface in
	// resp.Results[i].Error with "promo: voiceover failed: <%v>".
	ErrVoiceoverFailed = errors.New("promo: voiceover failed")

	// ErrTranslationEmpty is the empty/whitespace payload guard added
	// alongside ErrTranslationFailed. The canonical translator returns
	// (text, nil) on success — but ``strings.TrimSpace(text) == ""`` is
	// a degenerate case that downstream TTS engines either hang or
	// produce 0-byte audio on. We treat empty payloads as a failure
	// so the operator sees a deterministic reject regardless of
	// AllowUntranslated (the strict-mode path publishes a Result entry;
	// the lenient-mode path silently skips).
	ErrTranslationEmpty = errors.New("promo: translation returned empty payload")
)

// Request represents a promotional voiceover request.
//
// PR-VO-A6 (strict translator failure, June 2026): the legacy default
// silently dropped any language whose Ollama translation failed. The
// Result entry was never published and the Failed counter never
// incremented — operators had no signal that the batch was
// incomplete. AllowUntranslated=false (the JSON-zero default) is now
// the fail-closed mode: every translation failure populates a Result
// entry, increments Failed, and surfaces the language as failed in
// the response. Operators set AllowUntranslated=true to opt back into
// the lenient skip-on-fail behaviour (e.g. for hand-curated lists
// where partial success is acceptable).
type Request struct {
	Text          string   `json:"text" binding:"required"`
	DriveFolderID string   `json:"drive_folder_id,omitempty"`
	DryRun        bool     `json:"dry_run,omitempty"`
	Languages     []string `json:"languages,omitempty"`
	// AllowUntranslated opts back into the lenient (legacy) behaviour:
	// translation failures are silently dropped from the response and
	// the corresponding language does not appear in Results. Must be
	// explicitly true to skip the strict accounting. JSON name is
	// snake_case to match the existing payload shape; the default
	// false means strict / fail-closed.
	AllowUntranslated bool `json:"allow_untranslated,omitempty"`
}

// Result holds the result of a single promo language attempt.
//
// PR-VO-A5 (June 2026): every Result entry is now populated for every
// requested language — including the case where translation failed
// and voiceover was not attempted. OK reflects the full per-language
// outcome:
//   - dry-run: OK=true iff translation succeeded (voiceover not
//     attempted by definition);
//   - real-run: OK=true iff translation AND voiceover both
//     succeeded. Translation-only failures surface with OK=false
//     and Error containing "translation failed: ...".
//
// The Translated field is non-empty on translation success and
// empty ("") on translation failure. Whether voiceover succeeded is
// indicated by DriveLink/DriveFileID being non-empty.
type Result struct {
	OK          bool   `json:"ok"`
	Language    string `json:"language"`
	Translated  string `json:"translated,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Response aggregates all promo voiceover results.
//
// PR-VO-A5: the accounting model is now consistent across dry-run and
// real-run:
//
//   - Total = len(targets). ALWAYS the count of languages the caller
//     asked for (filtered by req.Languages when present, else the
//     translated.DefaultPromoLanguages() default set). Translation
//     failures no longer shrink this number.
//   - Success = count of voiceover generations that succeeded (real)
//     OR translations that succeeded (dry-run, where voiceover is
//     not attempted). Always a subset of Total.
//   - Failed = count of outcomes that did not succeed — translation
//     failures + voiceover failures, summed. Always (Total - Success)
//     on a non-OK response.
//   - OK = (Failed == 0). Was hardcoded to true in the legacy
//     implementation; now reflective of actual state so an operator
//     dashboard can colour the badge.
//   - Results = one entry per target, in target order. Always
//     len(targets) entries; never omitted (with the AllowUntranslated
//     opt-in exception: legacy lenient mode omits failed targets so
//     callers that relied on that count must migrate).
type Response struct {
	OK      bool     `json:"ok"`
	Total   int      `json:"total"`
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
	Results []Result `json:"results"`
}
