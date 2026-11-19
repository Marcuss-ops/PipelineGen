package voiceover

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// ── PR-VO-AUDIT-P01 helpers ─────────────────────────────────────────

// fail is the canonical failure normaliser. The previous implementation
// forward-stored the literal status (e.g. "tts_failed", "upload_failed",
// "missing_folder_id") and relied on downstream checks
// `if item.Status == "failed"` to gate the pipeline. Those checks missed
// every failure literal that wasn't exactly "failed", so a tts_failed in
// synthesizeStage could fall through Stage 2 with `Status == "tts_failed"`,
// then finalizeStage committed the record with `Status = "completed"` —
// silent false-success (the canonical audit P0.1 bug).
//
// The fix normalises every failure to Status(StatusFailed) regardless of
// the specific code, then records the code in item.Errors (omitempty-driven)
// so the forensic trail survives. The downstream aggregate check becomes
// `if item.Status == StatusFailed { ok = false }` — no more substring matching.
//
// Nil err case: the helper tolerates nil err (Error stays empty) so callers
// that want to mark a StatusFailed without a concrete message can still
// surface the failure code in Errors[] without lying about an empty message
// string.
func (i *BatchItem) fail(code FailureCode, err error) BatchItem {
	i.Status = Status(StatusFailed)
	if err != nil {
		i.Error = err.Error()
	}
	i.Errors = append(i.Errors, code)
	return *i
}

func randomSuffix(n int) string {
	if n <= 0 {
		return ""
	}
	size := (n + 1) / 2
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().Format("150405")
	}
	return hex.EncodeToString(buf)[:n]
}
