package config

import (
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// Canonical loopback spellings and endpoint construction.
//
// Every default endpoint that points at a sidecar on this machine must be
// built from these values instead of repeating the literal. The literal lives
// once, in pkg/defaults, because internal/platform/config already depends on
// that package; re-exporting it here keeps callers on one authority without an
// import cycle. Regression gate: loopback_literals_gate_test.go.
const (
	// LoopbackHost is the IPv4 loopback literal used by every local sidecar.
	LoopbackHost = defaults.DefaultHost
	// LoopbackHostname is the hostname spelling accepted by local clients.
	LoopbackHostname = "localhost"
	// LoopbackURLPrefix is "http://127.0.0.1:" and is const-safe, so
	// compile-time endpoint constants can concatenate their port.
	LoopbackURLPrefix = "http://" + defaults.DefaultHost + ":"
)

// LoopbackBaseURL returns the canonical http://127.0.0.1:<port> endpoint.
func LoopbackBaseURL(port int) string {
	return LoopbackURLPrefix + strconv.Itoa(port)
}

// IsLoopbackHost reports whether host names this machine. It accepts the
// spellings used by local sidecars, an optional port, and the RFC 6761
// *.localhost names; it does not accept "::" or an empty host.
func IsLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if strings.HasPrefix(h, "[") {
		if end := strings.LastIndex(h, "]"); end > 0 {
			h = h[1:end]
		}
	} else if strings.Count(h, ":") == 1 {
		if hostPart, portPart, ok := strings.Cut(h, ":"); ok {
			if _, err := strconv.Atoi(portPart); err == nil {
				h = hostPart
			}
		}
	}
	switch h {
	case LoopbackHost, LoopbackHostname, "::1":
		return true
	}
	return strings.HasSuffix(h, "."+LoopbackHostname)
}
