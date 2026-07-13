// internal/application/qdrant/maintenance/output.go — typed CLI-output
// adapter for the qdrant-maintenance per-mode handlers.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of "how the
// per-mode handlers format + write CLI UX output for human operators
// and machine consumers". Per-mode call sites migrate from
// `fmt.Fprintln(s.cliWriter, ...)` + `fmt.Fprintf(s.cliWriter, ...)` to
// the typed methods JSON / HumanLine / HumanLinef exposed here.
//
// Scope discipline (CR-thinker-validated, 2026-07):
//
//   - This adapter owns ONLY the io.Writer side of CLI UX output.
//   - It does NOT replace `Service.log *zap.Logger` (the
//     structured-telemetry surface). That field stays because
//     Service.initHeavy passes `s.log` directly into
//     `storage.OpenSQLiteDB(..., s.log)` and `app.InitComposition(
//     s.cfg, s.log)`. Folding zap into this adapter would either force
//     a `out.Logger()` getter (a godlike/07 minimum-blast-radius
//     regression: re-exports the structured logger through an extra
//     indirection) or weaken the heavy-init contract by accepting a
//     weaker interface upstream. Both are anti-patterns.
//   - godlike/07 NO-FAKE-AVAILABILITY:
//   - Nil io.Writer -> defaults to `os.Stdout` (CLI UX must always
//     land where the operator reads; silent no-op is the failing-
//     closed-as-silent-noop anti-pattern).
//   - `json.Marshal` error from the JSON method is RETURNED, not
//     silently swallowed (machine consumers expect a JSON line and
//     any marshal failure must surface somewhere — the typed error
//     is the only way per Q4 NO-FAKE-AVAILABILITY).
package maintenance

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// CLIOutput is the godlike/06 SSOT CLI-UX writer for the
// qdrant-maintenance per-mode handlers. Constructed by NewService and
// exposed to per-mode handlers via Service.cli.
//
// The struct holds ONLY `w io.Writer`. The `*zap.Logger` stays on the
// Service struct as `Service.log` — it is the structured-telemetry
// surface (godlike/06 SSOT: one canonical owner per fact; CLI UX and
// structured telemetry are distinct concerns).
type CLIOutput struct {
	w io.Writer
}

// NewCLIOutput constructs the typed CLI-UX adapter.
//
// godlike/07 fail-closed default: nil w defaults to os.Stdout (CLI
// operators expect output on stdout; the alternative — silent no-op —
// is the godlike/07 NO-FAKE-AVAILABILITY anti-pattern). Tests pass
// bytes.Buffer or strings.Builder explicitly.
func NewCLIOutput(w io.Writer) *CLIOutput {
	if w == nil {
		w = os.Stdout
	}
	return &CLIOutput{w: w}
}

// JSON marshals v as JSON (one line + '\n') into the underlying writer.
// Returns the marshal error if `json.Marshal` fails — caller decides
// whether to log the error as a warning (partial-report code paths in
// repair-locators / delete-invalid) or propagate it (audit mode).
//
// godlike/07 NO-FAKE-AVAILABILITY: silently swallowing the marshal
// error would emit empty/silent state when machine consumers expect
// a JSON line. The typed error is the only canonical failure surface
// for that scenario per CR-thinker Q4.
func (o *CLIOutput) JSON(v any) error {
	if o == nil || o.w == nil {
		return fmt.Errorf("maintenance.CLIOutput.JSON: writer is nil (NewCLIOutput(nil) fell through to os.Stdout; cannot marshal)")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("maintenance.CLIOutput.JSON: marshal failed: %w", err)
	}
	if _, err := fmt.Fprintln(o.w, string(b)); err != nil {
		return fmt.Errorf("maintenance.CLIOutput.JSON: write failed: %w", err)
	}
	return nil
}

// HumanLine writes a single line (with '\n') of human-readable text.
// Use for block headers ("=== qdrant-maintenance audit ===") and
// pre-formatted strings from `legacyaudit.StringifyReport`. This is
// the typed-method replacement for
// `fmt.Fprintln(s.cliWriter, preformattedString)`.
func (o *CLIOutput) HumanLine(s string) {
	if o == nil || o.w == nil {
		return
	}
	_, _ = fmt.Fprintln(o.w, s)
}

// HumanLinef writes a `Printf`-formatted line (with trailing '\n' if
// the format string ends in '\n' — caller controls the newline) of
// human-readable text. Replaces
// `fmt.Fprintf(s.cliWriter, format, args...)` calls in the per-mode
// handlers.
// Per CR-thinker Q5: this is the typed-method godlike/06 SSOT point
// for the format-then-write pattern in the qdrant-maintenance package.
//
// Write errors are intentionally swallowed: CLI UX output goes to
// os.Stdout (default) or a bytes.Buffer in tests — neither fails in
// practice. Operators see the buffered content; test captures the buf.
// This is the canonical operator-UX convention across the project.
func (o *CLIOutput) HumanLinef(format string, args ...any) {
	if o == nil || o.w == nil {
		return
	}
	_, _ = fmt.Fprintf(o.w, format, args...)
}
