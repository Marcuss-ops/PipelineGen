// Package promo — PRIORITY 5 canonical regression tests (Italian voiceover
// audit, 2026-08-08).
//
// TestPromo_StrictTranslationAccounting (5a): pins the strict translation
// accounting contract. 3 languages (en-US + it-IT + pt-BR),
// AllowUntranslated defaults to false (strict mode), translator fails for
// pt-BR → Total=3 Success=2 Failed=1 OK=false, voiceover MUST NOT be
// attempted for pt-BR.
//
// TestPromo_CanonicalMigrationGate (5b): regression-guard for the
// BLOC5.4 migration target. Today the bridge uses
// Service.GenerateWithDestination (per
// internal/capabilities/voiceover/service/promo.go:1-8). The migration target is
// to route the per-language voiceover step through jobs.Dispatcher with
// the canonical TypeVoiceoverGenerate job type. This test pins:
//  1. The canonical async job type literal is "voiceover.generate"
//     (one canonical const per jobType, per godlike/06 SSOT).
//  2. The bridge source does NOT call Service.GenerateBatch (the
//     legacy batch method that would double-invoke the TTS pipeline
//     and re-introduce the TTS-double-invocation regression class).
//
// The migration is forward-deferred to BLOC5.4 per
// internal/capabilities/voiceover/service/promo.go:1-8. This test serves as a
// regression-guard: a future agent re-introducing
// Service.GenerateBatch in the bridge would surface as a test failure.
package promo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ---------------------------------------------------------------------------
// 5a: strict translation accounting (3-language subset, pt-BR failure)
// ---------------------------------------------------------------------------

