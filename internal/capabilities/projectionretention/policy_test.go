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
