package config

import "strings"

// IsLoopbackBindHost reports whether a server bind host can only be reached
// from this machine.
//
// It exists because some postures must be decided by the LISTENER'S
// reachability rather than by request metadata or by gin mode: a process behind
// a local reverse proxy sees the proxy's loopback address as the peer, so an
// in-handler loopback check cannot tell a proxied public request from a local
// one. The /metrics posture (internal/platform/httpserver) keys on this
// predicate for exactly that reason — verified 2026-09-22 against a remote
// master that answered `200` to an unauthenticated `GET /metrics`.
//
// Anything that is not an explicit loopback spelling answers false, including
// the empty host and "::" (which net.Listen reads as "all interfaces"), so
// callers err towards requiring authentication.
//
// This is deliberately NOT the same question as the public-interface rule in
// (*Config).Validate, which decides whether disabling auth is allowed and
// accepts a specific non-loopback IP. The two must not be merged: that rule
// gates a start-up refusal, this one gates what an already-running server
// exposes.
func IsLoopbackBindHost(host string) bool {
	switch strings.TrimSpace(host) {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}
