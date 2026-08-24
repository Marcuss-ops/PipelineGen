// cmd/admin/qdrant_preflight.go — Qdrant preflight smoke runner.
//
// Subcommand registered in cmd/admin/main.go as `qdrant-preflight`.
// Runs the 11 PR-QDRANT-PREFLIGHT-TEST-* assertions against a running
// production stack (server :8081 + qdrant :6333). Exit code 0 only
// when ALL 11 PASS (with Test 9 SKIP-allowed under the chaos-day
// scheduling forward-pointer).
//
// godlike/06 SSOT: the canonical test registry (var AllTests below) is
// the SOLE source of truth for the 11-test list; mirrored verbatim in
// architecture/current.yaml#PR-QDRANT-FULL-STACK-AUTOMATED.notes.
//
// godlike/07 NO-FAKE-AVAILABILITY: each test emits a typed outcome
// (PASS / FAIL / SKIP). SKIPs are surfaced honestly rather than
// masquerading as PASS. Test 9 (chaos-day scheduling) is SKIP-allowed
// because it requires manual teardown of the Qdrant container
// (godlike/07 honest-limitation disclosure).
//
// godlike/07 minimum-blast-radius: tests run SEQUENTIALLY with per-test
// context.WithTimeout so a stalled test cannot pin downstream tests
// in awaiting state.
//
// Forward-pointers (godlike/07):
//   - Tests 3-8, 10, 11 currently FAIL (return "stack not ready" or
//     similar) when the production stack (PR-QDRANT-FULL-STACK-BRINGUP)
//     or seed CLI (PR-QDRANT-PREFLIGHT-DATA-SEED) is not available.
//     This is the correct CI-gate behavior: FAIL-LOUD when prerequisites
//     are unmet. Per-test fill-in lands in per-PR follow-ups as each
//     test individually gates green.
//   - Each test's Fn returns a typed error wrapping one of:
//     ErrPreflightStackDown / ErrPreflightSeedMissing / ErrPreflightNotImplemented
//     so the runner report can distinguish failure modes at a glance.

package qdrant

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
)

// PreflightTest is the canonical typed shape for each of the 11
// PR-QDRANT-PREFLIGHT-TEST-* assertions.
type PreflightTest struct {
	Name       string        // human-readable test name
	ID         string        // canonical linked_issue id
	Doc        string        // 1-line documentation
	Timeout    time.Duration // per-test context.WithTimeout bound
	Fn         func(ctx context.Context, deps *preflightDeps) error
	Skippable  bool   // allowed to emit OutcomeSKIP without failing the suite
	SkipReason string // human-readable SKIP reason
}

// Outcome is the per-test typed result (godlike/07 typed-error contract).
type Outcome string

const (
	OutcomePASS Outcome = "PASS"
	OutcomeFAIL Outcome = "FAIL"
	OutcomeSKIP Outcome = "SKIP"
)

// Typed sentinel errors (godlike/07 NO-FAKE-AVAILABILITY).
var (
	ErrPreflightStackDown      = fmt.Errorf("preflight: stack not ready (server :8081 + qdrant :6333 unreachable)")
	ErrPreflightSeedMissing    = fmt.Errorf("preflight: seed asset missing (PR-QDRANT-PREFLIGHT-DATA-SEED not shipped)")
	ErrPreflightNotImplemented = fmt.Errorf("preflight: not yet implemented (forward-pointer to per-test PR)")
)

// preflightDeps carries the resolved CLI flags + base HTTP client.
type preflightDeps struct {
	URL         string
	QdrantURL   string
	Collection  string
	AdminToken  string
	WorkerToken string
	HTTPClient  *http.Client
	Log         *zap.Logger
	// SeedAssetID is populated by Test 3 (asset ingested before
	// downstream Tests 4-7 read it). Consumed by Tests 4-8, 10, 11.
	SeedAssetID string
	SeedJobID   string
	// SeedVOAssetID is populated by Test 11 (voiceover piggy-back).
	// Forward-pointee for future voiceover-flow Tests.
	SeedVOAssetID string
}