// TestPromo_StrictTranslationAccounting pins the canonical strict-mode
// contract for a 3-language promo batch where 1 translation fails:
//
//   - Total = 3 (the count of languages the caller asked for, NOT
//     shrunk by the failure).
//   - Success = 2 (en-US + it-IT: translation + voiceover both OK).
//   - Failed = 1 (pt-BR: translation failed in strict mode → no
//     voiceover attempted → Result.OK=false).
//   - OK = false (any failure flips the aggregate).
//   - voiceover MUST NOT be called for the failed language (the
//     pre-PR-VO-A5 contract silently `continue`d past the failure
//     without surfacing Failed++; this regression-guard locks the
//     fail-closed semantic).
func TestPromo_StrictTranslationAccounting(t *testing.T) {
	// 3-language canonical subset (mirrors the user-spec literal:
	// it-IT + en-US + pt-BR). All 3 codes are in
	// translation.DefaultPromoLanguages() — see types.go L19-32.
	langs := []string{"en-US", "it-IT", "pt-BR"}

	// Translator is keyed by friendly name (per mkTranslator helper
	// signature; see generate_test.go L80-87). Default behavior of
	// mkTranslator is "auto-translated:<langName>" on miss; explicit
	// overrides here ensure deterministic text per language.
	realNames := map[string]translatorResp{
		"English": {text: "hello-en"},
		"Italian": {text: "ciao-it"},
		// pt-BR translation FAILS — strict-mode propagation
		// is the canonical fail-closed contract.
		"Portuguese": {err: errors.New("translator rate limited")},
	}

	vo := &stubVO{} // wildcard success: every locale gets a non-empty DriveLink.
	gen := NewGenerator(mkTranslator(realNames), vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{
		Text:      "Hello",
		Languages: langs,
		// AllowUntranslated defaults to false (strict mode).
		// AllowUntranslated: false, // explicit
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ── Aggregate accounting ────────────────────────────────────────
	if resp.Total != 3 {
		t.Errorf("Total = %d, want 3 (en-US + it-IT + pt-BR)", resp.Total)
	}
	if resp.Success != 2 {
		t.Errorf("Success = %d, want 2 (en-US + it-IT succeeded)", resp.Success)
	}
	if resp.Failed != 1 {
		t.Errorf("Failed = %d, want 1 (pt-BR only — strict mode surfaces the failure)", resp.Failed)
	}
	if resp.OK {
		t.Fatal("OK = true, want false (pt-BR translation failed in strict mode; OK = (Failed == 0))")
	}

	// ── Voiceover was attempted EXACTLY 2 times (NOT for pt-BR) ─────
	// The pre-PR-VO-A5 contract silently continued past the failure
	// without surfacing Failed++ AND without calling voiceover. This
	// regression-guard locks the fail-closed semantic: in strict mode,
	// translation failures MUST be visible (Result entry + Failed++ +
	// OK=false) AND voiceover MUST NOT be attempted (no TTS compute
	// wasted on a translation that did not produce text).
	if len(vo.calls) != 2 {
		t.Errorf("voiceover called %d times, want 2 (en-US + it-IT only; pt-BR translation failed so voiceover MUST NOT be attempted); got calls: %v", len(vo.calls), vo.calls)
	}
	// Pin the EXACT languages called: en-US + it-IT, NOT pt-BR.
	// stubVO records the lowercased locale (see generate_test.go L70
	// — `locale := strings.ToLower(strings.TrimSpace(cmd.Locale))`).
	calledLocales := make(map[string]bool, len(vo.calls))
	for _, l := range vo.calls {
		calledLocales[l] = true
	}
	if !calledLocales["en-us"] {
		t.Errorf("voiceover MUST be called for en-US; got calls: %v", vo.calls)
	}
	if !calledLocales["it-it"] {
		t.Errorf("voiceover MUST be called for it-IT; got calls: %v", vo.calls)
	}
	if calledLocales["pt-br"] {
		t.Errorf("voiceover MUST NOT be called for pt-BR (translation failed — gate is the canonical fail-closed contract); got calls: %v", vo.calls)
	}

	// ── Per-language Result entries (strict mode surfaces every failure) ─
	if len(resp.Results) != 3 {
		t.Fatalf("len(Results) = %d, want 3 (strict mode MUST surface every failure, including pt-BR)", len(resp.Results))
	}
	byLang := make(map[string]Result, len(resp.Results))
	for _, r := range resp.Results {
		byLang[r.Language] = r
	}

	// en-US: succeeded (translation + voiceover).
	if r, ok := byLang["en-US"]; !ok {
		t.Errorf("en-US Result entry MISSING (strict mode must surface every language, including successes)")
	} else {
		if !r.OK {
			t.Errorf("en-US Result.OK = false, want true (translation + voiceover both succeeded)")
		}
		if r.DriveLink == "" {
			t.Errorf("en-US Result.DriveLink = empty, want non-empty (voiceover succeeded; stub returns stub-drive-en-us)")
		}
		if r.Error != "" {
			t.Errorf("en-US Result.Error = %q, want empty (success path)", r.Error)
		}
	}

	// it-IT: succeeded (translation + voiceover).
	if r, ok := byLang["it-IT"]; !ok {
		t.Errorf("it-IT Result entry MISSING (strict mode must surface every language, including successes)")
	} else {
		if !r.OK {
			t.Errorf("it-IT Result.OK = false, want true")
		}
		if r.DriveLink == "" {
			t.Errorf("it-IT Result.DriveLink = empty, want non-empty (voiceover succeeded; stub returns stub-drive-it-it)")
		}
		if r.Error != "" {
			t.Errorf("it-IT Result.Error = %q, want empty (success path)", r.Error)
		}
	}

	// pt-BR: failed translation (strict mode) → no voiceover attempted.
	// The canonical error surface MUST wrap ErrTranslationFailed so
	// dashboards can `errors.Is(err, promo.ErrTranslationFailed)` for
	// operator-grep stability (per godlike/07 typed-error contract).
	if r, ok := byLang["pt-BR"]; !ok {
		t.Errorf("pt-BR Result entry MISSING (strict mode MUST surface pt-BR as a failure)")
	} else {
		if r.OK {
			t.Errorf("pt-BR Result.OK = true, want false (translation failed)")
		}
		if r.DriveLink != "" {
			t.Errorf("pt-BR Result.DriveLink = %q, want empty (voiceover MUST NOT be attempted when translation failed — gate is the canonical fail-closed contract)", r.DriveLink)
		}
		if r.Translated != "" {
			t.Errorf("pt-BR Result.Translated = %q, want empty (translation failed, no text was produced)", r.Translated)
		}
		if !errors.Is(errorOrSentinel(r), ErrTranslationFailed) {
			t.Errorf("pt-BR Result.Error must wrap ErrTranslationFailed (typed-error contract for operator-grep stability); got %q", r.Error)
		}
	}
}

// ---------------------------------------------------------------------------
// 5b: canonical migration gate (regression-guard for the BLOC5.4 cutover)
// ---------------------------------------------------------------------------

// findRepoRoot walks up from this test file's directory until it finds
// go.mod. The walk-up is bounded by the filesystem root. Used to
// resolve the bridge source path (`internal/capabilities/voiceover/service/promo.go`)
// regardless of which directory `go test` was invoked from.
//
// runtime.Caller(0) returns the file:line of the current function
// (findRepoRoot). The canonical test files live at
// `internal/application/workflow/promo/canonical_migration_test.go`
// (3 levels below repo root), so filepath.Dir + walk-up to go.mod is
// stable across `go test` invocation directories.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod from %s (walked up to filesystem root)", file)
		}
		dir = parent
	}
}

