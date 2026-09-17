package buildinfo

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIdentity_IsAlwaysAssembled pins the no-fake-availability contract: the
// identity document is always produced, and a field that cannot be determined
// is empty rather than invented.
func TestCurrent_IsAlwaysAssembled(t *testing.T) {
	identity := Current()

	if identity.Version == "" {
		t.Error("version must always fall back to a value, got empty")
	}
	if identity.PID <= 0 {
		t.Errorf("pid = %d, want > 0", identity.PID)
	}
	if strings.TrimSpace(identity.StartedAt) == "" {
		t.Error("started_at must always be populated")
	}
	if identity.IdentityHash == "" {
		t.Error("identity_hash must always be derived")
	}
}

// TestSetRuntime_FeedsIdentity is the bootstrap contract: whatever the
// composition root declares (config file, mode, instance id) is what /health
// publishes, so "which config is this process reading?" stops being a manual
// inspection of systemd units.
func TestSetRuntime_FeedsIdentity(t *testing.T) {
	previous := Runtime()
	t.Cleanup(func() { SetRuntime(previous) })

	SetRuntime(RuntimeInfo{ConfigPath: "/etc/pipelinegen/pipelinegen.yaml", Mode: "all", WorkerID: "host-1"})

	identity := Current()
	if identity.ConfigPath != "/etc/pipelinegen/pipelinegen.yaml" {
		t.Errorf("config_path = %q, want the loaded config", identity.ConfigPath)
	}
	if identity.Mode != "all" {
		t.Errorf("mode = %q, want %q", identity.Mode, "all")
	}
	if identity.WorkerID != "host-1" {
		t.Errorf("worker_id = %q, want %q", identity.WorkerID, "host-1")
	}
}

// TestSetRuntime_EmptyLeavesPreviousValue documents that an empty argument
// does not erase identity a previous bootstrap step owned.
func TestSetRuntime_EmptyLeavesPreviousValue(t *testing.T) {
	previous := Runtime()
	t.Cleanup(func() { SetRuntime(previous) })

	SetRuntime(RuntimeInfo{ConfigPath: "/tmp/a.yaml", Mode: "worker", WorkerID: "w-1"})
	SetRuntime(RuntimeInfo{ConfigPath: "  ", Mode: "", WorkerID: ""})

	got := Runtime()
	if got.ConfigPath != "/tmp/a.yaml" || got.Mode != "worker" || got.WorkerID != "w-1" {
		t.Errorf("empty SetRuntime overwrote identity: %+v", got)
	}
}

// TestDigest_IsStableAndFieldSensitive makes the canary comparison
// trustworthy: same tuple → same hash, any changed field → different hash.
func TestDigest_IsStableAndFieldSensitive(t *testing.T) {
	base := Identity{Version: "1.0.0", GitCommitFull: "abcdef012345", BinarySHA256: "aa", ConfigPath: "/etc/a.yaml", Mode: "all"}

	if base.Digest() != base.Digest() {
		t.Error("digest must be deterministic for the same tuple")
	}

	changed := base
	changed.BinarySHA256 = "bb"
	if base.Digest() == changed.Digest() {
		t.Error("a different binary digest must change identity_hash")
	}

	changed = base
	changed.ConfigPath = "/etc/b.yaml"
	if base.Digest() == changed.Digest() {
		t.Error("a different config path must change identity_hash")
	}

	if len(base.Digest()) != 16 {
		t.Errorf("identity_hash length = %d, want 16", len(base.Digest()))
	}
}

// TestComplete_RequiresRealStamps pins the certifier gate: a binary without a
// revision or a content digest must not be able to claim a complete identity.
func TestComplete_RequiresRealStamps(t *testing.T) {
	cases := []struct {
		name     string
		identity Identity
		want     bool
	}{
		{"all stamped", Identity{Version: "1.0.0", GitCommit: "abc123", BinarySHA256: "deadbeef"}, true},
		{"no revision", Identity{Version: "1.0.0", BinarySHA256: "deadbeef"}, false},
		{"no digest", Identity{Version: "1.0.0", GitCommit: "abc123"}, false},
		{"no version", Identity{GitCommit: "abc123", BinarySHA256: "deadbeef"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.identity.Complete(); got != tc.want {
				t.Errorf("Complete() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIdentity_JSONContract freezes the wire field names: scripts, the
// certifier and the other module's worker build all read these keys, so a
// rename is a breaking change to operator tooling.
func TestIdentity_JSONContract(t *testing.T) {
	raw, err := json.Marshal(Identity{Version: "0.1.0", GitCommit: "abc", PID: 7, StartedAt: "2026-09-17T00:00:00Z"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		"version", "git_commit", "git_dirty", "binary_sha256", "config_path",
		"pid", "started_at", "identity_hash",
	} {
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("identity JSON is missing the %q key: %s", key, raw)
		}
	}
}

// TestShortCommit matches git's own abbreviated form so an operator can paste
// the value from /health straight into `git show`.
func TestShortCommit(t *testing.T) {
	if got := shortCommit("a1b2c3d4e5f6071829"); got != "a1b2c3d4e5f6" {
		t.Errorf("shortCommit = %q, want %q", got, "a1b2c3d4e5f6")
	}
	if got := shortCommit("abc"); got != "abc" {
		t.Errorf("short revision must be returned unchanged, got %q", got)
	}
}

// TestNonEmpty keeps the "report absence honestly" rule: whitespace is absence.
func TestNonEmpty(t *testing.T) {
	if got := nonEmpty("  ", "fallback"); got != "fallback" {
		t.Errorf("nonEmpty(blank) = %q, want fallback", got)
	}
	if got := nonEmpty("value", "fallback"); got != "value" {
		t.Errorf("nonEmpty(value) = %q, want value", got)
	}
}
