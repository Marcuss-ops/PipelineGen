package defaults

// Canonical local-only server binding. Every package that needs the
// development default (CLI fallback, worker client, config resolver) must
// read these constants instead of repeating the literal.
//
// The struct tag in internal/platform/config/types_misc.go still spells the
// port as "8000" because struct tags cannot reference constants; that tag is
// the loader default, not a second source of truth for callers.
const (
	DefaultHost    = "127.0.0.1"
	DefaultPort    = "8000"
	DefaultBaseURL = "http://" + DefaultHost + ":" + DefaultPort
)
