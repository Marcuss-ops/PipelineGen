package concurrent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// panicOnPurpose is the deepest user frame of the panics raised below. The
// stack assertions look for it by NAME: a trace that does not name the failing
// user function would be useless to whoever reads the alert.
func panicOnPurpose(what string) {
	panic(what)
}

// captureReporter records every report it receives and signals on a channel so
// tests never sleep-and-hope.
type captureReporter struct {
	mu      sync.Mutex
	reports []panicReport
	ch      chan panicReport
}

type panicReport struct {
	goroutine string
	recovered any
	stack     string
}

func newCaptureReporter() *captureReporter {
	return &captureReporter{ch: make(chan panicReport, 8)}
}

func (c *captureReporter) report(goroutine string, recovered any, stack []byte) {
	rep := panicReport{goroutine: goroutine, recovered: recovered, stack: string(stack)}
	c.mu.Lock()
	c.reports = append(c.reports, rep)
	c.mu.Unlock()
	select {
	case c.ch <- rep:
	default:
	}
}

func (c *captureReporter) wait(t *testing.T) panicReport {
	t.Helper()
	select {
	case rep := <-c.ch:
		return rep
	case <-time.After(2 * time.Second):
		t.Fatal("no panic report received")
		return panicReport{}
	}
}

func (c *captureReporter) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reports)
}

// TestSetPanicReporterReceivesPanicValueAndStack pins the contract the
// composition roots depend on: a panic inside a fire-and-forget goroutine is
// invisible to the caller, so the installed sink must receive the goroutine
// name, the panic value AND the stack of the goroutine that died.
func TestSetPanicReporterReceivesPanicValueAndStack(t *testing.T) {
	reporter := newCaptureReporter()
	SetPanicReporter(reporter.report)
	t.Cleanup(func() { SetPanicReporter(nil) })
	before := PanicsRecovered()

	SafeGo("test-panic-goroutine", func() { panicOnPurpose("boom") })

	rep := reporter.wait(t)
	if rep.goroutine != "test-panic-goroutine" {
		t.Fatalf("goroutine = %q, want the caller-supplied name", rep.goroutine)
	}
	if got, _ := rep.recovered.(string); got != "boom" {
		t.Fatalf("recovered = %#v, want the panic value \"boom\"", rep.recovered)
	}
	if strings.TrimSpace(rep.stack) == "" {
		t.Fatal("stack is empty: a recovered panic without a stack is undiagnosable")
	}
	if !strings.Contains(rep.stack, "panicOnPurpose") {
		t.Fatalf("stack does not name the failing user function:\n%s", rep.stack)
	}
	if !strings.Contains(rep.stack, "test-panic-goroutine") && !strings.Contains(rep.stack, "SafeGo") {
		t.Fatalf("stack does not point at the goroutine that ran the work:\n%s", rep.stack)
	}
	if got := PanicsRecovered(); got != before+1 {
		t.Fatalf("PanicsRecovered() = %d, want %d (the counter is maintained independently)", got, before+1)
	}
}

// TestGroupGoPanicIsReportedToTheSinkToo pins that the SECOND recovery path
// (Group.Go, which additionally converts the panic into an error) also reaches
// the installed sink — otherwise half the process' recovered panics would still
// degrade to an unstructured stderr line.
func TestGroupGoPanicIsReportedToTheSinkToo(t *testing.T) {
	reporter := newCaptureReporter()
	SetPanicReporter(reporter.report)
	t.Cleanup(func() { SetPanicReporter(nil) })

	group, _ := WithContext(context.Background())
	group.Go("test-group-panic", func() error { panicOnPurpose("group boom"); return nil })
	err := group.Wait()

	if err == nil || !strings.Contains(err.Error(), "group boom") {
		t.Fatalf("Group.Wait() = %v, want the panic surfaced as an error", err)
	}
	rep := reporter.wait(t)
	if rep.goroutine != "test-group-panic" {
		t.Fatalf("goroutine = %q, want test-group-panic", rep.goroutine)
	}
	if !strings.Contains(rep.stack, "panicOnPurpose") {
		t.Fatalf("group stack does not name the failing function:\n%s", rep.stack)
	}
}

// TestSetPanicReporterNilRestoresTheDefaultSink pins the documented rollback:
// passing nil stops reports (the stdlib logger takes over) WITHOUT stopping the
// counter — an operator can always read the anomaly count even with no sink
// installed, which is what makes the count trustworthy.
func TestSetPanicReporterNilRestoresTheDefaultSink(t *testing.T) {
	reporter := newCaptureReporter()
	SetPanicReporter(reporter.report)
	SetPanicReporter(nil)
	t.Cleanup(func() { SetPanicReporter(nil) })

	before := PanicsRecovered()
	SafeGo("test-panic-no-sink", func() { panicOnPurpose("silent") })

	// The counter is incremented by the recovery path itself, so waiting for it
	// proves the panic was handled before we assert that NO sink saw it (a
	// plain sleep would be a race, and synchronising inside the panicking
	// closure is impossible: the panic aborts it).
	deadline := time.Now().Add(2 * time.Second)
	for PanicsRecovered() != before+1 {
		if time.Now().After(deadline) {
			t.Fatalf("PanicsRecovered() stayed at %d, want %d", PanicsRecovered(), before+1)
		}
		time.Sleep(time.Millisecond)
	}
	if n := reporter.len(); n != 0 {
		t.Fatalf("reporter received %d report(s) after SetPanicReporter(nil)", n)
	}
}
