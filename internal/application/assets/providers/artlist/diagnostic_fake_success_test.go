// Package artlist — diagnostic_fake_success_test.go (2026-07-04)
//
// ─── Diagnostic ARTIFACT for a godlike/07 fake-success anti-pattern ────────
//
// Observed live on a running PipelineGen server (port 8000):
//
//	POST /api/artlist/run
//	  body: {"term":"ocean","limit":1,"strategy":"default","dry_run":false}
//	  auth: Bearer $ADMIN_TOKEN
//
//	→ handler response: run_id="job_…", status=SUCCEEDED, processed=1, failed=0  ✓
//	→ jobs        table : row present, type="media.artlist", status=SUCCEEDED  ✓
//	→ artlist_runs table: 0 rows  (no per-run aggregate was ever written)      ✗
//	→ media_assets WHERE provider='artlist': 0 rows                            ✗
//	→ media_assets search for clip 489828 on (artlist_clip_id,
//	                                         source_id, source_ref,
//	                                         external_id, ref_id, clip_id): 0    ✗
//	→ GET /api/artlist/stats : 92 → 92 unchanged                                ✗
//
// ─── Closed verdict (thinker-with-files-gemini, 2026-07-04) ──────────────────
//
//  1. internal/application/assets/providers/artlist/run_orchestrator_stages.go:287
//     `ps.resp.Processed++` increments UNCONDITIONALLY during stageProcessBatch
//     (in-memory counter, no DB verification).
//
//  2. internal/application/assets/providers/artlist/run_orchestrator_stages.go:312
//     `if existingClip == nil { … continue }` silently no-ops the persist step
//     whenever the clip is not already in DB. Logged at Debug level, not Warn.
//
//  3. internal/application/assets/providers/artlist/job_core.go:281
//     `return jobCodec.ResultFromResponse(resp), nil` exits HandleJob without
//     invoking any ClipsRepository.Insert / ClipsRepository.Persist call.
//     The finalizer.markSucceeded writes ONLY `jobs.status=SUCCEEDED` +
//     `job_events(job_completed)` — never `media_assets`, never `artlist_runs`.
//
//  4. NO writer for the `artlist_runs` aggregate table exists in this code
//     path. The table is read by the Stats handler (returning the 92 legacy
//     rows from before the FASE-6 cutover) but never updated.
//
// ─── Reproduction affordance ─────────────────────────────────────────────────
//
// The single test below is SKIP-by-default. It activates ONLY when the env var
// VELOX_DIAGNOSTIC=1 is set, then runs live source-line probes against the
// recorded bug substrings.
//
//   - Default (CI): t.Skip → ZERO behavior change, CI stays green.
//   - Opt-in (`VELOX_DIAGNOSTIC=1`): the test reads each recorded source file
//     from the package directory and asserts the bug substring is still
//     present. Each probe logs the meaning for the operator's benefit.
//
// ─── Closure condition ──────────────────────────────────────────────────────
//
// When the fail-closed gate + the artlist_runs aggregate writer land, the
// recorded substrings will DISAPPEAR from the source. The probes will FAIL —
// that is the operator's signal to RETIRE this file entirely alongside the
// closure mirror entry in `architecture/current.yaml`.
//
//	Forward-pointer: `architecture/current.yaml#ARTLIST-PERSIST-FIX-2026-07-04`
//
// godlike/07 minimum-blast-radius: this file ships OFF by default and adds
// zero new production code. Diagnostics are the only surface it owns.
package artlist

import (
	"os"
	"strings"
	"testing"
)

// envDiagnosticGate is the activation flag for the live diagnostic path.
// OFF by default so CI is unaffected. Opt in with:
//
//	VELOX_DIAGNOSTIC=1 go test ./internal/application/assets/providers/artlist/ -run TestDiagnostic_
//
// This is intentional godlike/07 minimum-blast-radius: the artifact
// documents the gap without breaking healthy CI runs.
const envDiagnosticGate = "VELOX_DIAGNOSTIC"

// TestDiagnostic_FakeSuccess_RecordedBugPatterns runs ONLY with
// VELOX_DIAGNOSTIC=1. Default SKIP keeps CI green.
//
// When activated, it probes the source files in the package directory
// for the canonical bug substrings recorded by the 2026-07-04 diagnosis.
// Each probe that finds its substring logs a "STILL PRESENT" line; the
// test PASSES today (bug present) and will FAIL when the gate lands
// (substring absent) — the FAIL is the operator's signal to retire this
// diagnostic artifact per the closure path documented in the file godoc.
//
// godlike/06 SSOT: the substrings below MUST match the current source.
// Hang the test if any of the 3 files moves out of the package dir.
func TestDiagnostic_FakeSuccess_RecordedBugPatterns(t *testing.T) {
	if os.Getenv(envDiagnosticGate) != "1" {
		t.Skipf(
			"diagnostic artifact %s: set %s=1 to enable (proves fake-success gap, OFF by default — godlike/07 minimum-blast-radius)",
			"diagnostic_fake_success_test.go", envDiagnosticGate,
		)
	}

	type probe struct {
		file    string // filename only (same package dir as this test file)
		lookFor string
		meaning string
	}

	probes := []probe{
		{
			file:    "run_orchestrator_stages.go",
			lookFor: "ps.resp.Processed++",
			meaning: "Processed++ is unconditional during stageProcessBatch — no DB-side counter verify",
		},
		{
			file:    "run_orchestrator_stages.go",
			lookFor: "if existingClip == nil",
			meaning: "stagePersistResults silently no-ops (continue) when the clip is missing from DB",
		},
		{
			file:    "job_core.go",
			lookFor: "jobCodec.ResultFromResponse(resp)",
			meaning: "HandleJob exits via ResultFromResponse without invoking ClipsRepository.Insert or any artlist_runs writer",
		},
	}

	for _, p := range probes {
		data, err := os.ReadFile(p.file)
		if err != nil {
			t.Errorf("[%s] cannot read source %q: %v — diagnostic surface broken (file moved or renamed?)", p.file, p.file, err)
			continue
		}
		body := string(data)
		if !strings.Contains(body, p.lookFor) {
			// FAIL = the closure has landed. Operator signal: retire this
			// artifact alongside the closure mirror entry in
			// architecture/current.yaml#ARTLIST-PERSIST-FIX-2026-07-04.
			t.Errorf(
				"[%s] substring %q NOT present — fake-success closure appears to LAND. RETIRE this diagnostic artifact. Context: %s",
				p.file, p.lookFor, p.meaning,
			)
			continue
		}
		// Maybe count the occurrences to give the operator a hint that
		// the bug pattern is more pervasive than recorded.
		count := strings.Count(body, p.lookFor)
		t.Logf("[%s] bug pattern STILL PRESENT (%d occurrence(s)): %s — %s",
			p.file, count, p.lookFor, p.meaning,
		)
	}
}