// AllTests is the canonical registry of 11 PR-QDRANT-PREFLIGHT-TEST-*
// assertions. SEQUENTIAL execution order is enforced by slice order
// (NOT map iteration). godlike/06 SSOT: this slice IS the source of
// truth for what preflight verifies. Mirror block MUST land in
// architecture/current.yaml#PR-QDRANT-FULL-STACK-AUTOMATED.notes
// per the lockstep discipline.
var AllTests = []PreflightTest{
	{
		Name:    "qdrant-stack-healthy",
		ID:      "PR-QDRANT-PREFLIGHT-TEST-1",
		Doc:     "verify qdrant /healthz returns 200 (test-qdrant stand-up canonical surface)",
		Timeout: 10 * time.Second,
		Fn:      testQdrantStackHealthy,
	},
	{
		Name:    "schema-v3-shipped",
		ID:      "PR-QDRANT-PREFLIGHT-TEST-2",
		Doc:     "verify /collections returns media_assets_v3_e5_768_siglip_768 (post PR-QDRANT-PREFLIGHT-SCHEMA-V3-SHIPPED)",
		Timeout: 10 * time.Second,
		Fn:      testSchemaV3Shipped,
	},
	{
		Name:    "outbox-events-created",
		ID:      "PR-QDRANT-PREFLIGHT-TEST-3",
		Doc:     "submit asset, verify outbox_events row created (event_type='asset.index.requested')",
		Timeout: 30 * time.Second,
		Fn:      testOutboxEventsCreated,
	},
	{
		Name:    "outbox-events-completed",
		ID:      "PR-QDRANT-PREFLIGHT-TEST-4",
		Doc:     "wait, verify same outbox_events row reaches status='completed'",
		Timeout: 90 * time.Second,
		Fn:      testOutboxEventsCompleted,
	},
	{
		Name:    "media-assets-index-state-indexed",
		ID:      "PR-QDRANT-PREFLIGHT-TEST-5",
		Doc:     "verify media_assets.index_state='INDEXED' AND lifecycle_state='ACTIVE'",
		Timeout: 30 * time.Second,
		Fn:      testMediaAssetsIndexStateIndexed,
	},
	{
		Name:    "qdrant-scroll-finds-asset",
		ID:      "PR-QDRANT-PREFLIGHT-TEST-6",
		Doc:     "POST /points/scroll filter=asset_id returns >0 hits",
		Timeout: 30 * time.Second,
		Fn:      testQdrantScrollFindsAsset,
	},
	{
		Name:    "hybrid-search-score-gt-half",
		ID:      "PR-QDRANT-PREFLIGHT-TEST-7",
		Doc:     "GET /internal/v1/media/search returns score >= 0.5",
		Timeout: 30 * time.Second,
		Fn:      testHybridSearchScore,
	},
	{
		Name:    "supersede-gate-2-source-versions",
		ID:      "PR-QDRANT-PREFLIGHT-TEST-8",
		Doc:     "verify 2 outbox events with same aggregate_id supersede correctly (different source_version)",
		Timeout: 60 * time.Second,
		Fn:      testSupersedeGate,
	},
	{
		Name:       "chaos-day-scheduling",
		ID:         "PR-QDRANT-PREFLIGHT-TEST-9-RETRY-RECOVERY",
		Doc:        "chaos-day retry-recovery scheduling (already shipped as scheduling entry SHA 17df7fb3)",
		Timeout:    5 * time.Second,
		Fn:         testChaosDayScheduling,
		Skippable:  true,
		SkipReason: "PR-QDRANT-PREFLIGHT-TEST-9 requires manual Qdrant teardown (godlike/07 honest-limitation); SKIP-allowed",
	},
	{
		Name:    "delete-tombstone",
		ID:      "PR-QDRANT-PREFLIGHT-TEST-10-DELETE-TOMBSTONE",
		Doc:     "DELETE sandbox asset -> lifecycle_state=DELETED + Qdrant point removed (404 on points/<id>)",
		Timeout: 30 * time.Second,
		Fn:      testDeleteTombstone,
	},
	{
		Name:    "voiceover-piggyback",
		ID:      "PR-QDRANT-PREFLIGHT-TEST-11-VOICEOVER",
		Doc:     "voiceover.generate ~3 min -> outbox emit + Qdrant scroll finds vo asset (5-stage pipeline)",
		Timeout: 5 * time.Minute,
		Fn:      testVoiceoverPiggyback,
	},
}