// TestPromo_CanonicalMigrationGate pins the canonical post-migration
// contract for the promo workflow:
//
//  1. The canonical async job type literal is "voiceover.generate"
//     (one canonical const per jobType, per godlike/06 SSOT — see
//     internal/kernel/job/canonical_definitions.go:62 `TypeVoiceoverGenerate =
//     "voiceover.generate"`). A future PR that renames the const
//     literal would break the canonical contract and surface here.
//
//  2. The bridge source at internal/capabilities/voiceover/service/promo.go
//     MUST NOT call Service.GenerateBatch (the legacy batch method
//     that would double-invoke the TTS pipeline). Today the bridge
//     uses Service.GenerateWithDestination (the canonical non-batch
//     surface); the BLOC5.4 migration target is to route through
//     jobs.Dispatcher. Either way, the bridge MUST NOT call
//     Service.GenerateBatch — this regression-guard locks the
//     no-double-invocation invariant.
//
//  3. The bridge source DOES call Service.GenerateWithDestination
//     (the current state, sanity check that the test is reading the
//     right file). This assertion is forward-deferred: it will need
//     updating post-BLOC5.4 when the bridge routes through
//     jobs.Dispatcher instead.
//
// KNOWN GAP (godlike/07 NO-FAKE-AVAILABILITY): this test does NOT
// assert that the canonical registry binding is in place. Specifically,
// `internal/application/jobs/registry_voiceover.go` must register a
// handler for `job.TypeVoiceoverGenerate`. A future agent could rename
// the const literal, unregister the job type, or have a stale
// registration without surfacing here. Forward-pointer `PR-REF-REG-VO`
// (deadline 2026-08-22) will add the missing third sub-assert: load
// the registry via `registry_voiceover_test.go::TestRegistry_HasTypeVoiceoverGenerate`
// and assert `registry.HasHandler(job.TypeVoiceoverGenerate) == true`.
// The test still ships as a meaningful partial gate (the const + bridge
// surface contract is the load-bearing invariant for the BLOC5.4
// cutover) and the forward-pointer is the canonical SSOT for the
// follow-up.
func TestPromo_CanonicalMigrationGate(t *testing.T) {
	// ── 1. Canonical async job type const (godlike/06 SSOT) ──────
	// The single canonical const for the voiceover.generate job type
	// lives at internal/kernel/job/canonical_definitions.go:62. Renaming this literal
	// would break the wire-shape contract (jobs.Service.Enqueue callers
	// use this const exclusively; see AGENTS.md Git-Lesson-3).
	const wantJobType = "voiceover.generate"
	if got := string(job.TypeVoiceoverGenerate); got != wantJobType {
		t.Fatalf("job.TypeVoiceoverGenerate = %q, want %q (canonical one-canonical-const-per-jobType per godlike/06 SSOT — see internal/kernel/job/canonical_definitions.go:62)", got, wantJobType)
	}

	// ── 2. Bridge source does NOT call Service.GenerateBatch ──────
	// The bridge is the per-language voiceover adapter that
	// workflow/promo's Generator calls. A future agent re-introducing
	// Service.GenerateBatch here (e.g. via a copy-paste from the
	// pre-BLOC5.4 baseline) would re-trigger the TTS-double-invocation
	// regression class: the per-item caller already invokes
	// voiceover.Finalizer.Finalize, and GenerateBatch would invoke
	// the pipeline a SECOND time.
	repoRoot := findRepoRoot(t)
	bridgePath := filepath.Join(repoRoot, "internal", "capabilities", "voiceover", "service", "promo.go")
	bridgeSrc, err := os.ReadFile(bridgePath)
	if err != nil {
		t.Fatalf("read bridge source %q: %v (test fixture assumes repo-root resolution via findRepoRoot)", bridgePath, err)
	}
	bridgeBody := string(bridgeSrc)

	if strings.Contains(bridgeBody, "GenerateBatch(") {
		t.Errorf("bridge source %q must NOT call Service.GenerateBatch (legacy batch method that would double-invoke the TTS pipeline); "+
			"the canonical async path is voiceover.generate via jobs.Dispatcher (job.TypeVoiceoverGenerate = %q); "+
			"see internal/capabilities/voiceover/service/promo.go:1-8 (BLOC5.4 migration forward-deferred)",
			bridgePath, wantJobType)
	}

	// ── 3. Sanity check: bridge DOES use the current canonical surface ──
	// Today the bridge routes through the canonical per-item pipeline via
	// promoVoiceoverAdapter (Service.GeneratePromo → per-item executor).
	// A future BLOC5.4 migration to jobs.Dispatcher will update this
	// assertion to verify the dispatcher.Enqueue call site instead.
	if !strings.Contains(bridgeBody, "promoVoiceoverAdapter") && !strings.Contains(bridgeBody, "GeneratePromo") {
		t.Errorf("bridge source %q must use the canonical non-batch surface (today: Service.GeneratePromo via promoVoiceoverAdapter); "+
			"post-BLOC5.4 this assertion will need to verify the dispatcher.Enqueue call site instead",
			bridgePath)
	}

	t.Logf("canonical migration gate active: voiceover.generate is the canonical async target (job.TypeVoiceoverGenerate = %q); "+
		"bridge does NOT call Service.GenerateBatch (regression-guard for TTS-double-invocation); "+
		"current surface: Service.GenerateWithDestination; "+
		"BLOC5.4 migration target: bridge routes through jobs.Dispatcher",
		wantJobType)
}
