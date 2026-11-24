package vidrush

import (
	"context"
	"sync"
	"testing"
)

type stubObserver struct{ id string }

func (o *stubObserver) OnSceneCommitted(context.Context, SceneCommitted) error { return nil }

func TestWiringRegistryIsolatesRuns(t *testing.T) {
	registry := NewWiringRegistry()
	a, b := &stubObserver{id: "a"}, &stubObserver{id: "b"}
	registry.Register("run-a", Wiring{Observer: a})
	registry.Register("run-b", Wiring{Observer: b})

	if got, ok := registry.Lookup("run-a"); !ok || got.Observer != a {
		t.Fatalf("run-a observer = %v, ok = %v; want a", got.Observer, ok)
	}
	if got, ok := registry.Lookup("run-b"); !ok || got.Observer != b {
		t.Fatalf("run-b observer = %v, ok = %v; want b", got.Observer, ok)
	}

	registry.Unregister("run-a")
	if _, ok := registry.Lookup("run-a"); ok {
		t.Fatal("run-a wiring survived Unregister")
	}
	if got, ok := registry.Lookup("run-b"); !ok || got.Observer != b {
		t.Fatal("run-b wiring did not survive run-a teardown")
	}
}

func TestWiringRegistryZeroAndNilAreSafe(t *testing.T) {
	var zero WiringRegistry
	zero.Register("run", Wiring{Observer: &stubObserver{id: "zero"}})
	if _, ok := zero.Lookup("run"); !ok {
		t.Fatal("zero-value registry did not register")
	}

	var nilRegistry *WiringRegistry
	nilRegistry.Register("run", Wiring{})
	if _, ok := nilRegistry.Lookup("run"); ok {
		t.Fatal("nil registry returned a run")
	}
	nilRegistry.Unregister("run")
}

func TestWiringRegistryConcurrentRuns(t *testing.T) {
	registry := NewWiringRegistry()
	const runs = 32
	var wg sync.WaitGroup
	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runID := "run"
			registry.Register(runID, Wiring{Observer: &stubObserver{id: runID}})
			registry.Lookup(runID)
			registry.Unregister(runID)
		}()
	}
	wg.Wait()
}
