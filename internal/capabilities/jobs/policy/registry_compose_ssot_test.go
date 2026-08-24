// Package jobs — registry_compose_ssot_test.go (SSOT contract pin, June 2026).
//
// Pins the HC-1 (June 2026) Single Source Of Truth contract for
// per-job-type execution timeouts. HC-1 replaced the pre-HC-1
// hard-coded `context.WithTimeout(ctx, 2*time.Hour)` literal in
// internal/application/clips/bulk_upload_worker.go with a typed
// lookup rooted on *Registry.JobTimeout(t). The contract under
// guard:
//
//	(a) FORWARD: every entry registered in Compose() has a
//	    non-zero Timeout. Zero collapses to the canonical 10-minute
//	    fallback — exactly the regression signature we're guarding
//	    against (an inadvertent 2*time.Hour / 0 in a register call
//	    would re-introduce the pre-HC-1 drift).
//
//	(b) REVERSE: every canonical Type* constant declared in this
//	    package is registered in Compose(). A new Type* constant
//	    added without a corresponding r.Register(JobPolicy{...})
//	    call would silently fall through to the canonical 10-minute
//	    default at worker dispatch time — a real risk because
//	    handlers/dispatchers/jobs.Service code references Type*
//	    constants directly, not the lookup result.
//
//	(c) CHAIN: the TimeoutResolver interface dispatch, the
//	    Registry.JobTimeout method dispatch, and the Compose()
//	    snapshot lookup ALL agree on the same value for any given
//	    job type. The composition root (internal/app/clips_adapters_cfg.go)
//	    consumes via the TimeoutResolver interface; future drifts
//	    between the dispatch surfaces would silently diverge from
//	    the SSOT.
//
// Pattern 0 / SSOT rationale: the registry is the canonical source
// for per-job-type timeouts, max-retries, queue labels, concurrency,
// and required capabilities. These tests are the regression guard
// that a future contributor cannot accidentally re-introduce a
// hard-coded literal without flipping a CI test red.
//
// The companion CI gate Check 40 in scripts/ci-architectural-checks.sh
// bans `var jobTimeoutRegistry` / `SetJobTimeout(` callers; this test
// file complements it with an integration-level SSOT assertion.
package policy

import (
	"testing"
	"time"
)

// TestCompose_AllEntriesHaveNonZeroTimeout — forward SSOT direction.
// For every entry registered in Compose(), Timeout > 0 must hold.
// A zero value silently collapses to the canonical 10-minute fallback
// (Registry.Timeout's last-line default), which is exactly the
// signature of the pre-HC-1 hard-coded-literal regression we want to
// catch permanently.
func TestCompose_AllEntriesHaveNonZeroTimeout(t *testing.T) {
	t.Parallel()

	reg := Compose()
	if typs := reg.AllTypes(); len(typs) == 0 {
		t.Fatal("Compose() returned empty registry; expected 23+ entries (see registry.go::Compose())")
	}

	for _, typ := range reg.AllTypes() {
		entry, ok := reg.Get(typ)
		if !ok {
			t.Errorf("AllTypes() returned %q but Get says not registered (registry invariant broken)", typ)
			continue
		}
		if entry.Timeout <= 0 {
			t.Errorf("Registry[%q].Timeout = %v (zero or negative); Compose() MUST register every entry with a non-zero Timeout — the pre-HC-1 hard-coded 2*time.Hour literal must NEVER come back", typ, entry.Timeout)
		}
	}
}

