// Package images — slide_worker_protocol.go (PR-CHROME-PROVIDER-SPLIT, 2026-07-04):
// JSON wire protocol helpers for the slide_worker.py subprocess.
//
// PR-CHROME-PROVIDER-SPLIT (2026-07-04, godlike/06 + AGENTS.md Pattern 5):
// extracted from the pre-split ~260-LoC god file into a single-purpose
// capability file in the same package. Owns the JSON encoding layer:
//   - writeJSON(v any) — marshal + write newline-delimited JSON to stdin
//   - readResponse(expectedID string) — read + parse + ID-match the next
//     response (returns *workerResponse)
//   - readRawResponse() — read + parse the next response (returns
//     map[string]any for the warmup/health actions that don't carry an ID)
//   - workerResponse — typed envelope of the per-request response
//   - mapToStruct — round-trip helper for raw → typed unmarshal
//
// Imports needed by this file (single-purpose slice per Pattern 5): the
// canonical ChromeImageProvider fields (p.stdin, p.stdout, p.cmd) + the
// stdlib JSON/bufio/fmt primitives.
package chrome

import (
	"encoding/json"
	"fmt"
)

// writeJSON marshals and writes a JSON object line to the worker's stdin.
// Must be called while p.mu is held.
func (p *ChromeImageProvider) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	_, err = fmt.Fprintf(p.stdin, "%s\n", data)
	return err
}

// readResponse reads a response line from the worker and expects it to
// match the given requestID.
func (p *ChromeImageProvider) readResponse(expectedID string) (*workerResponse, error) {
	raw, err := p.readRawResponse()
	if err != nil {
		return nil, err
	}
	var resp workerResponse
	if err := mapToStruct(raw, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (raw=%v)", err, raw)
	}
	if resp.ID != expectedID {
		return nil, fmt.Errorf("response ID mismatch: expected %s, got %s", expectedID, resp.ID)
	}
	return &resp, nil
}

// readRawResponse reads the next JSON line from the worker's stdout.
func (p *ChromeImageProvider) readRawResponse() (map[string]any, error) {
	if !p.stdout.Scan() {
		err := p.stdout.Err()
		if err == nil {
			err = fmt.Errorf("worker stdout closed unexpectedly (process may have exited)")
		}
		// Try to collect stderr if the worker crashed.
		if p.cmd != nil && p.cmd.ProcessState != nil {
			return nil, fmt.Errorf("%w (exit code: %d)", err, p.cmd.ProcessState.ExitCode())
		}
		return nil, err
	}
	line := p.stdout.Text()
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON from worker: %w (line=%s)", err, line)
	}
	return raw, nil
}

// workerResponse is the JSON shape the Python worker writes to stdout.
//
// PR-CHROME-PROVIDER-SPLIT (2026-07-04): the `Profile *int` field is
// RETAINED for forward-compat (the Python worker still emits it in
// single-profile mode with profile=0); the Go-side cooldowns tracking
// that used this field is REMOVED (see chrome_provider.go::Generate).
//
// P0+P1 integration (July 2026): the response surface is extended with
// Code, Method, NaturalW, NaturalH, Complete for richer observability:
//   - Code: typed error code (e.g. 'ErrNoImageCandidate',
//     'ErrBlankOrPlaceholder', 'ErrGenerationTimeout').
//   - Method: which extraction path produced the bytes
//     ('googleusercontent' / 'blob-fetch' / 'element-screenshot').
//   - NaturalW/H: real pixel dimensions of the extracted image;
//     chrome_provider.Generate uses these for ratio verification
//     against req.Width/Height (forward-pointer for "ratio 16:9
//     verificato nei metadati dell'immagine" user spec).
//   - Complete: boolean mirror of `image.complete` from the DOM
//     extraction path — redundant with pHash but useful for log triage.
//
// P2 integration (July 2026): the response is further extended with
// full diagnostic replication so the Go-side job log has parity with
// the worker's per-phase JSONL output:
//   - CandidatesBaseline / CandidatesAfter: the count of google-
//     usercontent/blob images BEFORE the user clicks "create" and
//     AFTER polling completes (delta = n new candidates generated).
//   - Candidates: per-candidate src + naturalWidth/Height + complete
//     flag. The list is bounded to 8 entries to keep the response
//     line under ~2KB; the meta-counters AboveReport whether more
//     candidates existed.
//   - PhashHex, WhitePct, Variance, EdgeDensity: PIL pixel stats
//     computed in the worker (PIL pass on the saved PNG). The Go
//     side ALSO computes the same stats via visual_validate.ComputeStats
//     for redundancy + cross-validation; the worker's numbers are
//     the canonical primary source for the Zap log replication.
//   - ImageModeActive: whether the Immagine/Image tab is selected
//     (TRUE = image-generation mode; FALSE = text-only or layout mode).
//   - RatioSelected: the ratio option the worker actually selected
//     on the Chromium side ('16:9', '4:3', '1:1', or 'unset').
//   - PromptOriginal: the prompt as received from Go (used to detect
//     worker-side mutation, e.g. truncation > MAX_PROMPT_LEN).
//   - PromptDOM: the prompt actually filled into the textarea (post-
//     cleanup, post-150-char truncation). Operators can compare the
//     two to detect regressions in the cleanup helper.
//   - ScreenshotPath: on ERROR or fallback, the path to the saved
//     page screenshot. Empty on success.
//
// None of these omitempty:empty fields break existing Go callers; the
// pre-P2 fields are unchanged. New fields are all optional, so the
// Python worker can ship them incrementally.
type workerResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	Code      string `json:"code,omitempty"`
	Method    string `json:"method,omitempty"`
	NaturalW  int    `json:"natural_w,omitempty"`
	NaturalH  int    `json:"natural_h,omitempty"`
	Complete  bool   `json:"complete,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms,omitempty"`
	Bytes     int    `json:"bytes,omitempty"`
	Profile   *int   `json:"profile,omitempty"`

	// P2 diagnostic replication fields (July 2026).
	CandidatesBaseline int         `json:"candidates_baseline,omitempty"`
	CandidatesAfter    int         `json:"candidates_after,omitempty"`
	Candidates         []Candidate `json:"candidates,omitempty"`
	PhashHex           string      `json:"phash_hex,omitempty"`
	WhitePct           float64     `json:"white_pct,omitempty"`
	Variance           float64     `json:"variance,omitempty"`
	EdgeDensity        float64     `json:"edge_density,omitempty"`
	ImageModeActive    bool        `json:"image_mode_active,omitempty"`
	RatioSelected      string      `json:"ratio_selected,omitempty"`
	PromptOriginal     string      `json:"prompt_original,omitempty"`
	PromptDOM          string      `json:"prompt_dom,omitempty"`
	ScreenshotPath     string      `json:"screenshot_path,omitempty"`
}

// Candidate is the per-img entry the Python worker emits alongside the
// diagnostic counters. naturalWidth/Height/complete are 1:1 mirrors of
// the DOM <img> properties; Src is the URL the worker tried to fetch.
type Candidate struct {
	Src      string `json:"src"`
	NaturalW int    `json:"natural_w"`
	NaturalH int    `json:"natural_h"`
	Complete bool   `json:"complete"`
}

// mapToStruct round-trips a map through JSON to populate a struct.
func mapToStruct(m map[string]any, v any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
