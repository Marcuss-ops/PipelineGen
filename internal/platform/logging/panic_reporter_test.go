package logger

import (
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestPanicReporterEmitsOneStructuredErrorWithStack pins the shape operators
// and alerting depend on: a recovered goroutine panic must arrive as ONE error
// entry carrying the goroutine, the panic value, the process-wide count and the
// stack — not an unstructured stderr line.
func TestPanicReporterEmitsOneStructuredErrorWithStack(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	reporter := PanicReporter(zap.New(core))

	const stack = "goroutine 42 [running]:\nmain.run()\n\t/app/main.go:12 +0x1"
	reporter("worker-7", "boom", []byte(stack))

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want exactly 1", len(entries))
	}
	entry := entries[0]
	if entry.Level != zapcore.ErrorLevel {
		t.Fatalf("level = %s, want error", entry.Level)
	}
	if entry.Message != "recovered goroutine panic" {
		t.Fatalf("message = %q", entry.Message)
	}
	if !strings.HasPrefix(entry.LoggerName, "panic") {
		t.Fatalf("logger name = %q, want it under the panic scope", entry.LoggerName)
	}

	fields := entry.ContextMap()
	if fields["goroutine"] != "worker-7" {
		t.Fatalf("goroutine field = %#v, want worker-7", fields["goroutine"])
	}
	if fields["panic"] != "boom" {
		t.Fatalf("panic field = %#v, want the panic value", fields["panic"])
	}
	if _, ok := fields["panics_recovered_total"]; !ok {
		t.Fatalf("entry is missing the recovered-panic count: %#v", fields)
	}
	if got, _ := fields["stack"].(string); got != stack {
		t.Fatalf("stack field = %#v, want the captured stack", fields["stack"])
	}
}

// TestPanicReporterToleratesNilLogger keeps the installer safe on a partial
// deploy (a caller that has no logger yet must not panic the process while
// reporting a panic).
func TestPanicReporterToleratesNilLogger(t *testing.T) {
	reporter := PanicReporter(nil)
	if reporter == nil {
		t.Fatal("PanicReporter(nil) must still return an installable sink")
	}
	reporter("worker-0", "boom", []byte("stack"))
}