// TestCompose_AllCanonicalTypesAreRegistered — reverse SSOT direction.
// For every Type* constant declared in this package, Compose() must
// contain an entry. This guards against the "new Type* constant added
// without registering" drift — handlers/dispatchers reference
// Type* constants directly, and un-registered types silently fall
// through to the canonical 10-minute default.
//
// The expected-names list mirrors registry.go::Compose() 1-to-1; if
// a future Type* constant is added, this test MUST be updated to
// include it. The cross-check between registry.go's (a) compose
// register calls and (b) this list is the SSOT seam marker: any
// drift between the two surfaces a CI failure.
func TestCompose_AllCanonicalTypesAreRegistered(t *testing.T) {
	t.Parallel()

	reg := Compose()

	cases := []struct {
		name string
		typ  string
	}{
		// ── Script generation ──
		{"TypeScriptGenerate", TypeScriptGenerate},
		{"TypeMediaCurate", TypeMediaCurate},

		// ── Media processing ──
		{"TypeMediaExtract", TypeMediaExtract},
		{"TypeMediaStock", TypeMediaStock},
		{"TypeMediaGenerate", TypeMediaGenerate},
		{"TypeMediaReindex", TypeMediaReindex},
		{"TypeMediaEnrich", TypeMediaEnrich},
		{"TypeBulkUploadYouTubeClips", TypeBulkUploadYouTubeClips}, // HC-1 anchor

		// ── YouTube ──
		{"TypeYouTubeUpload", TypeYouTubeUpload},
		{"TypeYouTubeClipExtract", TypeYouTubeClipExtract},
		{"TypeYouTubeRebuildST", TypeYouTubeRebuildST},

		// ── Voiceover / subtitles ──
		{"TypeVoiceoverBatch", TypeVoiceoverBatch},
		{"TypeVoiceoverPromo", TypeVoiceoverPromo},
		{"TypeSubtitleGenerate", TypeSubtitleGenerate},

		// ── Catalog / sync ──
		{"TypeCatalogSync", TypeCatalogSync},
		{"TypeArtlistRun", TypeArtlistRun},
		{"TypeDriveFolderSync", TypeDriveFolderSync},

		// ── Content processing ──
		{"TypeBooksProcess", TypeBooksProcess},
		{"TypeLessonsProcess", TypeLessonsProcess},

		// ── System ──
		{"TypeSystemCleanup", TypeSystemCleanup},

		// ── AI Image Generation ──
		{"TypeImageGenerateGoogle", TypeImageGenerateGoogle},

		// ── Clip registration (async batch, PR-BATCH-REGISTER-ASYNC) ──
		{"TypeClipRegister", TypeClipRegister},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if !reg.IsRegistered(c.typ) {
				t.Fatalf("Type* %s (%q) is NOT registered in Compose(); handlers/dispatchers referencing this constant will silently fall back to the canonical 10-minute default — add an r.Register(JobPolicy{...}) call in registry.go::Compose()", c.name, c.typ)
			}

			// Cross-direction SSOT: even if registered, a zero Timeout
			// would collapse to the canonical default. Assert non-zero.
			entry, _ := reg.Get(c.typ)
			if entry.Timeout <= 0 {
				t.Fatalf("Registry[%q].Timeout = %v (zero); Compose() MUST register every entry with a non-zero Timeout", c.typ, entry.Timeout)
			}

			// Wave 19 (June 2026) typed accessor contract: after
			// applyDefaults() (invoked by Compose), every registered
			// entry's Queue must read non-empty via Queue(t) and
			// Concurrency must read >= 1 via Concurrency(t). These
			// accessors are the canonical SSOT surface for those
			// fields, not the raw entry.Queue / entry.Concurrency.
			if q := reg.Queue(c.typ); q == "" {
				t.Fatalf("Registry.Queue(%q) = %q (empty); Compose() must normalise zero/empty Queue via applyDefaults()", c.typ, q)
			}
			if cc := reg.Concurrency(c.typ); cc < DefaultConcurrency {
				t.Fatalf("Registry.Concurrency(%q) = %d (< DefaultConcurrency=%d); Compose() must normalise zero/negative Concurrency via applyDefaults()", c.typ, cc, DefaultConcurrency)
			}
		})
	}
}

