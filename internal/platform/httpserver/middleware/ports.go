package middleware

import "go.uber.org/zap"

// EnvReader is the typed port for reading environment variables.
// The concrete implementation lives in adapters.go (osEnvReader);
// the port exists so the middleware layer never imports "os" directly.
type EnvReader interface {
	Getenv(key string) string
}

// noopEnvReader returns "" for every key. Used as a safe fallback in
// tests that don't need env-var sensitivity.
type noopEnvReader struct{}

func (noopEnvReader) Getenv(string) string { return "" }

var _ EnvReader = noopEnvReader{}

// StructuredLogger is the structured logging port consumed by middleware.
// The concrete implementation (the infrastructure zap logger) is
// wired via SetStructuredLogger from the composition root. The port exposes
// the three log levels the middleware actually uses; Debug and
// DPanic are intentionally omitted to keep the interface minimal.
//
// The port uses zap.Field for structured fields because the callers
// already construct zap fields inline (zap.String, zap.Bool, etc.)
// and converting to a key-value interface would add allocation
// overhead for no benefit. The concrete adapter is a thin wrapper
// that passes fields directly to zap.Logger.
type StructuredLogger interface {
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
}

// noopLogger is the zero-value fallback when SetLogger has not been
// called. It silently discards all log messages so test fixtures and
// partial compose-root builds don't panic on nil logger.
type noopLogger struct{}

func (noopLogger) Info(_ string, _ ...zap.Field)  {}
func (noopLogger) Warn(_ string, _ ...zap.Field)  {}
func (noopLogger) Error(_ string, _ ...zap.Field) {}

// mwLogger is the package-level logger used by all middleware
// functions. Tests that care about log output can swap it with
// SetStructuredLogger; production wiring calls SetStructuredLogger once at startup.
var mwLogger StructuredLogger = noopLogger{}

// SetStructuredLogger installs the concrete StructuredLogger implementation.
// Must be called before the first HTTP request. Not goroutine-safe by
// design — call once during server setup.
func SetStructuredLogger(l StructuredLogger) {
	if l != nil {
		mwLogger = l
	}
}
