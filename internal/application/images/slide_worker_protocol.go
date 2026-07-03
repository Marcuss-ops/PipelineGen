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
package images

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
type workerResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms,omitempty"`
	Bytes     int    `json:"bytes,omitempty"`
	Profile   *int   `json:"profile,omitempty"`
}

// mapToStruct round-trips a map through JSON to populate a struct.
func mapToStruct(m map[string]any, v any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