// TestCompose_BulkUploadTimeoutResolvesThroughAllSurfaces — chain
// SSOT direction. The HC-1 lookup chain has THREE independent
// dispatch surfaces:
//
//  1. REGISTRY DIRECT: reg.JobTimeout(TypeBulkUploadYouTubeClips)
//     — the canonical method on Registry. Used by JobsHandler
//     directly via Worker.WithRegistry.
//  2. INTERFACE: typed-port TimeoutResolver.JobTimeout(...) —
//     the alias that internal/app/clips_adapters_cfg.go::clipsCfgAdapter
//     forwards to. Used by the bulk_upload worker via the typed
//     ClipConfigPort.JobTimeout method.
//  3. SNAPSHOT: the typed-port TimeoutMap returned by reg.Compose()
//     — held by each Worker as w.timeouts and indexed per
//     j.Type at runJob time.
//
// All three must return the same value for the canonical
// bulk-upload job type. A future fork in the lookup surfaces would
// silently diverge from the SSOT and let an operator override
// propagate inconsistently across the three readers.
//
// Pin is anchored at 120 minutes (the registered value) — if
// Compose() ever changes the canonical timeout for this type, this
// test surface is the seam marker that forces an explicit review.
func TestCompose_BulkUploadTimeoutResolvesThroughAllSurfaces(t *testing.T) {
	t.Parallel()

	reg := Compose()

	const wantTimeout = 120 * time.Minute

	// Surface 1: Registry.JobTimeout (direct method).
	if got := reg.JobTimeout(TypeBulkUploadYouTubeClips); got != wantTimeout {
		t.Fatalf("Registry.JobTimeout(%q) = %v; want canonical %v (width-SSOT, HC-1 SSOT pin)",
			TypeBulkUploadYouTubeClips, got, wantTimeout)
	}

	// Surface 2: TimeoutResolver interface dispatch.
	// This is the surface internal/app/clips_adapters_cfg.go consumes
	// and forwards to the bulk_upload_worker's ClipConfigPort.JobTimeout.
	var resolver TimeoutResolver = reg
	if got := resolver.JobTimeout(TypeBulkUploadYouTubeClips); got != wantTimeout {
		t.Fatalf("TimeoutResolver.JobTimeout(%q) via interface = %v; want %v (interface dispatch MUST agree with direct method — composability invariant of HC-1)",
			TypeBulkUploadYouTubeClips, got, wantTimeout)
	}

	// Surface 3: Compose() snapshot (the typed-port TimeoutMap held
	// by each Worker internally as w.timeouts).
	snap := reg.Compose()
	got, ok := snap[TypeBulkUploadYouTubeClips]
	if !ok {
		t.Fatalf("Compose() snapshot missing entry for %q; Worker.timeouts index lookup would silently fall through to canonical 10m default",
			TypeBulkUploadYouTubeClips)
	}
	if got != wantTimeout {
		t.Fatalf("Compose()[%q] = %v; want %v (snapshot lookup MUST agree with direct method — Worker.jobTimeoutFor uses this snapshot)",
			TypeBulkUploadYouTubeClips, got, wantTimeout)
	}

	// Negative control: an unregistered type must NOT collide with
	// the canonical 10-minute default's numeric noise. We assert the
	// snapshot returns (zero, false) for an unknown type, NOT a
	// value — this guarantees Worker.jobTimeoutFor's job-type → map
	// hit/miss semantics remain consistent across the SSOT chain.
	if v, ok := snap["definitely.not.a.real.type"]; ok {
		t.Fatalf("Compose() snapshot has entry for unknown type \"definitely.not.a.real.type\" = %v; the snapshot MUST only carry entries with Timeout > 0 (Registry.Compose() filters zero-timeouts per HC-1 code-review DISCUSS)", v)
	}
}

