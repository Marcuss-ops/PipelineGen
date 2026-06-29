// Package workerruntime — identity.go (P1-3, June 2026).
//
// The worker identity tuple is the canonical input to
// appjobs.RegisterWorkerCommand:
//
//   WorkerID     — $VELOX_WORKER_ID, fallback to os.Hostname()
//   WorkerName   — $VELOX_WORKER_NAME, fallback to WorkerID
//   Version      — $VELOX_WORKER_VERSION, fallback to "dev"
//   Hostname     — os.Hostname() (NOT env-overridable — Hostname
//                  is an infra signal, not an operator override)
//
// Env + Hostname are the CANONICAL public helpers — exported so
// cmd/worker/doctor_main.go (RW-PROD-016, also `package main`,
// separate file in cmd/worker/) can call them from outside this
// package boundary. The previous unexported envOr/hostnameOr were
// not reachable from doctor_main.go once the cmd/worker/main.go
// god-file collapsed to the slim entry — exporting them is the
// boundary fix the user mandated.
package workerruntime

import (
	"os"
	"strings"
)

// Identity is the canonical 4-tuple the worker ships at registration
// (P1-3, June 2026). It is the wire-shape that
// RegisterWorkerSession() forwards into
// appjobs.RegisterWorkerCommand.
type Identity struct {
	WorkerID   string
	WorkerName string
	Version    string
	Hostname   string
}

// WorkerIdentity reads the 4 env-driven identity slots and returns
// an Identity tuple. Falls back to hostname for WorkerID and
// WorkerID for WorkerName. Version falls back to "dev" (the
// canonical go-build `-ldflags` placeholder for unflagged builds).
func WorkerIdentity() Identity {
	id := Env("VELOX_WORKER_ID", Hostname("unknown"))
	name := Env("VELOX_WORKER_NAME", id)
	version := Env("VELOX_WORKER_VERSION", "dev")
	host := Hostname("unknown")
	return Identity{
		WorkerID:   id,
		WorkerName: name,
		Version:    version,
		Hostname:   host,
	}
}

// Env returns the trimmed env var value or fallback if unset.
// Exported so cmd/worker/doctor_main.go (and any future binary
// subcommand) can read env vars with the same trim semantics the
// worker uses. Pre-P1-3 helper renamed verbatim from cmd/worker/
// main.go::envOr.
func Env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Hostname returns the OS hostname (canonical infra signal) or
// `fallback` if the hostname lookup fails / returns empty.
// Exported for the same reason as Env above. Pre-P1-3 helper
// renamed verbatim from cmd/worker/main.go::hostnameFallback.
func Hostname(fallback string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return fallback
	}
	return host
}
