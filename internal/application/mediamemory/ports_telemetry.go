// internal/application/mediamemory/ports_telemetry.go —
// telemetry port implementations (Logger / Clock / MetricsSink
// noop + production defaults). Extracted from ports.go; no
// behavior change.
package mediamemory

import "time"

// noopLogger swallows every call. Used when callers pass nil and
// in tests where noise must be zero. Mirrors search.noopLogger.
type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Error(string, ...any) {}

// NoopLogger returns a Logger that drops every message. Convenience
// for tests.
func NoopLogger() Logger { return noopLogger{} }

// realClock delegates to time.Now.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// RealClock returns a Clock backed by time.Now. Composition-root uses
// this in production; tests inject a fake.
func RealClock() Clock { return realClock{} }

// noopMetrics drops every metric. Used in unit tests.
type noopMetrics struct{}

func (noopMetrics) IncCounter(string, ...string)                {}
func (noopMetrics) ObserveHistogram(string, float64, ...string) {}

// NoopMetrics returns a MetricsSink that drops every observation.
func NoopMetrics() MetricsSink { return noopMetrics{} }