// TestCompose_ScriptGenerateItem_ProducesArtifactsFalse — P0 #4 audit
// (Commit A, July 2026). Pins the contract that script.generate_item is
// a pure-data child job (ProducesArtifacts=false). The child handler
// produces only a result map (ok, status, item_id) — it does NOT
// produce an artifact manifest. Removing ProducesArtifacts=true fixes
// the state-machine breakage where SQLiteStore.Complete would reject
// completions with the canonical typed sentinel
// domainremote.ErrCompleteJobPathViolation (per FASE 0.1, July 4 2026:
// the pre-FASE-0.1 package-local alias ErrArtifactJobRequiresCompleteWithArtifacts
// was REMOVED; godlike/06 SSOT owner is
// internal/domain/remote/complete_job.go::ErrCompleteJobPathViolation).
func TestCompose_ScriptGenerateItem_ProducesArtifactsFalse(t *testing.T) {
	t.Parallel()

	reg := Compose()

	if !reg.IsRegistered(TypeScriptGenerateItem) {
		t.Fatal("TypeScriptGenerateItem must be registered in Compose()")
	}

	if reg.ProducesArtifacts(TypeScriptGenerateItem) {
		t.Fatal("TypeScriptGenerateItem must set ProducesArtifacts=false (pure-data child job; produces only a result map — no artifact manifest). " +
			"ProducesArtifacts=true breaks the state-machine: SQLiteStore.Complete rejects completions and demands CompleteWithArtifacts, " +
			"but the child handler never builds an artifact manifest.")
	}
}

// TestCompose_SnapshotFiltersZeroTimeoutEntries — pins the
// documented "Zero-filter semantics" of Registry.Compose()
// (registry.go::Compose() doc, HC-1 code-review DISCUSS).
//
// The promise: a registered entry with Timeout=0 MUST be filtered
// out of the Compose() snapshot. Without this guarantee, a future
// contributor who registers `JobPolicy{Timeout: 0}` would have its
// entry present in the snapshot — Worker.jobTimeoutFor would then
// return 0 instead of the canonical 10m default, silently breaking
// the SSOT boundary.
//
// Filter vs map-miss matters because Workers.index-scan the snapshot
// branch-free: `timeouts[j.Type]` returns (0, false) for BOTH an
// unregistered type AND a registered zero-timeout type. Worker.jobTimeoutFor
// must treat these identically (canonical 10m). This test pins the
// snapshot shape that makes that branch-free dispatch reliable.
func TestCompose_SnapshotFiltersZeroTimeoutEntries(t *testing.T) {
	t.Parallel()

	const zeroTimeoutType = "ssot-test-zero-timeout"

	reg := NewRegistry()
	if err := reg.Register(RegistryEntry{
		Completion: CompletionDeclaration{
			JobType:              zeroTimeoutType,
			ArtifactOwnership:    ArtifactOwnershipNone,
			FinalizationStrategy: FinalizationStrategyLegacyComplete,
		},
		Description:       "ssot zero-timeout filter pin",
		Timeout:           0, // explicit zero — must be filtered by Compose()
		DefaultMaxRetries: 1,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	snap := reg.Compose()

	// (1) Filter semantic: Timeout=0 entries MUST be absent from the snapshot.
	if v, ok := snap[zeroTimeoutType]; ok {
		t.Fatalf("Compose() snapshot has entry for zero-timeout type: %v; the doc-promised filter is broken — Worker.jobTimeoutFor's branch-free timeouts[j.Type] lookup would silently read 0 from the snapshot instead of falling through to the canonical 10m default", v)
	}

	// (2) Cross-check: direct method (Registry.JobTimeout) returns
	// canonical 10m for registered-but-zero-timeout type — proves
	// the filter and the typed accessor agree on the same default.
	if got := reg.JobTimeout(zeroTimeoutType); got != 10*time.Minute {
		t.Fatalf("Registry.JobTimeout(%q) for zero-timeout registered type = %v; want canonical 10m (the typed accessor MUST agree with the snapshot filter semantic)", zeroTimeoutType, got)
	}

	// (3) IsRegistered still returns true (the type IS in the registry;
	// only the snapshot filters it out — these are independent surfaces).
	if !reg.IsRegistered(zeroTimeoutType) {
		t.Fatalf("Registry.IsRegistered(%q) = false (registered-but-zero-timeout must still report registered)", zeroTimeoutType)
	}
}
