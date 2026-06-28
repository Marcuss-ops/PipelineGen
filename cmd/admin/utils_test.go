// cmd/admin/utils_test.go — fail-closed config loader tests
//
// Sanity-pinned behaviour (Phase 3 audit #4, fix/admin-config-fail-closed):
//
//   - missing --app-config flag        → operator-friendly "missing" error
//   - --app-config to nonexistent path → "file not found" error (refuses silent fallback)
//   - --app-config to malformed YAML   → "error loading config" wrapper carrying yaml err
//   - --app-config to YAML failing cfg.Validate() → "config validation failed" wrapper
//   - --app-config to valid YAML       → returns *config.Config, nil error
//
// Both --app-config and --app-config=path (single-token form) are accepted.
//
// StripAppConfig behaviour: removes --app-config tokens (both forms)
// while leaving every other arg intact — so a downstream flag.NewFlagSet()
// does not reject them as unknown flags.
//
// IMPORTANT: LoadConfigOrExit (the production-side wrapper) calls os.Exit
// on error, so it is NOT unit-tested here — invoking it would terminate
// the test binary. The exit-code-2 contract is asserted below via the
// gnarly pattern of "the error message must include the expected
// substring", which is what operators / CI grep for on stderr.
//
// Net scope: this file pins the fail-closed contract; failures here
// mean an operator can run a destructive admin op against a
// silently-fallback config.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// canonical /etc/skel-style minimal-valid-config YAML. Every required
// field that cfg.Validate() complains about is filled:
//
//   - Server: port in [1..65535], read+write timeouts > 0
//   - External.OllamaURL: non-empty
//   - Security: EnableAuth=false, DeliveryHMACSecret ≥32 bytes,
//     AdminToken non-empty when EnableAuth=true (we keep auth OFF here)
//   - host: 127.0.0.1 (private) so the public-interface guard passes
//
// The same constant is used across happy-path tests; pin shape in the
// test bodies themselves if the validator grows new required fields.
const minimalValidYAML = `server:
  host: "127.0.0.1"
  port: 8000
  read_timeout: 600
  write_timeout: 600
external:
  ollama_url: "http://localhost:11434"
security:
  enable_auth: false
  delivery_hmac_secret: "0123456789abcdef0123456789abcdef"
`

const placeholderYAML = `server:
  host: "127.0.0.1"
  port: 8000
  read_timeout: 600
  write_timeout: 600
external:
  ollama_url: "http://localhost:11434"
security:
  enable_auth: true
  admin_token: "YOUR_ADMIN_TOKEN_HERE"
`

const emptyYAML = `server:
  port: 0
`

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestTryLoadConfigFromArgs_MissingFlag — operator forgot --app-config.
// This is the primary audit-#4 vector: every production-critical
// admin subcommand must REJECT invocations without an explicit
// --app-config <path>.
func TestTryLoadConfigFromArgs_MissingFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		args []string
	}{
		{"empty args", nil},
		{"only positional args", []string{"some-positional"}},
		{"only unrelated flag", []string{"--apply", "true"}},
		{"dashdash-prefix match only", []string{"--config", "/tmp/x.yaml"}}, // legacy --config for JSON is distinct
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			tt := tt
			t.Parallel()

			cfg, err := tryLoadConfigFromArgs(tt.args)
			if cfg != nil {
				t.Errorf("expected nil cfg on missing flag, got %+v", cfg)
			}
			if err == nil {
				t.Fatal("expected error on missing --app-config flag, got nil")
			}
			if !strings.Contains(err.Error(), "missing required flag") {
				t.Errorf("error must mention %q; got %q", "missing required flag", err.Error())
			}
			if !strings.Contains(err.Error(), "--app-config") {
				t.Errorf("error must mention %q (so operators see the fix); got %q", "--app-config", err.Error())
			}
		})
	}
}

// TestTryLoadConfigFromArgs_FileNotFound — operator pointed at a
// path that does NOT exist. The audit explicitly bans the
// silent-fallback to defaults that legacy config.Get() exhibited;
// this test pins the "fail loud, refuse to proceed" behaviour.
func TestTryLoadConfigFromArgs_FileNotFound(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "definitely-not-here.yaml")

	cfg, err := tryLoadConfigFromArgs([]string{"--app-config", missing})
	if cfg != nil {
		t.Errorf("expected nil cfg on missing file, got %+v", cfg)
	}
	if err == nil {
		t.Fatal("expected error on missing file, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error must surface %q (so operator knows path is the problem); got %q", "not found", err.Error())
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error must include the path %q so the operator can see which path failed; got %q", missing, err.Error())
	}
	// Defensive pinning: the error MUST NOT mention "configure
	// / default" — i.e. the silent-fallback path. If a future commit
	// reintroduces fallback, this assertion fires.
	if strings.Contains(err.Error(), "default") {
		t.Errorf("error must NOT contain %q (the audit bans silent fallback to defaults); got %q", "default", err.Error())
	}
}

