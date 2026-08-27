package middleware

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
