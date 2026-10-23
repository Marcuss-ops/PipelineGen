package rendercacheinfo

import (
	"sync"
	"testing"
)

func TestCurrentDefaultsToNotWired(t *testing.T) {
	Reset()
	status := Current()
	if status.Enabled || status.Wired || status.Epoch != "" || status.Reason != "" {
		t.Fatalf("zero status = %+v, want an honest not-wired document", status)
	}
}

func TestSetRuntimeSnapshotsAndFillsTheCanonicalTable(t *testing.T) {
	Reset()
	defer Reset()
	SetRuntime(Status{Enabled: true, Wired: true, Epoch: "epoch-1"})

	got := Current()
	if !got.Enabled || !got.Wired || got.Epoch != "epoch-1" {
		t.Fatalf("status = %+v", got)
	}
	if got.Table != TableName {
		t.Fatalf("wired status table = %q, want %q", got.Table, TableName)
	}

	// A caller mutating the struct it passed in must not mutate the snapshot.
	original := Status{Enabled: true, Wired: true, Epoch: "epoch-1"}
	SetRuntime(original)
	original.Epoch = "mutated"
	if Current().Epoch != "epoch-1" {
		t.Fatal("SetRuntime must store a snapshot, not the caller's value")
	}
}

func TestReasonStringsNameTheActualCause(t *testing.T) {
	cases := map[string]string{
		"kill switch": ReasonKillSwitch,
		"missing db":  ReasonNoCacheDB,
		"not wired":   ReasonNotConfigured,
	}
	seen := map[string]bool{}
	for name, reason := range cases {
		if reason == "" {
			t.Fatalf("%s reason is empty", name)
		}
		if seen[reason] {
			t.Fatalf("reason %q is shared by two causes", reason)
		}
		seen[reason] = true
	}
}

func TestConcurrentReadersSeeAStableSnapshot(t *testing.T) {
	Reset()
	defer Reset()
	SetRuntime(Status{Enabled: true, Wired: true, Epoch: "epoch-1"})

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := Current(); got.Epoch != "epoch-1" {
				t.Errorf("concurrent snapshot = %+v", got)
			}
		}()
	}
	wg.Wait()
}
