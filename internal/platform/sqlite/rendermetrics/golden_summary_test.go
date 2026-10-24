// golden_summary_test.go pins the golden per-class metric definition:
// per-class service statistics, the class share, the window capacity and the
// no-fabrication rule for unmeasured populations.
package rendermetrics

import (
	"testing"
)

func TestBuildGoldenSummary_PerClassServiceStats(t *testing.T) {
	samples := []ItemSample{
		{ItemID: "m1", Class: "map", Status: "COMPLETED", RenderMS: 40000, StartedAt: 1000, EndedAt: 41000},
		{ItemID: "p1", Class: "text_phrase", Status: "COMPLETED", RenderMS: 2000, StartedAt: 1000, EndedAt: 3000},
		{ItemID: "p2", Class: "text_phrase", Status: "COMPLETED", RenderMS: 4000, StartedAt: 3000, EndedAt: 7000},
		{ItemID: "i1", Class: "image", Status: "FAILED", RenderMS: 0},
	}
	s := BuildGoldenSummary(samples)

	if s.Items != 4 || s.Completed != 3 {
		t.Fatalf("items=%d completed=%d, want 4/3", s.Items, s.Completed)
	}
	if got := s.ClassCounts["text_phrase"]; got != 2 {
		t.Fatalf("class_counts[text_phrase] = %d, want 2", got)
	}
	if got := s.StatusCounts["FAILED"]; got != 1 {
		t.Fatalf("status_counts[FAILED] = %d, want 1", got)
	}

	phrase := s.PerClass["text_phrase"]
	if phrase.Items != 2 || phrase.Completed != 2 {
		t.Fatalf("phrase class = %+v, want 2 items completed", phrase)
	}
	if phrase.ServiceMS.Count != 2 || phrase.ServiceMS.AvgMS != 3000 ||
		phrase.ServiceMS.P50MS != 3000 || phrase.ServiceMS.MinMS != 2000 ||
		phrase.ServiceMS.MaxMS != 4000 {
		t.Fatalf("phrase service = %+v, want avg/p50 3000", phrase.ServiceMS)
	}

	// Aggregate service is over the measured samples only (image RenderMS 0 is
	// unmeasured, never a fake 0 ms sample).
	if s.PerItemServiceMS.Count != 3 || s.TotalServiceMS != 46000 {
		t.Fatalf("aggregate = %+v total=%d, want 3 samples / 46000 ms",
			s.PerItemServiceMS, s.TotalServiceMS)
	}
	// Map share = 40000/46000 = 87.0%; phrase share = 6000/46000 = 13.0%.
	if got := s.PerClass["map"].ServiceSharePct; got != 87.0 {
		t.Fatalf("map share = %v, want 87.0", got)
	}
	if got := phrase.ServiceSharePct; got != 13.0 {
		t.Fatalf("phrase share = %v, want 13.0", got)
	}

	// Window: 1000 → 41000 = 40 s, 4 timestamped... only 3 samples carry both
	// timestamps (m1/p1/p2), so capacity = 3 items / 40 s = 4.5/min.
	if s.WindowSeconds != 40 {
		t.Fatalf("window = %v, want 40", s.WindowSeconds)
	}
	if s.ItemsPerMin != 4.5 {
		t.Fatalf("items_per_min = %v, want 4.5", s.ItemsPerMin)
	}
}

func TestBuildGoldenSummary_UnknownClassIsCountedNotDropped(t *testing.T) {
	s := BuildGoldenSummary([]ItemSample{
		{ItemID: "x", Status: "COMPLETED", RenderMS: 100},
	})
	if s.UnknownClass != 1 {
		t.Fatalf("unknown_class = %d, want 1", s.UnknownClass)
	}
	if _, ok := s.PerClass[unknownClass]; !ok {
		t.Fatalf("expected an %q entry, got %v", unknownClass, s.PerClass)
	}
}

func TestBuildGoldenSummary_UnmeasuredPopulationsStayAbsent(t *testing.T) {
	s := BuildGoldenSummary([]ItemSample{
		{ItemID: "a", Class: "map", Status: "FAILED"},
		{ItemID: "b", Class: "map", Status: "FAILED"},
	})
	if s.PerItemServiceMS.Count != 0 || s.PerItemServiceMS.MaxMS != 0 {
		t.Fatalf("service stat must be empty for unmeasured samples, got %+v",
			s.PerItemServiceMS)
	}
	if s.WindowSeconds != 0 || s.ItemsPerMin != 0 {
		t.Fatalf("window must stay absent without timestamps, got %v/%v",
			s.WindowSeconds, s.ItemsPerMin)
	}
}

func TestServiceStat_EvenMedianRounds(t *testing.T) {
	stat := serviceStat([]int64{1, 2, 4, 8})
	if stat.P50MS != 3 {
		t.Fatalf("p50 = %d, want 3 (average of 2 and 4)", stat.P50MS)
	}
	if stat.AvgMS != 4 {
		t.Fatalf("avg = %d, want 4", stat.AvgMS)
	}
}

func TestBuildGoldenSummary_CacheTimingsKeepMicrosecondsAndOutcomes(t *testing.T) {
	summary := BuildGoldenSummary([]ItemSample{
		{ItemID: "a", CacheLookupUS: 120, CacheProbeUS: 340, CacheStoreUS: 560, CacheOutcome: "miss_stored"},
		{ItemID: "b", CacheLookupUS: 220, CacheProbeUS: 0, CacheStoreUS: 760, CacheOutcome: "hit"},
		{ItemID: "c", CacheOutcome: "probe_error"},
	})
	if got := summary.CacheTiming.LookupUS; got.Count != 2 || got.AvgUS != 170 || got.P50US != 170 || got.MinUS != 120 || got.MaxUS != 220 {
		t.Fatalf("lookup timing = %+v", got)
	}
	if got := summary.CacheTiming.ProbeUS; got.Count != 1 || got.AvgUS != 340 {
		t.Fatalf("probe timing = %+v", got)
	}
	if got := summary.CacheTiming.StoreUS; got.Count != 2 || got.AvgUS != 660 || got.P50US != 660 {
		t.Fatalf("store timing = %+v", got)
	}
	if summary.CacheTiming.Outcomes["miss_stored"] != 1 || summary.CacheTiming.Outcomes["hit"] != 1 || summary.CacheTiming.Outcomes["probe_error"] != 1 {
		t.Fatalf("cache outcomes = %v", summary.CacheTiming.Outcomes)
	}
}
