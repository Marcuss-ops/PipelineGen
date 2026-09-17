// Package buildinfo is the ONE owner of the question every operator and every
// agent asks first: "am I actually running the code I just built?".
//
// Before this package the answer was spread across three unrelated
// conventions, none of which reached an HTTP surface:
//
//   - `-X main.buildVersion=... -X main.commitHash=...` injected by
//     make/build.mk and Dockerfile into symbols that NO Go file declared, so
//     the linker dropped them silently and the build stamped nothing;
//   - internal/app/workerruntime.Identity, an env-driven 4-tuple
//     (VELOX_WORKER_ID/NAME/VERSION) used only for worker registration;
//   - the RenderingGen worker registry columns (renderinggen_version,
//     chronon_version, overlay_schema_version) which exist ONLY in the other
//     module and are readable only through the queue's /workers endpoint.
//
// The result was measurable: certifying a change meant comparing
// /etc/systemd unit files, `ps` output, binary mtimes, port owners and the
// queue's worker table by hand, and a suspended stale worker could still
// claim the canary job. The cheapest way to make that question answerable is
// to make every process publish the SAME identity document on the health
// surface it already serves.
//
// Contract (JSON, stable — mirrored verbatim by RenderingGen's worker health):
//
//	"build": {
//	  "version":        "0.1.0",             // tagged/release version
//	  "git_commit":     "a1b2c3d4e5f6",      // short VCS revision
//	  "git_commit_full":"a1b2c3d4…",         // full VCS revision
//	  "git_dirty":      true,                // built from a modified tree
//	  "build_time":     "2026-09-17T10:11Z", // VCS commit time (UTC RFC3339)
//	  "binary_sha256":  "…64 hex…",          // digest of the running executable
//	  "binary_path":    "/usr/local/bin/…",  // resolved executable path
//	  "config_path":    "/etc/…/…yaml",      // config the process loaded
//	  "mode":           "all",               // runtime mode/subcommand
//	  "worker_id":      "host-1",            // instance identity
//	  "pid":            4424,
//	  "started_at":     "2026-09-17T08:42Z",
//	  "identity_hash":  "…16 hex…"           // stable digest of the tuple above
//	}
//
// Sources, in priority order:
//
//  1. -ldflags overrides (Version, GitCommit, BuildTime, BinarySHA256) — used
//     by release builds that are not built from a VCS checkout;
//  2. runtime/debug.BuildInfo VCS stamps (vcs.revision / vcs.time /
//     vcs.modified) — populated automatically by `go build` inside a
//     checkout, so the common `go build ./cmd/server` is self-describing with
//     no build-system cooperation at all;
//  3. honest empties. Nothing here is ever fabricated: a field that cannot be
//     determined is empty, and Identity.Complete() reports the verdict so a
//     certifier can refuse to certify an unstamped binary.
//
// identity_hash exists so a script can compare two processes with one string
// instead of field-by-field, and so a canary job can record the exact tuple
// that produced it.
package buildinfo

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// ── ldflags-overridable build stamps ────────────────────────────────────
//
// These are the ONLY symbols the build system should pass with -X. They are
// declared here (not in package main) so every binary in the module — server,
// worker, admin — shares one stamped identity. make/build.mk sets all four.
var (
	// Version is the release/tagged version, e.g. "1.4.0" or "dev".
	Version = "dev"
	// GitCommit is the VCS revision the binary was built from.
	GitCommit = ""
	// BuildTime is the RFC3339 build timestamp.
	BuildTime = ""
	// BinarySHA256 lets a build system pre-compute the executable digest when
	// the binary is cross-built and cannot hash itself.
	BinarySHA256 = ""
)

// shortCommitLen is the conventional short-revision length used by git, so
// the value operators copy from /health is the same one `git log --oneline`
// prints.
const shortCommitLen = 12

// processStartedAt is captured at package init: the closest honest answer to
// "when did this process start" available without an extra dependency.
var processStartedAt = time.Now().UTC()

// runtime holds the values only the composition root knows (the config file
// it actually loaded, the mode it was started in, the instance identity).
// It is process-lifecycle state set once from main, exactly like
// logging.Init — not domain state, and never read before SetRuntime.
var (
	runtimeMu  sync.RWMutex
	runtimeCfg runtimeInfo
)

type runtimeInfo struct {
	configPath string
	mode       string
	workerID   string
}

// RuntimeInfo is the bootstrap-supplied half of the identity.
type RuntimeInfo struct {
	ConfigPath string
	Mode       string
	WorkerID   string
}

// SetRuntime records the process-lifecycle identity. It is called once from
// the composition root right after flag parsing and is safe to call from
// tests that need a deterministic value. Empty arguments leave the previous
// value in place, so a caller can set only what it owns.
func SetRuntime(info RuntimeInfo) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if path := strings.TrimSpace(info.ConfigPath); path != "" {
		runtimeCfg.configPath = path
	}
	if mode := strings.TrimSpace(info.Mode); mode != "" {
		runtimeCfg.mode = mode
	}
	if id := strings.TrimSpace(info.WorkerID); id != "" {
		runtimeCfg.workerID = id
	}
}

