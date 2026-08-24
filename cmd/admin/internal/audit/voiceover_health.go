package audit

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"time"
)

// ttsHealthResult is the JSON shape printed by `admin voiceover-health`.
// All fields are always emitted (omitempty only for Error) so a missing
// key surfaces in operator parsing (godlike/07 NO-FAKE-AVAILABILITY).
type ttsHealthResult struct {
	Reachable    bool   `json:"reachable"`
	URL          string `json:"url"`
	StatusCode   int    `json:"status_code"`
	LatencyMs    int64  `json:"latency_ms"`
	Error        string `json:"error,omitempty"`
	ProbeTimeISO string `json:"probe_time_iso"`
}

// runVoiceoverHealth dispatches the `admin voiceover-health` subcommand.
//
// CLI probe for the persistent TTS worker (audioasset.Processor at
// internal/infrastructure/audio::worker_health.go). The canonical
// instance lives inside the running pipelinegen process and is not
// reachable from outside this binary; we probe the worker's HTTP /health	// surface directly (the canonical public surface).
//
// Default exit code is 0; the printable Result.Reachable conveys the
// verdict. Operators parse JSON with `jq '.reachable'` to wire this into
// a CI gate without a custom exit-code per probe. godlike/07
// NO-FAKE-AVAILABILITY: failure to reach the worker surfaces via
// Reachable=false + populated Error, never a blank verdict.
//
// The --fail-on-unreachable opt-in flag flips the exit code to non-zero
// when the worker is unreachable, giving CI gates a binary signal
// without forking the JSON envelope. godlike/07 minimum-blast-radius:
// the flag is OFF by default so existing operator scripts see byte-stable
// exit semantics.
//
// Step[8] / Fail-mode 3 (worker off): the canonical scenario where
// `voiceover.generate_item` children stay in RETRY_WAIT (Steps [4]+[6]+[7]).
// This probe lets an operator or CI runner detect the worker-down
// condition WITHOUT a full voiceover POST loop (which would also
// consume the rate limit — see Commit 2's VELOX_VOICEOVER_RATE_LIMIT_BURST
// bypass).
func RunVoiceoverHealth(args []string) error {
	fs := flag.NewFlagSet("voiceover-health", flag.ExitOnError)
	baseURL := fs.String("base-url", "http://127.0.0.1:8089", "Base URL for the persistent TTS worker (http://host:port prefix; the probe appends /health)")
	failOnUnreachable := fs.Bool("fail-on-unreachable", false, "Return non-zero exit code when the TTS worker is not reachable (CI gate use)")
	_ = fs.Parse(args)

	res := probeTTSHealth(cli.CmdContext(), *baseURL)
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	fmt.Println(string(out))
	if *failOnUnreachable && !res.Reachable {
		return fmt.Errorf("TTS worker unreachable: %s (status=%d, error=%q)", res.URL, res.StatusCode, res.Error)
	}
	return nil
}

// probeTTSHealth is the pure helper that runs the HTTP probe. Exposed at
// package scope (lowercase) so cmd/admin/voiceover_health_test.go can
// exercise it directly via httptest without re-implementing the JSON
// envelope or the timeout wiring. Total wall-clock budget is 5s;
// status-code 200 marks reachable.
func probeTTSHealth(ctx context.Context, baseURL string) ttsHealthResult {
	start := time.Now()
	url := baseURL + "/health"
	res := ttsHealthResult{
		URL:          url,
		ProbeTimeISO: start.Format(time.RFC3339),
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	resp, err := http.DefaultClient.Do(req)
	res.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	res.StatusCode = resp.StatusCode
	if resp.StatusCode == http.StatusOK {
		res.Reachable = true
	} else {
		res.Error = fmt.Sprintf("unexpected status: %d", resp.StatusCode)
	}

	return res
}
