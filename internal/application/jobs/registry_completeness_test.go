// Package jobs — registry_completeness_test.go (Wave 19 / P1-9).
//
// The canonical user-requested enumeration test: every job type in
// the canonical domain/job/job.go const block MUST have a
// corresponding JobPolicy entry registered through jobs.Compose().
// The test enumerates the canonical source list, asserts each Type
// has a registry entry with the canonical policy contract (Queue != "",
// Concurrency >= 1, DefaultMaxRetries > 0, Description != ""), and
// verifies applyDefaults idempotency.
//
// When a future contributor adds a new `Type*` constant to
// domain/job/job.go without registering a JobPolicy in
// registry.go::Compose(), this test fails with a clear message
// pointing the maintainer at the canonical registration site.
//
// Note: the canonical source list is duplicated here as a string slice
// (mirrors the const declarations in domain/job/job.go and
// internal/application/jobs/registry.go) so the test compiles WITHOUT
// reaching into Go reflect/AST. Each entry has a comment pointing to
// its canonical declaration site so drift between the test list and
// the canonical const block surfaces at review time.
//
// Failure mode contracts (per user spec):
//  1. missing-policy       — a Type* in the canonical list lacks a
//     Compose() registration.
//  2. empty-description    — a registered entry has Description == "".
//  3. sub-canonical-queue  — Queue() accessor returns empty/""
//     when entry has been registered.
//  4. sub-canonical-concurrency — Concurrency() accessor returns < 1.
//  5. retries-nonzero      — DefaultMaxRetries accessor returns <= 0.
//     (a registered entry that allows ZERO re-licks would defeat the
//     retry contract; canonical minimum is 1 retry = the "at least
//     one re-attempt" safety net)
//  6. apply-Defaults-noop  — applyDefaults run twice produces the
//     same map snapshot (idempotency).
//  7. forward-drift        — a Type registered in Compose() is NOT
//     in canonicalJobTypes (inverse of #1).
//  8. cardinality-mismatch — reg.AllTypes().len != canonicalJobTypes.len
//     (catches asymmetry between #1 and #7 — together they pin the
//     Compose() ingress symmetric against the canonical const block).
package jobs

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// canonicalJobTypes enumerates every Type* constant declared in the
// canonical const block of either internal/domain/job/job.go or
// internal/application/jobs/registry.go. Test failure on this list
// means drift between the canonical SSOT and the test list — the
// fix is to ADD the missing Type to this slice with a comment
// pointing at its canonical declaration site.
//
// Sources of canonical job types:
//
//		internal/domain/job/job.go (Wave pre-Wave19 SSOT):	//  - TypeMediaExtract, TypeMediaStock, TypeVoiceoverBatch,
//	   TypeSubtitleGenerate, TypeYouTubeUpload,
//	   TypeYouTubeClipExtract, TypeCatalogSync, TypeArtlistRun,
//	   TypeSystemCleanup, TypeMediaGenerate,
//		    TypeBooksProcess, TypeLessonsProcess, TypeMediaReindex,
//		    TypeMediaEnrich, TypeYouTubeRebuildST, TypeScriptGenerate,
//		    TypeBulkUploadYouTubeClips, TypeDriveFolderSync,
//		    TypeMediaCurate, TypeVoiceoverPromo.
//
//		internal/application/jobs/registry.go (Wave 19 / P1-9 adder):
//		  - TypeImageGenerateGoogle — FASE 2 (June 2026) Chrome/Playwright
//		    AI image generation. The handler is NOT wired yet (pending
//		    FASE 6); the registry entry declares the operational
//		    parameters so the broker can accept jobs of this type.
//
// Adding a new Type* to either file means adding the string below
// with a comment pointing to the new declaration site. The test
// enforces this via the missing-policy rule (1) above.
//
// Mirror check (forward-drift): TestRegistry_NoForwardDrift asserts
// the inverse — every Compose()-registered Type IS in this slice.
// The two tests are pairwise: backward-drift (a Type in the slice
// missing from Compose) trips test 1; forward-drift (a Type in
// Compose NOT in the slice) trips test 4. Together they make the
// Compose() ingress symmetric against the canonical const block.
var canonicalJobTypes = []string{
	TypeMediaExtract,           // domain/job/job.go
	TypeMediaStock,             // domain/job/job.go
	TypeVoiceoverBatch,         // domain/job/job.go
	TypeSubtitleGenerate,       // domain/job/job.go
	TypeYouTubeUpload,          // domain/job/job.go
	TypeYouTubeClipExtract,     // domain/job/job.go
	TypeCatalogSync,            // domain/job/job.go
	TypeArtlistRun,             // domain/job/job.go
	TypeArtlistCacheRefresh,    // domain/media/job_types.go (durable stale-cache refresh)
	TypeSystemCleanup,          // domain/job/job.go
	TypeMediaGenerate,          // domain/job/job.go
	TypeBooksProcess,           // domain/job/job.go
	TypeLessonsProcess,         // domain/job/job.go
	TypeMediaReindex,           // domain/job/job.go
	TypeMediaEnrich,            // domain/job/job.go
	TypeYouTubeRebuildST,       // domain/job/job.go
	TypeScriptGenerate,         // domain/job/job.go
	TypeBulkUploadYouTubeClips, // domain/job/job.go
	TypeDriveFolderSync,        // domain/job/job.go
	TypeMediaCurate,            // domain/job/job.go
	TypeVoiceoverPromo,         // domain/job/job.go
	TypeVoiceoverGenerate,      // domain/job/job.go
	TypeVoiceoverGenerateItem,  // domain/job/job.go
	TypeImageGenerateGoogle,    // application/jobs/registry.go (FASE 2 / June 2026)
	// Spina Dorsale Fase 2 / PR-BATCH-REGISTER-ASYNC / PR-GEMMA-EXTRACT-IMPORTANT
	// downstream job types registered in registry_script.go, registry_stock.go,
	// registry_media.go, and registry_extraction.go.
	TypeAssetsResolve,          // domain/job/job.go
	TypeDocumentGenerate,       // domain/job/job.go
	TypeImagesGenerate,         // domain/job/job.go
	TypeClipRegister,           // domain/job/job.go
	TypeMediaStockRLMEnrich,    // domain/job/job.go
	TypeScriptGenerateItem,     // domain/job/job.go
	TypeScriptImageSibling,     // domain/job/job.go
	TypeScriptVoiceoverSibling, // domain/job/job.go
	TypeYouTubeStock,           // domain/youtube/job_types.go (YouTube stock vertical slice)
	TypeAssetTextMaterialize,   // application/jobs/registry_types.go (FASE texttracks / July 2026)
	TypeYouTubeExtract,         // application/jobs/registry_types.go (July 2026)
	TypeIntegrityVerify,        // application/jobs/registry_integrity.go
	TypeAssetCleanup,           // application/jobs/registry_integrity.go
	TypeClipRender,             // capabilities/cliprender/request.go (canonical clip post-processing, August 2026)
}

