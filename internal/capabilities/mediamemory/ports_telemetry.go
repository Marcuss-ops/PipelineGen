// Package mediamemory — ports_telemetry.go: telemetry port implementations
// (noopLogger, realClock, noopMetrics).
package mediamemory

import "time"

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Error(string, ...any) {}

// NoopLogger returns a Logger that drops every message.
func NoopLogger() Logger { return noopLogger{} }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// RealClock returns a Clock backed by time.Now.
func RealClock() Clock { return realClock{} }

type noopMetrics struct{}

func (noopMetrics) IncCounter(string, ...string)                {}
func (noopMetrics) ObserveHistogram(string, float64, ...string) {}

// NoopMetrics returns a MetricsSink that drops every observation.
func NoopMetrics() MetricsSink { return noopMetrics{} }