// TestTryLoadConfigFromArgs_MalformedYAML — file exists with garbage
// content. yaml.Unmarshal surfaces an error; we wrap it in a
// canonical "error loading config from <flag>" message that names the
// flag + path + underlying yaml error.
func TestTryLoadConfigFromArgs_MalformedYAML(t *testing.T) {
	t.Parallel()
	// YAML with a tab-indentation inconsistency that gopkg.in/yaml.v3
	// refuses. (We don't try to trip every YAML rule — just one real
	// malformed shape.)
	body := "server:\n  port: 8000\n  read_timeout: \x00not_yaml\n"
	path := writeFile(t, "bad.yaml", body)

	cfg, err := tryLoadConfigFromArgs([]string{"--app-config", path})
	if cfg != nil {
		t.Errorf("expected nil cfg on malformed YAML, got %+v", cfg)
	}
	if err == nil {
		t.Fatal("expected error on malformed YAML, got nil")
	}
	if !strings.Contains(err.Error(), "error loading config") {
		t.Errorf("error must include %q; got %q", "error loading config", err.Error())
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error must include the path %q; got %q", path, err.Error())
	}
	if !strings.Contains(err.Error(), "--app-config") {
		t.Errorf("error must include the flag name %q; got %q", "--app-config", err.Error())
	}
}

// TestTryLoadConfigFromArgs_ValidationFail — file is parseable YAML
// but cfg.Validate() rejects it (placeholder admin token, in this
// test). Distinguish from the malformed case: error prefix differs
// because Validate runs after a successful Unmarshal.
func TestTryLoadConfigFromArgs_ValidationFail(t *testing.T) {
	t.Parallel()
	path := writeFile(t, "placeholder.yaml", placeholderYAML)

	cfg, err := tryLoadConfigFromArgs([]string{"--app-config", path})
	if cfg != nil {
		t.Errorf("expected nil cfg on validation failure, got %+v", cfg)
	}
	if err == nil {
		t.Fatal("expected error on cfg.Validate() failure, got nil")
	}
	if !strings.Contains(err.Error(), "config validation failed") {
		t.Errorf("error must include %q (distinct from malformed's prefix); got %q", "config validation failed", err.Error())
	}
	if !strings.Contains(err.Error(), "YOUR_ADMIN_TOKEN_HERE") {
		t.Errorf("error must surface the underlying Validate() reason (placeholder admin token rejected); got %q", err.Error())
	}
}

// TestTryLoadConfigFromArgs_EmptyYAMLValidationFail — operator
// provided --app-config to a file but the file is missing required
// fields. cfg.Validate() catches the empty Server.Port /
// External.OllamaURL. Sanity-check Validate is actually invoked
// (the audit requires it).
func TestTryLoadConfigFromArgs_EmptyYAMLValidationFail(t *testing.T) {
	t.Parallel()
	path := writeFile(t, "empty.yaml", emptyYAML)

	cfg, err := tryLoadConfigFromArgs([]string{"--app-config", path})
	if cfg != nil {
		t.Errorf("expected nil cfg on validation failure, got %+v", cfg)
	}
	if err == nil {
		t.Fatal("expected error when cfg.Validate() rejects minimal config, got nil")
	}
	if !strings.Contains(err.Error(), "config validation failed") {
		t.Errorf("error must include %q; got %q", "config validation failed", err.Error())
	}
}

// TestTryLoadConfigFromArgs_HappyPath — operator passes a valid
// --app-config <path>. We get a non-nil cfg back and no error.
// Pins the canonical "production path works".
func TestTryLoadConfigFromArgs_HappyPath(t *testing.T) {
	t.Parallel()
	path := writeFile(t, "good.yaml", minimalValidYAML)

	cfg, err := tryLoadConfigFromArgs([]string{"--app-config", path})
	if err != nil {
		t.Fatalf("unexpected error for valid config: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil cfg for valid config, got nil")
	}
	if cfg.Server.Port != 8000 {
		t.Errorf("server.port: got %d, want 8000", cfg.Server.Port)
	}
	if cfg.External.OllamaURL != "http://localhost:11434" {
		t.Errorf("external.ollama_url: got %q, want %q", cfg.External.OllamaURL, "http://localhost:11434")
	}
}