// runQdrantPreflight is the cmd/admin/main.go entry point registered
// in the switch dispatcher. Not the same surface as node-level
// cmd/preflight/* (we chose the admin CLI subcommand pattern per
// architecture-action-plan §5's dispatcher-shape alignment).
func RunQdrantPreflight(args []string) error {
	fs := flag.NewFlagSet("qdrant-preflight", flag.ExitOnError)
	urlFlag := fs.String("url", "http://127.0.0.1:8081", "PipelineGen server URL")
	qdrantFlag := fs.String("qdrant-url", "http://127.0.0.1:6333", "Qdrant base URL")
	collectionFlag := fs.String("collection", "media_assets_v3_e5_768_siglip_768", "Qdrant canonical collection name (alias resolved)")
	tokenFlag := fs.String("admin-token", "", "Admin token (or set VELOX_ADMIN_TOKEN env var)")
	workerTokenFlag := fs.String("worker-token", "", "Worker token (or set VELOX_WORKER_TOKEN env var); required by PR-B for /internal/v1/* routes — see godlike/06 §PR-B")
	listFlag := fs.Bool("list", false, "Print all 11 TDD tests + exit 0 (diagnostic-only; no stack interaction)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *listFlag {
		fmt.Println("Qdrant Preflight Suite - 11 TDD tests (canonical godlike/06 SSOT registry):")
		for i, t := range AllTests {
			fmt.Printf("  [%2d/11] %-32s  id=%s\n", i+1, t.Name, t.ID)
			fmt.Printf("           timeout=%v\n", t.Timeout)
			if t.Skippable {
				fmt.Printf("           SKIP-allowed: %s\n", t.SkipReason)
			} else if strings.Contains(t.Doc, "forward") || isStubTest(t.Name) {
				fmt.Printf("           [forward-pointer: per-test PR]\n")
			}
		}
		return nil
	}

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("VELOX_ADMIN_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("qdrant-preflight: admin token required (--admin-token=... or set VELOX_ADMIN_TOKEN env)")
	}

	workerToken := *workerTokenFlag
	if workerToken == "" {
		workerToken = os.Getenv("VELOX_WORKER_TOKEN")
	}
	if workerToken == "" {
		return fmt.Errorf("qdrant-preflight: worker token required (--worker-token=... or set VELOX_WORKER_TOKEN env) — PR-B defense-in-depth (Wave 22) requires a worker token for /internal/v1/* routes; admin tokens are rejected at the middleware with 401")
	}

	// Phase A fix: cli.AppLogger() returns 4 values (cfg, *zap.Logger, cleanup, error).
	// Preflight discards cfg (no command needs it) and uses the typed cleanup
	// callback deferred at exit. Staticcheck: cmd/admin/logger.go:30 documents
	// cleanup is safe to call multiple times so an extra defer is harmless.
	_, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return fmt.Errorf("qdrant-preflight: init logger: %w", err)
	}
	defer cleanup()

	deps := &preflightDeps{
		URL:         *urlFlag,
		QdrantURL:   *qdrantFlag,
		Collection:  *collectionFlag,
		AdminToken:  token,
		WorkerToken: workerToken,
		HTTPClient:  &http.Client{}, // per-test context.WithTimeout is the only deadline (see runQdrantPreflight); a static 30s Client.Timeout would preempt Tests 4/8/11 which carry 90s/60s/5m ctx budgets
		Log:         log,
	}

	log.Info("qdrant-preflight: starting",
		zap.Int("tests", len(AllTests)),
		zap.String("url", deps.URL),
		zap.String("qdrant_url", deps.QdrantURL),
	)

	type result struct {
		Test    PreflightTest
		Outcome Outcome
		Err     error
	}
	var results []result
	failCount, skipCount, passCount := 0, 0, 0
	for i, t := range AllTests {
		log.Info("running test",
			zap.Int("idx", i+1),
			zap.String("name", t.Name),
			zap.String("id", t.ID),
			zap.Duration("timeout", t.Timeout),
		)
		ctx, cancel := context.WithTimeout(cli.CmdContext(), t.Timeout)
		err := t.Fn(ctx, deps)
		cancel()
		var oc Outcome
		switch {
		case err == nil:
			oc, err = OutcomePASS, nil
		case t.Skippable:
			// SKIP-allowed: emit SKIP with reason; never FAIL the suite.
			oc = OutcomeSKIP
			skipCount++
		case isSkipErr(err):
			// Skip-prefixed error: surfacing a SKIP voluntarily.
			oc = OutcomeSKIP
			skipCount++
		default:
			oc = OutcomeFAIL
			failCount++
		}
		if oc == OutcomePASS {
			passCount++
		}
		results = append(results, result{Test: t, Outcome: oc, Err: err})
		fmt.Printf("  [%2d/11] %-32s  %s  %s\n", i+1, t.Name, oc, errOrSkipReason(t, oc, err))
	}

	// Final report + exit-code decision
	fmt.Println()
	fmt.Println("Qdrant Preflight Summary:")
	fmt.Printf("  PASS: %d   SKIP: %d   FAIL: %d   Total: %d\n", passCount, skipCount, failCount, len(results))

	// godlike/07: exit 0 iff zero FAIL. SKIPs allowed through (Test 9 SKIP-allowed).
	if failCount > 0 {
		return fmt.Errorf("qdrant-preflight: %d test(s) failed (see above); gate FAIL-closed per godlike/07 NO-FAKE-AVAILABILITY", failCount)
	}
	log.Info("qdrant-preflight: all tests PASS (or allowed SKIP)",
		zap.Int("pass", passCount),
		zap.Int("skip", skipCount),
	)
	return nil
}

