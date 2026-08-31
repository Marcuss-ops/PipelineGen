// File logger.go — narrow logging port + nop default impl for the
// resolver adapter. Extracted from adapter.go per AGENTS.md Pattern 5
// v2 (1 concetto per file; code-motion pura, zero logica cambiata).
//
// godlike/06 SSOT one-canonical-owner-per-fact: this interface lives ONLY
// here. Callers (NewAdapter + WithLogger in adapter.go) reference it
// by package-local name.
//
// godlike/07 minimum-blast-radius: a nil logger falls back to a
// no-op default so callers do not need to wire a real logger in
// every test fixture.
package resolver

// resolverLogger is a narrow logging port for resolver-specific
// observability. Mirrors internal/capabilities/assets/sourcing.Logger
// but kept package-local to avoid the import cycle.
//
// godlike/07 minimum-blast-radius: a nil logger falls back to a
// no-op default so callers do not need to wire a real logger in
// every test fixture.
type resolverLogger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
}

type nopResolverLogger struct{}

func (nopResolverLogger) Info(string, ...any)  {}
func (nopResolverLogger) Warn(string, ...any)  {}
func (nopResolverLogger) Error(string, ...any) {}
func (nopResolverLogger) Debug(string, ...any) {}
