// Package app — central-Register composition gate (Blocco C1-Step 2).
//
// This is the FORWARD-PROTECTION gate that enforces the Blocco C1
// Capability Standard invariant: NO production file in
// internal/app/** other than capability_registry.go may contain a
// literal `(<api|module|jobs|providers>)\.Registry\.Register`
// substring (the typed prefix is required to distinguish the
// legitimate per-file variable-name call shape — `register Module`
// after a parameter named `registry` — from an actual
// TypedRegistry.Register(...) call that bypasses the canonical
// composition point).
//
// The gate uses ripgrep for fast + deterministic scanning (a parallel
// AST-based approach is documented as a future enhancement in
// ARCHITECTURE.md §11.5 — the team accepted ripgrep's residual
// reflection-bypass risk in exchange for fast +217-line gate).
//
// Behaviour:
//   - rg exit 1 (no matches) → PASS.
//   - rg exit 0 (matches found) → FAIL with the offending lines.
//   - rg exit 2 (ripgrep error) → FAIL with the error.
//   - rg not installed → SKIP with t.Skipf.
//
// To allow a NEW transient migration PR that legitimately needs
// a typed prefix outside capability_registry.go, edit the
// `forbiddenPattern` regex below (last resort; reviewed per PR).
package capabilities

import (
	"os/exec"
	"testing"
)

// forbiddenPattern matches any literal `(<typed>).Registry.Register`
// where <typed> is one of the four canonical registry-instance
// type prefixes used in the codebase today: api, module, jobs,
// providers. The regex requires the .Register suffix to terminate
// at a word boundary (so `RegisterAnything` with no boundary
// after `Register` does NOT match — that's intentional: future
// `RegisterSearch`/`RegisterFetch`/etc. method names are allowed
// because they have a different semantic, not a duplicate of the
// `Register` mutation surface).
//
// Source: written as a Go raw-string literal so backslashes are
// passed through unchanged to ripgrep's --regexp flag. Test files
// themselves are excluded by the `-g '!*_test.go'` filter below.
const forbiddenPattern = `\b(api|module|jobs|providers)\.Registry\.Register\b`

// TestRegisterGate_NoDirectRegistryRegisterOutsideCapabilityRegistry
// is the Blocco C1-Step 2 forward-protection gate (see package
// doc above). Walks internal/app/**.go with ripgrep, EXCLUDING:
//   - *_test.go               (test files reference the patterns by design)
//   - capability_registry.go  (the canonical single composition point)
//
// Fails the build if any line in the remaining files matches
// forbiddenPattern.
func TestRegisterGate_NoDirectRegistryRegisterOutsideCapabilityRegistry(t *testing.T) {
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		t.Skipf(
			"ripgrep not on PATH (install via `apt-get install ripgrep` or `brew install ripgrep`); "+
				"skipping gate: %v", err)
	}

	args := []string{
		"--no-heading",
		"--line-number",
		// Ripgrep evaluates globs left-to-right and the LAST
		// matching glob wins. Include *.go first, then apply
		// the explicit exclusions so they take precedence.
		"-g", "*.go",
		"-g", "!capability_registry.go",
		"-g", "!capability_registry_gate_test.go",
		"-g", "!*_test.go",
		"--regexp", forbiddenPattern,
		".",
	}
	cmd := exec.Command(rgPath, args...)
	out, err := cmd.CombinedOutput()
	outStr := string(out)

	if err == nil {
		// ripgrep exit 0 = matches found → GATE VIOLATION.
		t.Fatalf(
			"Blocco C1-Step 2 gate violation: typed-punctuated Registry.Register("+
				") call(s) found in internal/app/** OUTSIDE capability_registry.go.\n\n"+
				"Forbidden pattern: %q\n\n"+
				"Matched lines:\n%s\n\n"+
				"Remediation:\n"+
				"  1. Move the call into capability_registry.go's registerProviders /\n"+
				"     registerHTTPModules / registerJobs path, OR\n"+
				"  2. Wrap the call behind a typed port interface (AGENTS.md Pattern 0),\n"+
				"     OR\n"+
				"  3. If legitimately a transient migration, edit forbiddenPattern above\n"+
				"     (last resort, reviewed per PR).\n",
			forbiddenPattern, outStr)
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("ripgrep returned unexpected error type: %v\nOutput:\n%s", err, outStr)
	}

	// exit 1 = no matches = PASS. exit 2+ = real ripgrep error.
	if exitErr.ExitCode() == 2 {
		t.Fatalf("ripgrep error (exit %d):\n%s", exitErr.ExitCode(), outStr)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("ripgrep unexpected exit code %d:\n%s", exitErr.ExitCode(), outStr)
	}
	// exit 1 ⇒ gate PASS
}