// isSkipErr returns true if the test voluntarily emitted a SKIP via
// the canonical "skip: " or "skipped: " prefix convention, OR if the
// error IS one of the canonical typed sentinels that gate SKIPs.
func isSkipErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "skip: ") || strings.HasPrefix(msg, "skipped: ") {
		return true
	}
	return false
}

func errOrSkipReason(t PreflightTest, oc Outcome, err error) string {
	if oc == OutcomePASS {
		return "OK"
	}
	if oc == OutcomeSKIP {
		if err != nil && err.Error() != "" {
			return err.Error()
		}
		return t.SkipReason
	}
	return err.Error()
}

// isStubTest identifies tests that are typed stubs awaiting per-test
// follow-up PRs (Tests 3-8, 10, 11 in the current ship state).
func isStubTest(name string) bool {
	stubs := map[string]bool{
		"outbox-events-created":            true,
		"outbox-events-completed":          true,
		"media-assets-index-state-indexed": true,
		"qdrant-scroll-finds-asset":        true,
		"hybrid-search-score-gt-half":      true,
		"supersede-gate-2-source-versions": true,
		"delete-tombstone":                 true,
		"voiceover-piggyback":              true,
	}
	return stubs[name]
}

// Tests 1-2 — extracted to qdrant_preflight_stack.go (PR-PREFLIGHT-STACK-SPLIT).
// Tests 3-8, 10, 11 — extracted to qdrant_preflight_stubs.go
// (PR-PREFLIGHT-SPLIT, July 2026).
// testChaosDayScheduling (Test 9) — extracted to qdrant_preflight_stubs.go
// (PR-PREFLIGHT-SPLIT, July 2026).
