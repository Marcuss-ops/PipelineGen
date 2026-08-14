package projectionretention

import (
	"reflect"
	"sort"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

const testPrefix = "media_assets_v3_nomic_768_siglip_768"

func statusMap(projections map[string]mediaregistry.ProjectionStatus) map[string]mediaregistry.ProjectionStatus {
	return projections
}

// TestDecide_StatusAware_FailedPartialNeverKept pins the failed-partial
// bug: a FAILED partial with a newer name timestamp must never crowd out
// the previous known-good (RETIRED) rollback target.
func TestDecide_StatusAware_FailedPartialNeverKept(t *testing.T) {
	active := testPrefix + "_20260814_071358_545816432"
	failedPartial := testPrefix + "_20260814_070758_298500178"
	rollback := testPrefix + "_20260813_184719_687643250"
	olderRetired := testPrefix + "_20260813_182650_373125513"

	plan, err := (ProjectionRetentionPolicy{KeepLastN: 2, RetentionDays: 1}).Decide(Input{
		Collections:   []string{active, failedPartial, rollback, olderRetired},
		ActiveTarget:  active,
		CurrentPrefix: testPrefix,
		Statuses: statusMap(map[string]mediaregistry.ProjectionStatus{
			active:        mediaregistry.ProjectionActive,
			failedPartial: mediaregistry.ProjectionFailed,
			rollback:      mediaregistry.ProjectionRetired,
			olderRetired:  mediaregistry.ProjectionRetired,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	got := append([]string(nil), plan.Drop...)
	want := []string{failedPartial, olderRetired}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Drop = %v, want %v", got, want)
	}
	if len(plan.Protected) != 1 || plan.Protected[0] != rollback {
		t.Fatalf("Protected = %v, want [%s]", plan.Protected, rollback)
	}
	if len(plan.Keep) != 2 {
		t.Fatalf("Keep = %v, want 2 entries (active + rollback)", plan.Keep)
	}
}

// TestDecide_RetiredPrefixMatched verifies retired-schema prefixes are
// matched and unknown collections are left untouched.
func TestDecide_RetiredPrefixMatched(t *testing.T) {
	active := testPrefix + "_20260814_071358_545816432"
	rollback := testPrefix + "_20260813_184719_687643250"
	e5 := "media_assets_v3_e5_768_siglip_768_20260813_181431_205856695"
	bare := "media_assets"
	synthetic := "synthetic_assets_test_v3"

	plan, err := (ProjectionRetentionPolicy{
		KeepLastN:       2,
		RetentionDays:   1,
		RetiredPrefixes: []string{"media_assets_v3_e5_768_siglip_768"},
	}).Decide(Input{
		Collections:   []string{active, rollback, e5, bare, synthetic},
		ActiveTarget:  active,
		CurrentPrefix: testPrefix,
		Statuses: statusMap(map[string]mediaregistry.ProjectionStatus{
			active:   mediaregistry.ProjectionActive,
			rollback: mediaregistry.ProjectionRetired,
			e5:       mediaregistry.ProjectionRetired,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Drop) != 1 || plan.Drop[0] != e5 {
		t.Fatalf("Drop = %v, want [%s]", plan.Drop, e5)
	}
}

// TestDecide_KeepLastN_DescendingTail verifies the newest-first keep tail.
func TestDecide_KeepLastN_DescendingTail(t *testing.T) {
	active := testPrefix + "_active"
	oldest := testPrefix + "_20260101_aaa"
	middle := testPrefix + "_20260201_bbb"

	plan, err := (ProjectionRetentionPolicy{KeepLastN: 2, RetentionDays: 1}).Decide(Input{
		Collections:   []string{active, oldest, middle},
		ActiveTarget:  active,
		CurrentPrefix: testPrefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Drop) != 1 || plan.Drop[0] != oldest {
		t.Fatalf("Drop = %v, want [%s]", plan.Drop, oldest)
	}
	if len(plan.Keep) != 2 {
		t.Fatalf("Keep = %v, want 2 (active + newest)", plan.Keep)
	}
}

// TestDecide_DisabledReturnsEmpty pins the RetentionDays <= 0 no-op.
func TestDecide_DisabledReturnsEmpty(t *testing.T) {
	plan, err := (ProjectionRetentionPolicy{KeepLastN: 2, RetentionDays: 0}).Decide(Input{
		Collections:   []string{"a", "b"},
		CurrentPrefix: testPrefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Drop) != 0 || len(plan.Keep) != 0 {
		t.Fatalf("disabled policy must return an empty plan, got %+v", plan)
	}
}

// TestRetentionPrefixes_RejectsOverlappingPrefix pins the fail-closed guard.
func TestRetentionPrefixes_RejectsOverlappingPrefix(t *testing.T) {
	if _, err := RetentionPrefixes(testPrefix, []string{"media_assets"}); err == nil {
		t.Fatal("expected error for overlapping prefix")
	}
	if _, err := RetentionPrefixes(testPrefix, []string{"media_assets_v3"}); err == nil {
		t.Fatal("expected error for overlapping prefix")
	}
	if _, err := RetentionPrefixes(testPrefix, []string{testPrefix}); err == nil {
		t.Fatal("expected error for prefix equal to current")
	}
	got, err := RetentionPrefixes(testPrefix, []string{"media_assets_v3_e5_768_siglip_768", "", "media_assets_v3_e5_768_siglip_768"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{testPrefix, "media_assets_v3_e5_768_siglip_768"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestIsStalePartial(t *testing.T) {
	for _, s := range []mediaregistry.ProjectionStatus{
		mediaregistry.ProjectionFailed, mediaregistry.ProjectionBuilding, mediaregistry.ProjectionValidating,
	} {
		if !IsStalePartial(s) {
			t.Fatalf("IsStalePartial(%s) = false, want true", s)
		}
	}
	for _, s := range []mediaregistry.ProjectionStatus{
		mediaregistry.ProjectionActive, mediaregistry.ProjectionRetired, mediaregistry.ProjectionReady, "",
	} {
		if IsStalePartial(s) {
			t.Fatalf("IsStalePartial(%q) = true, want false", s)
		}
	}
}

// contains reports whether names contains target.
func contains(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

// TestProjectionRetention_ActivePlusOneRollbackLeavesTwoCollections pins the
// keep_last_n=2 floor: active + exactly one known-good rollback survive, and
// every older retired collection is dropped.
func TestProjectionRetention_ActivePlusOneRollbackLeavesTwoCollections(t *testing.T) {
	active := testPrefix + "_20260814_071358_545816432"
	rollback1 := testPrefix + "_20260813_184719_687643250"
	rollback2 := testPrefix + "_20260813_182650_373125513"
	rollback3 := testPrefix + "_20260812_132156_138767446"

	plan, err := (ProjectionRetentionPolicy{KeepLastN: 2, RetentionDays: 1}).Decide(Input{
		Collections:   []string{active, rollback1, rollback2, rollback3},
		ActiveTarget:  active,
		CurrentPrefix: testPrefix,
		Statuses: statusMap(map[string]mediaregistry.ProjectionStatus{
			active:    mediaregistry.ProjectionActive,
			rollback1: mediaregistry.ProjectionRetired,
			rollback2: mediaregistry.ProjectionRetired,
			rollback3: mediaregistry.ProjectionRetired,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Keep) != 2 {
		t.Fatalf("Keep = %v, want exactly 2 (active + one rollback)", plan.Keep)
	}
	if !contains(plan.Keep, active) {
		t.Fatalf("Keep = %v, must include active target %q", plan.Keep, active)
	}
	if len(plan.Protected) != 1 || plan.Protected[0] != rollback1 {
		t.Fatalf("Protected = %v, want [%s] (newest known-good rollback)", plan.Protected, rollback1)
	}

	got := append([]string(nil), plan.Drop...)
	want := []string{rollback2, rollback3}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Drop = %v, want %v", got, want)
	}
}

// TestProjectionRetention_NeverDeletesActiveAliasTarget pins the
// defense-in-depth invariant: the runtime alias target is never dropped, even
// when the durable status map (stale) marks it RETIRED.
func TestProjectionRetention_NeverDeletesActiveAliasTarget(t *testing.T) {
	active := testPrefix + "_20260814_071358_545816432"
	rollback1 := testPrefix + "_20260813_184719_687643250"
	rollback2 := testPrefix + "_20260813_182650_373125513"

	plan, err := (ProjectionRetentionPolicy{KeepLastN: 2, RetentionDays: 1}).Decide(Input{
		Collections:   []string{active, rollback1, rollback2},
		ActiveTarget:  active,
		CurrentPrefix: testPrefix,
		// active is intentionally marked RETIRED: the alias target must win
		// over the stale status so a transient registry/alias disagreement
		// can never drop the live write target.
		Statuses: statusMap(map[string]mediaregistry.ProjectionStatus{
			active:    mediaregistry.ProjectionRetired,
			rollback1: mediaregistry.ProjectionRetired,
			rollback2: mediaregistry.ProjectionRetired,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	if contains(plan.Drop, active) {
		t.Fatalf("active alias target %q must never be dropped; Drop=%v", active, plan.Drop)
	}
	if !contains(plan.Keep, active) {
		t.Fatalf("active alias target %q must be kept; Keep=%v", active, plan.Keep)
	}
	if len(plan.Drop) != 1 || plan.Drop[0] != rollback2 {
		t.Fatalf("Drop = %v, want [%s] (only the older retired rollback)", plan.Drop, rollback2)
	}
}

// TestProjectionRetention_DeletesOnlyRetiredCollections pins the status
// safety of the drop set: every dropped collection carries a RETIRED (known
// good, superseded) lifecycle, and neither ACTIVE nor the protected rollback
// is ever dropped.
func TestProjectionRetention_DeletesOnlyRetiredCollections(t *testing.T) {
	active := testPrefix + "_20260814_071358_545816432"
	rollback := testPrefix + "_20260813_184719_687643250"
	olderRetired := testPrefix + "_20260813_182650_373125513"

	statuses := statusMap(map[string]mediaregistry.ProjectionStatus{
		active:       mediaregistry.ProjectionActive,
		rollback:     mediaregistry.ProjectionRetired,
		olderRetired: mediaregistry.ProjectionRetired,
	})
	plan, err := (ProjectionRetentionPolicy{KeepLastN: 2, RetentionDays: 1}).Decide(Input{
		Collections:   []string{active, rollback, olderRetired},
		ActiveTarget:  active,
		CurrentPrefix: testPrefix,
		Statuses:      statuses,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Drop) != 1 || plan.Drop[0] != olderRetired {
		t.Fatalf("Drop = %v, want [%s]", plan.Drop, olderRetired)
	}
	for _, dropped := range plan.Drop {
		if statuses[dropped] != mediaregistry.ProjectionRetired {
			t.Fatalf("dropped collection %q has status %s, want RETIRED", dropped, statuses[dropped])
		}
	}
	if contains(plan.Drop, active) || contains(plan.Drop, rollback) {
		t.Fatalf("active or protected rollback must never be dropped; Drop=%v", plan.Drop)
	}
}

// TestProjectionRetention_IsIdempotent pins two idempotency properties:
// Decide is deterministic (same input -> same plan), and re-running the sweep
// after the drop set has been removed produces no further drops.
func TestProjectionRetention_IsIdempotent(t *testing.T) {
	active := testPrefix + "_20260814_071358_545816432"
	rollback := testPrefix + "_20260813_184719_687643250"
	olderRetired := testPrefix + "_20260813_182650_373125513"

	policy := ProjectionRetentionPolicy{KeepLastN: 2, RetentionDays: 1}
	input := Input{
		Collections:   []string{active, rollback, olderRetired},
		ActiveTarget:  active,
		CurrentPrefix: testPrefix,
		Statuses: statusMap(map[string]mediaregistry.ProjectionStatus{
			active:       mediaregistry.ProjectionActive,
			rollback:     mediaregistry.ProjectionRetired,
			olderRetired: mediaregistry.ProjectionRetired,
		}),
	}

	first, err := policy.Decide(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := policy.Decide(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Decide is not deterministic: first=%+v second=%+v", first, second)
	}

	// Simulate the post-sweep state: the dropped collection is gone. A
	// second sweep must drop nothing.
	post := Input{
		Collections:   []string{active, rollback},
		ActiveTarget:  active,
		CurrentPrefix: testPrefix,
		Statuses: statusMap(map[string]mediaregistry.ProjectionStatus{
			active:   mediaregistry.ProjectionActive,
			rollback: mediaregistry.ProjectionRetired,
		}),
	}
	again, err := policy.Decide(post)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Drop) != 0 {
		t.Fatalf("second sweep must drop nothing, got Drop=%v", again.Drop)
	}
}

// TestProjectionRetention_UnknownFailsClosed pins the fail-closed invariant:
// a prefix-matching collection with NO durable registry status is left
// untouched (never dropped, never counted in the keep tail).
func TestProjectionRetention_UnknownFailsClosed(t *testing.T) {
	active := testPrefix + "_20260814_071358_545816432"
	rollback := testPrefix + "_20260813_184719_687643250"
	unknown := testPrefix + "_orphan_without_registry_row"

	plan, err := (ProjectionRetentionPolicy{KeepLastN: 2, RetentionDays: 1}).Decide(Input{
		Collections:   []string{active, rollback, unknown},
		ActiveTarget:  active,
		CurrentPrefix: testPrefix,
		Statuses: statusMap(map[string]mediaregistry.ProjectionStatus{
			active:   mediaregistry.ProjectionActive,
			rollback: mediaregistry.ProjectionRetired,
			// unknown is intentionally absent from the status map.
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	if contains(plan.Drop, unknown) {
		t.Fatalf("unknown collection %q must never be dropped (fail-closed); Drop=%v", unknown, plan.Drop)
	}
	if contains(plan.Keep, unknown) {
		t.Fatalf("unknown collection %q must not be counted in Keep (left untouched); Keep=%v", unknown, plan.Keep)
	}
	if len(plan.Drop) != 0 {
		t.Fatalf("Drop = %v, want empty (only unknown + protected rollback remain)", plan.Drop)
	}
}
