// Package payloadcap bounds the JSON blobs written to the operational SQLite
// planes (audit DB-04, 2026-11-27).
//
// job_results.result_payload and run_observability.report_json are written by
// several producers and had no upper bound: one 4.2 MB result and thousands of
// 100 KB results made job_results 620 MB on its own. The cap is deliberately
// high enough that it never touches a legitimate payload observed in
// production (max result 4.17 MB, max report 609 KB at the time of the audit),
// while stopping a runaway producer from writing an unbounded blob.
//
// Truncation is explicit, never silent: the stored value stays valid JSON and
// carries the original size and SHA-256 plus a head/tail excerpt, so an operator
// can find the source payload and a consumer can detect the condition by the
// `truncated` field instead of decoding garbage.
package payloadcap

import (
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const (
	// ResultBytes bounds job_results.result_payload. The largest real payload
	// measured before the cap was 4,175,643 bytes, so 4 MiB preserves every one
	// of them and only rejects a new pathological write.
	ResultBytes = 4 << 20
	// ReportBytes bounds run_observability.report_json (max measured 608,830
	// bytes).
	ReportBytes = 1 << 20
	// excerptBytes is how much of each end of the original payload the
	// truncation envelope keeps for diagnosis.
	excerptBytes = 1024
)

// truncation is the JSON envelope stored in place of an oversized payload.
// Field names are part of the on-disk contract: consumers check `truncated`.
type truncation struct {
	Truncated    bool   `json:"truncated"`
	LimitBytes   int    `json:"limit_bytes"`
	OriginalSize int    `json:"original_bytes"`
	OriginalHash string `json:"original_sha256"`
	Head         string `json:"head"`
	Tail         string `json:"tail"`
}

// Enforce returns raw unchanged when it fits within limit. Otherwise it returns
// a valid JSON truncation envelope and true. A non-positive limit disables the
// cap (callers must opt in to an unbounded payload explicitly).
func Enforce(raw string, limit int) (string, bool) {
	if limit <= 0 || len(raw) <= limit {
		return raw, false
	}
	env := truncation{
		Truncated:    true,
		LimitBytes:   limit,
		OriginalSize: len(raw),
		OriginalHash: digest.SHA256Bytes([]byte(raw)),
		Head:         raw[:excerptBytes],
		Tail:         raw[len(raw)-excerptBytes:],
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		// json.Marshal only fails on unsupported types; every field is a
		// string/bool/int, so this branch is unreachable. Fail closed with a
		// minimal valid envelope rather than writing a partial payload.
		return `{"truncated":true,"original_bytes":0,"original_sha256":"","head":"","tail":""}`, true
	}
	return string(encoded), true
}

// EnforceResult caps a job result payload.
func EnforceResult(raw string) (string, bool) { return Enforce(raw, ResultBytes) }

// EnforceReport caps a run report payload.
func EnforceReport(raw string) (string, bool) { return Enforce(raw, ReportBytes) }