// TestExtractAppConfigPath — both forms (two-token and single-token)
// are accepted; positional args and --other flags are ignored.
func TestExtractAppConfigPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		args []string
		want string
	}{
		{"two-token", []string{"--app-config", "/etc/foo.yaml"}, "/etc/foo.yaml"},
		{"single-token", []string{"--app-config=/etc/foo.yaml"}, "/etc/foo.yaml"},
		{"first of many flags", []string{"--app-config", "/etc/a.yaml", "--apply", "true"}, "/etc/a.yaml"},
		{"positional after flag", []string{"--app-config", "/etc/b.yaml", "positional-arg"}, "/etc/b.yaml"},
		{"absent", []string{"--apply", "true"}, ""},
		{"empty", []string{}, ""},
		{"dashdash-only value", []string{"--app-config", "--"}, ""}, // second token is "--", not a path
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			tt := tt
			t.Parallel()
			if got := extractAppConfigPath(tt.args); got != tt.want {
				t.Errorf("extractAppConfigPath(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

// TestStripAppConfig — pinned behaviour for the helper that lets
// flag.NewFlagSet()-based subcommands (db backup / check /
// migrations / rotate / status) coexist with the mandatory
// --app-config <path> flag.
//
// Both forms of --app-config must be stripped exactly; every other
// arg (including sub-specific flags, positional args, and the
// --app-config value) must be preserved verbatim so fs.Parse sees
// what the operator passed minus the canonical config flag.
func TestStripAppConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		In   []string
		Want []string
	}{
		{
			desc: "two-token + db's own flags",
			In:   []string{"--app-config", "/etc/x.yaml", "--data-dir", "./data", "--out", "/tmp/bak"},
			Want: []string{"--data-dir", "./data", "--out", "/tmp/bak"},
		},
		{
			desc: "single-token + db's own flags",
			In:   []string{"--app-config=/etc/x.yaml", "--data-dir", "./data"},
			Want: []string{"--data-dir", "./data"},
		},
		{
			desc: "no app-config preserves all args",
			In:   []string{"--data-dir", "./data", "--apply"},
			Want: []string{"--data-dir", "./data", "--apply"},
		},
		{
			desc: "empty input",
			In:   nil,
			Want: []string{},
		},
		{
			desc: "only app-config strips to empty",
			In:   []string{"--app-config", "/etc/x.yaml"},
			Want: []string{},
		},
		{
			desc: "app-config with equals+value: single token consumed",
			In:   []string{"--app-config=/etc/x.yaml"},
			Want: []string{},
		},
		{
			desc: "positional arg preserved",
			In:   []string{"--app-config", "/etc/x.yaml", "positional"},
			Want: []string{"positional"},
		},
		{
			desc: "double-dash preserved as a no-op (real ops never pass it but the loop must not panic)",
			In:   []string{"--app-config", "/etc/x.yaml", "--"},
			Want: []string{"--"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			tt := tt
			t.Parallel()
			got := StripAppConfig(tt.In)
			if len(got) != len(tt.Want) {
				t.Fatalf("StripAppConfig(%v): length got %d want %d (got=%v)", tt.In, len(got), len(tt.Want), got)
			}
			for i, g := range got {
				if g != tt.Want[i] {
					t.Errorf("StripAppConfig(%v)[%d]: got %q, want %q (full got=%v)", tt.In, i, g, tt.Want[i], got)
				}
			}
		})
	}
}

// TestExitCodeConfigError — expose the constant so a future commit
// that flips it to 1 (collapsing the runtime/config distinction)
// trips this test and forces a deliberate review of the operator-facing
// matrix.
//
// Exit code 2 is the documented contract in the file header; CI can
// grep admin subcommands' stderr for "admin:" + exit-2 to detect
// "config refused" without parsing the message body.
func TestExitCodeConfigError(t *testing.T) {
	t.Parallel()
	if ExitCodeConfigError != 2 {
		t.Errorf("ExitCodeConfigError: got %d, want 2 (canonical 'config error' code; flip intentionally and update this test + audit doc)", ExitCodeConfigError)
	}
}
