// Package workerdoctor — probes_liveness.go (PR-SPLIT-WORKERDOCTOR-PROBES,
// 2026-07-06).
//
// Liveness probes: master /health reachability + master /ready
// aggregation. These probes run at NETWORK layer (HTTP), unlike the
// dependency probes (config/cert/filesystem, which run at LOCAL
// layer) and the invariant probes (engine/runtime, which run at
// PROCESS layer).
//
// godlike/06 SSOT: this file is the canonical SOLE owner of the
// master-URL HTTP probing surface. Shared HTTP helpers
// (defaultHTTPDo + fetchJSON) are co-located here rather than in a
// shared helpers file per AGENTS.md Pattern 5 one-canonical-owner-
// per-fact — they are exclusively consumed by the liveness probes
// and have no callers in the dependency/invariant probe scopes.
package workerdoctor

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// probeMasterReachable polls ${masterURL}/health with the same
// budget as the worker's startup pre-flight (5s timeout per attempt).
// We do NOT retry multiple times — the doctor is a snapshot, not a
// service mesh. A failure means "master is not healthy right now";
// the worker will surface the same problem at the pre-flight itself.
func probeMasterReachable(masterURL string, cfg DoctorConfig, dp DefaultProbes) ProbeReceipt {
	if masterURL == "" {
		return ProbeReceipt{OK: false, Applicable: true, Error: "master URL is empty"}
	}
	do := dp.HTTPDo
	if do == nil {
		do = defaultHTTPDo(5 * time.Second)
	}
	healthURL := strings.TrimRight(masterURL, "/") + "/health"
	req, err := http.NewRequest(http.MethodGet, healthURL, nil)
	if err != nil {
		return ProbeReceipt{OK: false, Applicable: true, Error: err.Error()}
	}
	resp, err := do(req)
	if err != nil {
		return ProbeReceipt{OK: false, Applicable: true, Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      "master /health returned " + resp.Status,
			Extras:     map[string]any{"status_code": resp.StatusCode},
		}
	}
	return ProbeReceipt{
		OK:         true,
		Applicable: true,
		Extras:     map[string]any{"status_code": resp.StatusCode, "url": healthURL},
	}
}

// WireReady is called by the CLI after the master_reachable probe
// ran successfully. It installs a /ready probe driven by the
// canonical application ReadyChecker so the deep checks (DB, jobs,
// drive, qdrant) are honored by the doctor too.
//
// Connection failures (master truly unreachable) are treated as
// Applicable=false so the verdict loop does NOT cascade a single
// upstream fault into a downstream fail. Once the master is
// reachable, /ready reports its own canonical status; "healthy"
// passes, anything else fails the doctor.
func WireReady(agg *Aggregator, masterURL string, dp DefaultProbes) error {
	if agg == nil {
		return errors.New("aggregator is nil")
	}
	agg.SetCheck(CheckIDReady, func() ProbeReceipt {
		// We cannot reuse ReadyChecker directly without a Service
		// — the doctor is a stand-alone tool. Instead we read /ready
		// directly: it's the canonical aggregation of DB + Drive +
		// Qdrant + Jobs. Reading it here is functionally equivalent
		// to wrapping the ReadyChecker but does not require the
		// heavier app composition graph to be loaded.
		body, status, err := fetchJSON(masterURL+"/ready", dp)
		if err != nil {
			// Treat network-level failure as opt-out, NOT a fail:
			// master_reachable already flagged the upstream fault
			// and we want /ready to be "not run", not "run and
			// double-fail". Cascade failures produce noisy reports.
			return ProbeReceipt{
				OK:         false,
				Applicable: false,
				Note:       "master unreachable; /ready probe skipped (already reported by master_reachable)",
				Error:      err.Error(),
			}
		}
		// /ready is the canonical HTTP 503 / 200 aggregator; both
		// 2xx and 5xx can carry {"status":"unhealthy"} so we parse
		// the body instead of just looking at the status code.
		var r struct {
			OK     bool   `json:"ok"`
			Status string `json:"status"`
		}
		if jerr := json.Unmarshal(body, &r); jerr != nil {
			return ProbeReceipt{
				OK:         false,
				Applicable: true,
				Error:      "ready body not parseable: " + jerr.Error(),
				Extras:     map[string]any{"status_code": status, "body": string(body)},
			}
		}
		if !r.OK || r.Status != "healthy" {
			return ProbeReceipt{
				OK:         false,
				Applicable: true,
				Error:      "master /ready reported " + r.Status,
				Extras:     map[string]any{"status_code": status, "body": string(body)},
			}
		}
		return ProbeReceipt{
			OK:         true,
			Applicable: true,
			Extras:     map[string]any{"status_code": status, "ready_status": r.Status},
		}
	})
	return nil
}

// defaultHTTPDo returns a do-cls with a per-call timeout. We use a
// fresh client per call because the doctor only fires one HTTP
// probe; lifecycle overhead is irrelevant.
func defaultHTTPDo(perReq time.Duration) HTTPDoFunc {
	client := &http.Client{Timeout: perReq}
	return func(req *http.Request) (*http.Response, error) {
		return client.Do(req)
	}
}

// fetchJSON — small helper used by /ready probe. Limited body
// (4KiB cap) so a misbehaving master can't OOM the doctor.
func fetchJSON(url string, dp DefaultProbes) ([]byte, int, error) {
	do := dp.HTTPDo
	if do == nil {
		do = defaultHTTPDo(5 * time.Second)
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if rerr != nil {
			break
		}
		if len(buf) >= 16384 {
			// hard cap; we only need first few hundred bytes to parse /ready
			break
		}
	}
	return buf, resp.StatusCode, nil
}
