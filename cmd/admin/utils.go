// cmd/admin/utils.go — fail-closed config loader for the admin CLI.
//
// Phase 3 audit #4 (fix/admin-config-fail-closed, June 2026):
// production-critical admin subcommands MUST load their config via an
// explicit --app-config <path> flag. Silent fallback to defaults+env
// (the legacy config.Get() behaviour) is banned — operators used to
// think their pipelinegen config was loaded when it was actually
// replaced by defaults, which silently routed destructive ops
// (backup, restore, migrate, rotate, backfill, reset, drive-upload)
// against the wrong database.
//
// Exit code 2 is canonical for "the CLI refused to start because the
// config could not be loaded or validated". Distinguishes from exit
// code 1 which is reserved for runtime errors after config accepted.
// Mirrors the convention used by git (2 = usage error), bazel, kubectl
// and other Go CLIs; operators can grep stderr for the canonical
// "admin: error" prefix to distinguish from runtime errors.
//
// Ephemeral exception: cmd/admin/gen_api_docs.go constructs its
// config inline (a test-grade *Config literal seeded with feature
// flags needed to enumerate routes). It does NOT mutate prod
// data — it only wires the API router enough to extract routes for
// doc generation — so it is exempted from the --app-config
// requirement and is documented at the call site.

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ExitCodeConfigError signals "the CLI refused to start because the
// --app-config flag was missing, the file did not exist, the YAML
// was malformed, or cfg.Validate() failed". Operators / CI can grep
// stderr for the canonical "admin: error" prefix to distinguish from
// runtime errors (exit 1).
const ExitCodeConfigError = 2

// appConfigFlag is the canonical flag name for the PipelineGen
// config.yaml passed into an admin subcommand. Kept distinct from
// "seed-channels"'s existing --config flag, which carries a
// channel-monitor JSON config (a different concept with the same
// name) — preserving back-compat avoids breaking any
// `admin seed-channels --config <channel_json>` invocations in
// scripts.
const appConfigFlag = "--app-config"

// tryLoadConfigFromArgs parses --app-config <path> out of args, then
// calls config.GetFromPath(path) + cfg.Validate(). Returns the
// loaded config + nil on success, or nil + a clear operator-friendly
// error on missing flag / missing file / malformed YAML / validation
// failure.
//
// This is the pure, testable core. The production-side caller
// (LoadConfigOrExit) wraps it with os.Exit on error so operators can
// rely on exit code 2 to detect "config not loaded" without parsing
// stderr.
//
// Fail-closed contract: every malformed-input path returns a
// meaningful error before any caller code runs. The audit goal is
// "an operator who supplies a broken config file SHOULD NOT see the
// admin command proceed against the default config" — this function
// is the single chokepoint that implements that contract.
func tryLoadConfigFromArgs(args []string) (*config.Config, error) {
	path := extractAppConfigPath(args)
	if path == "" {
		return nil, fmt.Errorf("missing required flag: %s <path> (point to a valid PipelineGen config.yaml)", appConfigFlag)
	}

	// Fail-closed on missing file: config.GetFromPath silently returns
	// defaults+env when the file is absent. The audit explicitly bans
	// that fallback for production-critical admin ops, so we stat
	// first and error before GetFromPath's silent-fallback kicks in.
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s file not found at %s (refusing to silently fall back to defaults)", appConfigFlag, path)
		}
		return nil, fmt.Errorf("%s path %s is not accessible: %w", appConfigFlag, path, err)
	}

	cfg, err := config.GetFromPath(path)
	if err != nil {
		// GetFromPath errors with "malformed" for YAML parse failures
		// and "cannot read" for filesystem errors. We rewrap with
		// the flag name so the operator knows which flag was at
		// fault when they see "admin: error loading config from
		// <path>: config file <path> is malformed: ...".
		return nil, fmt.Errorf("error loading config from %s (%s): %w", appConfigFlag, path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed for %s (%s): %w", appConfigFlag, path, err)
	}
	return cfg, nil
}

// LoadConfigOrExit is the production-side wrapper for
// tryLoadConfigFromArgs. On any error: prints "admin: <err>" to
// stderr (NOT stdout — stdout output may be parsed by operator
// tooling) and exits with ExitCodeConfigError. On success, returns
// the loaded config.
//
// All production-critical admin subcommands (those that mutate prod
// data via DB writes, Drive uploads, schema migrations, or folder
// resets) MUST call this instead of config.Get(). Ephemeral
// subcommands that don't mutate prod data (gen-api-docs, benchmark)
// are documented exceptions and may keep their inline/test-grade
// config initialisation.
func LoadConfigOrExit(args []string) *config.Config {
	cfg, err := tryLoadConfigFromArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "admin:", err)
		os.Exit(ExitCodeConfigError)
	}
	return cfg
}

// StripAppConfig returns args with --app-config <path> /
// --app-config=<path> removed. Used when the rest of the args are
// passed to a flag.NewFlagSet() that does not register --app-config.
// The admin dispatcher's FlagSet-based subcommands (db backup /
// check / migrations / rotate / status) need this — they parse OWN
// flags after --app-config has been consumed by LoadConfigOrExit, so
// the remaining slice must NOT carry the --app-config token
// (FlagSet would reject it as an unknown flag).
//
// Both forms (two-token: --app-config /path; single-token:
// --app-config=/path) are stripped.
//
// Convention: call LoadConfigOrExit(args) BEFORE fs.Parse(stripped)
// so the original --app-config is consumed by the config loader;
// the FlagSet sees only the operator's subcommand-specific flags.
// Reversed order would silently break on the first FlagSet-based
// subcommand because the FlagSet would error on --app-config as
// an unknown flag.
func StripAppConfig(args []string) []string {
	out := make([]string, 0, len(args))
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if a == appConfigFlag {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, appConfigFlag+"=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// extractAppConfigPath scans args for the canonical --app-config
// <path> pair and returns the path. Returns "" if absent. Recognises
// both the two-token form (--app-config /path) AND the
// single-token form (--app-config=/path) for parity with Go's
// testing flags and conventional CLI flag parsing.
func extractAppConfigPath(args []string) string {
	for i, arg := range args {
		if arg == appConfigFlag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, appConfigFlag+"=") {
			return strings.TrimPrefix(arg, appConfigFlag+"=")
		}
	}
	return ""
}