// sortedCanonicalTypes returns the canonical list sorted ascending
// so assertion failures print a stable diff (Go map iteration order
// is non-deterministic; without sorting, two test runs on the same
// code may report a different lists order — false flake).
func sortedCanonicalTypes() []string {
	out := append([]string(nil), canonicalJobTypes...)
	sort.Strings(out)
	return out
}

// canonicalTypesSet is a derived lookup of canonicalJobTypes as
// a map[string]bool. The forward-drift test (TestRegistry_NoForwardDrift)
// asserts every reg.AllTypes() entry lands in this set; the existing
// backward-missing-policy test (TestRegistry_AllCanonicalJobTypesHavePolicy)
// asserts every entry in the canonical slice has a registered
// JobPolicy. Together the two tests make the set symmetric — Compose()
// can never drift forward OR backward without one of them failing.
var canonicalTypesSet = func() map[string]bool {
	out := make(map[string]bool, len(canonicalJobTypes))
	for _, t := range canonicalJobTypes {
		out[t] = true
	}
	return out
}()

// TestRegistry_AllCanonicalJobTypesHavePolicy — the canonical
// enumeration test. Each canonical job type surfaces through the
// registry with a complete policy record.
//
// Failure messages are intentionally verbose: the maintainer who
// triggers a violation should not have to grep through the codebase
// to understand what re-registration is required. Each failure
// message names the canonical registration site explicitly.
func TestRegistry_AllCanonicalJobTypesHavePolicy(t *testing.T) {
	reg := Compose() // builds registry + applies Wave-19 defaults

	// Sanity: every canonical Type in the static list must be in the
	// registry. This is the FAIL-FAST on the most common drift:
	// someone added a Type* const and forgot the JobPolicy{} register.
	missing := []string{}
	for _, t := range sortedCanonicalTypes() {
		if !reg.IsRegistered(t) {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		t.Fatalf(
			"FAIL Policy contract (missing-policy, Wave 19 / P1-9):\n"+
				"  %d canonical job type(s) NOT registered in jobs.Compose():\n"+
				"    %s\n"+
				"Fix: open internal/application/jobs/registry.go and add a\n"+
				"     r.Register(JobPolicy{Type: <the-type>, Description: ..., Timeout: ..., DefaultMaxRetries: ...})\n"+
				"     call in the Compose() function. The struct doc-comment\n"+
				"     `JobPolicy{...}` block lists ALL Wave 19 fields.",
			len(missing), strings.Join(missing, ", "),
		)
	}

	// Per-entry policy contract: every registered canonical Type
	// must surface a valid policy via the typed accessors.
	policyFailures := []string{}
	for _, t := range sortedCanonicalTypes() {
		entry, ok := reg.Get(t)
		if !ok {
			continue // already reported in the missing set above
		}

		// Contract (b): empty-description rule.
		if strings.TrimSpace(entry.Description) == "" {
			policyFailures = append(policyFailures,
				t+": Description is empty (operators have no signal)")
		}

		// Contract (c): empty-queue accessor rule.
		if reg.Queue(t) == "" {
			policyFailures = append(policyFailures,
				t+": Queue() accessor returned empty string (registration missed DefaultQueue pass)")
		}

		// Contract (d): sub-canonical Concurrency accessor rule.
		if reg.Concurrency(t) < 1 {
			policyFailures = append(policyFailures,
				t+": Concurrency() accessor returned "+strconv.Itoa(reg.Concurrency(t))+
					" (< 1; registration missed DefaultConcurrency pass)")
		}

		// Contract (e): DefaultMaxRetries should be at least 1.
		// A policy that allows ZERO retries would defeat the retry
		// safety net (a single transient failure kills the job).
		if reg.DefaultMaxRetries(t) < 1 {
			policyFailures = append(policyFailures,
				t+": DefaultMaxRetries() accessor returned "+
					strconv.Itoa(reg.DefaultMaxRetries(t))+
					" (< 1; canonical minimum is 1 retry)")
		}
	}
	if len(policyFailures) > 0 {
		t.Fatalf(
			"FAIL Policy contracts (per-entry, Wave 19 / P1-9):\n"+
				"  %d registered entry/entries with broken policy contracts:\n"+
				"    %s\n"+
				"Fix: every JobPolicy{...} literal in registry.go::Compose() must\n"+
				"     include Description, DefaultMaxRetries >= 1, and (the Wave 19\n"+
				"     addition) Queue + Concurrency. The applyDefaults() pass fills\n"+
				"     missing Queue/Concurrency with DefaultQueue/DefaultConcurrency,\n"+
				"     but a future contributor adding a new entry should set them\n"+
				"     explicitly so the static type carries the canonical value.",
			len(policyFailures), strings.Join(policyFailures, "; "),
		)
	}
}

// TestRegistry_ApplyDefaultsIsIdempotent — the canonical Wave-19
// normalisation rule. Compose() calls applyDefaults() once at the
// end of construction. If a future contributor adds another pass
// (e.g. for a new typed field), the registry should still be
// idempotent under reapplied defaults — running applyDefaults()
// twice in a row produces the same map snapshot.
//
// Failure mode: a typo in the normalisation pass (e.g. forgetting
// to handle a field) causes the canonical value to drift between
// the first and second pass. This test pins the contract.
//
// Implementation note: pre/post comparison uses a per-Type deep-equal
// of the full RegistryEntry via reflect.DeepEqual. Direct
// field-equality avoids the brittleness of an ad-hoc
// "hash-of-concurrency+queue-len" formula (which could collide
// between adjacent states if the formula's base magnitude is wrong).
func TestRegistry_ApplyDefaultsIsIdempotent(t *testing.T) {
	reg := Compose()

	// Snapshot the full entry per Type pre-second-pass.
	type entrySnapshot struct {
		entry RegistryEntry
		ok    bool
	}
	pre := make(map[string]entrySnapshot, len(canonicalJobTypes))
	for _, c := range sortedCanonicalTypes() {
		e, ok := reg.Get(c)
		pre[c] = entrySnapshot{entry: e, ok: ok}
	}

	// Second pass — applies defaults AGAIN.
	reg.applyDefaults()

	// Snapshot post-second-pass + diff.
	drifts := []string{}
	for c, preSS := range pre {
		postEntry, postOK := reg.Get(c)
		// Reflect.DeepEqual on the FULL entry. The post-pass entry
		// is byte-identical to the pre-pass entry when applyDefaults
		// is well-behaved (idempotent).
		if preSS.ok != postOK || (preSS.ok && !reflect.DeepEqual(preSS.entry, postEntry)) {
			drifts = append(drifts,
				c+": pre-vs-post entry differs after a second applyDefaults() pass")
		}
	}
	if len(drifts) > 0 {
		t.Fatalf(
			"FAIL applyDefaults idempotency contract (Wave 19 / P1-9):\n"+
				"  %d entry/entries DRIFT between successive applyDefaults() passes:\n"+
				"    %s\n"+
				"Fix: applyDefaults() is documented idempotent in registry.go. A\n"+
				"     drift means a normalisation rule is missing (e.g. a new typed\n"+
				"     field whose zero-value isn't coerced to a canonical constant).",
			len(drifts), strings.Join(drifts, "; "),
		)
	}
}

// TestRegistry_AllTypesAndQueueCoverage — coverage check.
//
// Even if every Type* has a JobPolicy, the registry SHOULD expose
// every registered Type via AllTypes() (used by scheduler / ClaimNext
// type filters). And the Queue() accessor should return a non-empty
// string for EVERY registered Type (the typed-accessor contract
// from Wave 19).
func TestRegistry_AllTypesAndQueueCoverage(t *testing.T) {
	reg := Compose()
	allTypes := reg.AllTypes()
	if len(allTypes) == 0 {
		t.Fatal("Registry.AllTypes() returned empty slice — Compose() failed silently?")
	}

	emptyQueue := []string{}
	for _, c := range sortedCanonicalTypes() {
		if reg.Queue(c) == "" {
			emptyQueue = append(emptyQueue, c)
		}
	}
	if len(emptyQueue) > 0 {
		t.Fatalf(
			"FAIL Queue() coverage contract (Wave 19 / P1-9):\n"+
				"  %d registered canonical Type(s) report empty Queue via the typed accessor:\n"+
				"    %s\n"+
				"Fix: the applyDefaults() pass must run before any Queue() read\n"+
				"     RIGHT AFTER Compose() returns. If Queue() reports empty on a\n"+
				"     fresh registry, applyDefaults() didn't fire (assertion would\n"+
				"     have caught the bug at the build site via the var _ assertion).",
			len(emptyQueue), strings.Join(emptyQueue, ", "),
		)
	}
}

// TestRegistry_NoForwardDrift — the inverse of
// TestRegistry_AllCanonicalJobTypesHavePolicy: every Type registered
// in Compose() MUST appear in canonicalJobTypes. Together the two
// tests form a symmetric pairing:
//
//   - TestRegistry_AllCanonicalJobTypesHavePolicy catches BACKWARD drift
//     (a Type* in the canonical slice missing from Compose()).
//   - TestRegistry_NoForwardDrift       catches FORWARD drift
//     (a Type* registered in Compose() but missing from the slice,
//     i.e. someone added a registration without updating the test).
//
// Forward drift is the more dangerous of the two: a contributor
// adds a new JobPolicy{...} literal in Compose() AND adds a
// matching Type* const to domain/job/job.go (or registry.go's
// const block), but forgets to add the new string to the test's
// canonicalJobTypes slice. Without this test, the new registration
// is silent — it rolls out without test coverage.
//
// Drift fingerprint rule: the test fails if reg.AllTypes() is NOT a
// SUBSET of canonicalTypesSet. A SUBSET (Compose has fewer than
// the canonical slice has) is the BACKWARD direction, which is
// caught by the other test; a SUPERSET would be forward drift
// here.
func TestRegistry_NoForwardDrift(t *testing.T) {
	reg := Compose()

	// Collect all Compose()-registered Types.
	registered := make(map[string]bool, len(reg.AllTypes()))
	for _, c := range reg.AllTypes() {
		registered[c] = true
	}

	// Forward drift: every registered Type must be in canonicalTypesSet.
	stale := []string{}
	for c := range registered {
		if !canonicalTypesSet[c] {
			stale = append(stale, c)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf(
			"FAIL forward-drift (Wave 19 / P1-9):\n"+
				"  %d registered Type(s) are NOT in canonicalJobTypes:\n"+
				"    %s\n"+
				"Fix: forward drift means a new JobPolicy{...} literal was added\n"+
				"     to internal/application/jobs/registry.go::Compose() WITHOUT\n"+
				"     mirroring the new Type* into the canonicalJobTypes slice of\n"+
				"     this test file. The slice mirrors the const declarations in\n"+
				"     internal/domain/job/job.go + internal/application/jobs/registry.go.\n"+
				"     Add the missing string(s) above to canonicalJobTypes with a\n"+
				"     comment pointing to the canonical declaration site.",
			len(stale), strings.Join(stale, ", "),
		)
	}

	// Symmetry bonus: count the registered Types vs the canonical
	// Types. If forward+backward drift are both caught by the
	// other tests, this assertion pins the cardinality relationship.
	if len(registered) != len(canonicalTypesSet) {
		sort.Strings(stale)
		t.Fatalf(
			"FAIL cardinality mismatch (Wave 19 / P1-9):\n"+
				"  reg.AllTypes() cardinality: %d\n"+
				"  canonicalJobTypes cardinality: %d\n"+
				"  One of the two drift directions was missed by the prior tests\n"+
				"  (TestRegistry_AllCanonicalJobTypesHavePolicy for backward,\n"+
				"  TestRegistry_NoForwardDrift for forward). Inspect the reg+\n"+
				"  canonical diff set to find the asymmetry.",
			len(registered), len(canonicalTypesSet),
		)
	}
}

// TestRegistry_TimeoutAccessorFallback — sanity: a registered Type's
// Timeout accessor returns > 0 (sanity check on the typed-accessor
// surface; a zero timeout would defeat job execution latency). For
// non-registered types, Timeout() falls through to the canonical
// 10-minute default.
func TestRegistry_TimeoutAccessorFallback(t *testing.T) {
	reg := Compose()

	// Probe every canonical Type — must return a positive timeout.
	bad := []string{}
	for _, c := range sortedCanonicalTypes() {
		if d := reg.Timeout(c); d <= 0 {
			bad = append(bad, c+": timeout="+d.String())
		}
	}
	if len(bad) > 0 {
		t.Fatalf(
			"FAIL Timeout() accessor contract (Wave 19 / P1-9):\n"+
				"  %d canonical Type(s) report zero-or-negative Timeout:\n"+
				"    %s\n"+
				"Fix: every JobPolicy{...} literal in Compose() must include a\n"+
				"     non-zero Timeout. The Time Fallback to the canonical 10-min\n"+
				"     default only kicks in when Timeout == 0 AND the type is\n"+
				"     unregistered, NOT when the type IS registered with 0.",
			len(bad), strings.Join(bad, ", "),
		)
	}

	// An unregistered type falls through to the canonical default.
	if reg.Timeout("never-registered-type") != 10*time.Minute {
		t.Fatalf("unregistered type fallback expected 10min, got %v",
			reg.Timeout("never-registered-type"))
	}
}