// Runtime returns a copy of the bootstrap-supplied identity.
func Runtime() RuntimeInfo {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return RuntimeInfo{
		ConfigPath: runtimeCfg.configPath,
		Mode:       runtimeCfg.mode,
		WorkerID:   runtimeCfg.workerID,
	}
}

// Identity is the stable JSON document every process publishes under
// /health and /ready as the "build" object.
//
// Every key is ALWAYS present (no omitempty): a stable document is what lets
// a certifier and an operator script assert fields directly instead of
// branching on key absence, and it keeps "unknown" distinguishable from
// "absent".
type Identity struct {
	Version       string `json:"version"`
	GitCommit     string `json:"git_commit"`
	GitCommitFull string `json:"git_commit_full"`
	GitDirty      bool   `json:"git_dirty"`
	BuildTime     string `json:"build_time"`
	BinarySHA256  string `json:"binary_sha256"`
	BinaryPath    string `json:"binary_path"`
	ConfigPath    string `json:"config_path"`
	Mode          string `json:"mode"`
	WorkerID      string `json:"worker_id"`
	PID           int    `json:"pid"`
	StartedAt     string `json:"started_at"`
	IdentityHash  string `json:"identity_hash"`
}

// Complete reports whether the identity is strong enough to certify a change:
// a version, a VCS revision and a binary digest. Without all three, "is this
// the binary I built?" is unanswerable, which is the failure this package
// exists to remove.
func (i Identity) Complete() bool {
	return strings.TrimSpace(i.Version) != "" &&
		strings.TrimSpace(i.GitCommit) != "" &&
		strings.TrimSpace(i.BinarySHA256) != ""
}

// Digest is the canonical short fingerprint of the identity tuple. Two
// processes rendering the same string are running identical bytes from the
// same revision with the same config — the one-string compare a certifier
// needs. It is derived from the tuple, never from the clock.
func (i Identity) Digest() string {
	sum := digest.SHA256Bytes([]byte(strings.Join([]string{
		i.Version, i.GitCommitFull, strconv.FormatBool(i.GitDirty),
		i.BuildTime, i.BinarySHA256, i.ConfigPath, i.Mode, i.WorkerID,
	}, "\x00")))
	if len(sum) <= 16 {
		return sum
	}
	return sum[:16]
}

// Current assembles the current process identity. It never fails and never
// blocks on I/O other than the one-time executable digest, so it is safe to
// call from a per-request health handler.
func Current() Identity {
	vcsRevision, vcsTime, vcsModified := vcsStamp()

	identity := Identity{
		Version:       nonEmpty(Version, "dev"),
		GitCommitFull: nonEmpty(GitCommit, vcsRevision),
		GitDirty:      vcsModified,
		BuildTime:     nonEmpty(BuildTime, vcsTime),
		BinarySHA256:  nonEmpty(BinarySHA256, executableDigest()),
		BinaryPath:    executablePath(),
		PID:           os.Getpid(),
		StartedAt:     processStartedAt.Format(time.RFC3339),
	}

	boot := Runtime()
	identity.ConfigPath = boot.ConfigPath
	identity.Mode = boot.Mode
	identity.WorkerID = boot.WorkerID

	if identity.GitCommitFull != "" {
		identity.GitCommit = shortCommit(identity.GitCommitFull)
	}
	identity.IdentityHash = identity.Digest()
	return identity
}

// vcsStamp reads the VCS stamps `go build` bakes into the executable when it
// runs inside a checkout. All three values are empty for a build without VCS
// metadata — that absence is reported, never guessed.
func vcsStamp() (revision, commitTime string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return "", "", false
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = strings.TrimSpace(setting.Value)
		case "vcs.time":
			commitTime = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	return revision, commitTime, modified
}

// executableDigestCache memoizes the executable digest: the file is immutable
// for the lifetime of the process, and hashing a ~80 MiB binary on every
// /health poll would make the endpoint expensive for no new information.
var executableDigestCache struct {
	once sync.Once
	sum  string
}

// executableDigest returns the SHA-256 of the running executable, or "" when
// it cannot be read (e.g. a deleted binary). A missing digest is reported as
// missing: silently substituting a previous value would reintroduce exactly
// the "looks verified, is not" failure mode this package removes.
func executableDigest() string {
	executableDigestCache.once.Do(func() {
		path, err := os.Executable()
		if err != nil || strings.TrimSpace(path) == "" {
			return
		}
		sum, _, err := digest.SHA256File(path)
		if err != nil {
			return
		}
		executableDigestCache.sum = sum
	})
	return executableDigestCache.sum
}

// executablePath resolves the executable path without failing the caller.
func executablePath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return path
}

// shortCommit truncates a revision to the conventional short form. Shorter
// revisions are returned unchanged.
func shortCommit(revision string) string {
	if len(revision) <= shortCommitLen {
		return revision
	}
	return revision[:shortCommitLen]
}

// nonEmpty returns primary when it carries content, else fallback.
func nonEmpty(primary, fallback string) string {
	if trimmed := strings.TrimSpace(primary); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(fallback)
}
