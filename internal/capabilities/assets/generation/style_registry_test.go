package assets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/styleerrors"
)

// testYAML writes a minimal styles YAML to a temp file and returns the path.
func testYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "styles.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test YAML: %v", err)
	}
	return path
}

// ── Step-1 (A1) audit pin ───────────────────────────────────────────────
//
// TestResolve_ProviderUnsupported and TestResolve_ModelUnsupported
// were retired earlier in surface-3 (July 2026) along with the
// underlying checks. The empty-fields path
// (TestResolve_EmptyProviderAndModel) below still exercises Resolve
// with empty inputs to confirm the function entry is reachable. The
// t.Skip stubs at the bottom of this file keep the surface-3
// retirement grep-able for downstream audits (godlike/06 audit-pin).

func TestResolve_EmptyStyleID(t *testing.T) {
	reg, err := NewStyleRegistry(testYAML(t, "styles: []"))
	if err != nil {
		t.Fatal(err)
	}

	// Step-1 (A1, July 2026): provider/model inputs are still
	// passed for call-shape compat, but the resolver no longer
	// reads them (the per-style allowlist checks were retired in
	// surface-3, A1 drops the remaining surface-1 references).
	resolved, err := reg.Resolve("", "google-slides", "nano-banana-pro")
	if err != nil {
		t.Fatalf("empty styleID should not error: %v", err)
	}
	if resolved.ID != "" || resolved.Version != 0 || resolved.PromptSuffix != "" {
		t.Fatalf("empty styleID should produce zero ResolvedStyle: got %+v", resolved)
	}
}

func TestResolve_ValidStyle(t *testing.T) {
	// Step-1 typed migration (A1, July 2026): fixture uses
	// `test-style` (no magic literal reuse). Description fall-back
	// is gone — prompt_suffix is the sole resolved suffix.
	yaml := `
styles:
  - name: "test-style"
    version: 2
    prompt_suffix: "test suffix, dramatic lighting"
    negative_prompt: "blurry, low quality"
    destination_key: "ai-images/test-style"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := reg.Resolve("test-style", "google-slides", "nano-banana-pro")
	if err != nil {
		t.Fatalf("valid style should resolve: %v", err)
	}
	if resolved.ID != "test-style" {
		t.Fatalf("ID = %q, want %q", resolved.ID, "test-style")
	}
	if resolved.Version != 2 {
		t.Fatalf("Version = %d, want 2", resolved.Version)
	}
	if resolved.PromptSuffix != "test suffix, dramatic lighting" {
		t.Fatalf("PromptSuffix = %q", resolved.PromptSuffix)
	}
	if resolved.NegativePrompt != "blurry, low quality" {
		t.Fatalf("NegativePrompt = %q", resolved.NegativePrompt)
	}
	if resolved.DestinationKey != "ai-images/test-style" {
		t.Fatalf("DestinationKey = %q", resolved.DestinationKey)
	}
}

func TestResolve_StyleNotFound(t *testing.T) {
	reg, err := NewStyleRegistry(testYAML(t, "styles: []"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = reg.Resolve("nonexistent", "google-slides", "")
	if !errors.Is(err, ErrStyleNotFound) {
		t.Fatalf("expected ErrStyleNotFound, got: %v", err)
	}
}

func TestResolve_DisabledStyle(t *testing.T) {
	yaml := `
styles:
  - name: "test-style"
    enabled: false
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	_, err = reg.Resolve("test-style", "google-slides", "")
	if !errors.Is(err, ErrStyleDisabled) {
		t.Fatalf("expected ErrStyleDisabled, got: %v", err)
	}
}

func TestResolve_EmptyProviderAndModel(t *testing.T) {
	// Step-1 typed migration (A1, July 2026): allowed_providers /
	// allowed_models fields dropped from StyleDefinition. The
	// underlying checks were already retired in surface-3; this cut
	// simply drops the now-no-op fixture fields.
	yaml := `
styles:
  - name: "test-style"
    prompt_suffix: "test"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	// Empty provider and model should still resolve cleanly
	// (post surface-3 the allowlist checks are dead so this is a
	// pure passthrough to the ResolvedStyle population step).
	resolved, err := reg.Resolve("test-style", "", "")
	if err != nil {
		t.Fatalf("empty provider/model should resolve: %v", err)
	}
	if resolved.ID != "test-style" {
		t.Fatalf("ID = %q", resolved.ID)
	}
}

func TestResolve_DestinationKeyFallback(t *testing.T) {
	yaml := `
styles:
  - name: "test-style"
    prompt_suffix: "test"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := reg.Resolve("test-style", "", "")
	if err != nil {
		t.Fatalf("should resolve: %v", err)
	}
	if resolved.DestinationKey != "ai-images/test-style" {
		t.Fatalf("DestinationKey fallback = %q, want %q", resolved.DestinationKey, "ai-images/test-style")
	}
}

// ── Step-2 (A2, July 2026) ApplyStyle fail-closed contract ───────────
//
// godlike/06 audit-pinning: the pre-A2 ApplyStyle test
// (TestApplyStyle_Deprecated) was retired as part of the
// fail-closed closure. The 7 TDD tests below pin every typed-error
// emission + the canonical success path. errors.Is is the dispatch
// contract (godlike/07 fail-closed typed-error surface) — the
// message text is informational only.
//
// commonApplyStyleRegistry builds a registry exercising the 4
// positive gates + the version=0 wildcard. The 5 styles cover the
// canonical ApplyStyle surface:
//   - apply-ok        (Version=2, Enabled=true, has PromptSuffix) — canonical success
//   - apply-disabled  (Enabled=false)                              — ErrStyleDisabled gate
//   - apply-nosuffix  (PromptSuffix empty)                         — ErrEmptyPrompt gate seed
//   - apply-curated   (Version=7, Enabled=true, has PromptSuffix)  — ErrStyleVersionMismatch gate
//   - apply-nokey     (DestinationKey empty)                       — DestinationKey fallback test
func commonApplyStyleRegistry(t *testing.T) *StyleRegistry {
	yaml := `
styles:
  - name: "apply-ok"
    version: 2
    prompt_suffix: "cinematic lighting, photorealistic"
    negative_prompt: "blurry, low quality"
    destination_key: "ai-images/apply-ok"
    enabled: true
  - name: "apply-disabled"
    prompt_suffix: "anything"
    destination_key: "ai-images/apply-disabled"
    enabled: false
  - name: "apply-nosuffix"
    destination_key: "ai-images/apply-nosuffix"
    enabled: true
  - name: "apply-curated"
    version: 7
    prompt_suffix: "test"
    destination_key: "ai-images/apply-curated"
    enabled: true
  - name: "apply-nokey"
    version: 1
    prompt_suffix: "suffix-for-nokey"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestApplyStyle_Success_ComposesPromptAndSuffix pins the canonical
// success path: prompt + PromptSuffix compose via canonical
// comma-join, all metadata fields populated, version=0 fits the
// wildcard (does NOT trigger ErrStyleVersionMismatch).
func TestApplyStyle_Success_ComposesPromptAndSuffix(t *testing.T) {
	reg := commonApplyStyleRegistry(t)
	composed, err := reg.ApplyStyle("a beautiful sunset", "apply-ok", 0)
	if err != nil {
		t.Fatalf("ApplyStyle valid input should succeed: %v", err)
	}
	if composed == nil {
		t.Fatal("ApplyStyle returned nil composed")
	}
	wantComposed := "a beautiful sunset, cinematic lighting, photorealistic"
	if composed.ComposedText != wantComposed {
		t.Fatalf("ComposedText = %q, want %q", composed.ComposedText, wantComposed)
	}
	if composed.StyleID != "apply-ok" {
		t.Fatalf("StyleID = %q, want %q", composed.StyleID, "apply-ok")
	}
	if composed.StyleVersion != 2 {
		t.Fatalf("StyleVersion = %d, want 2", composed.StyleVersion)
	}
	if composed.PromptSuffix != "cinematic lighting, photorealistic" {
		t.Fatalf("PromptSuffix = %q", composed.PromptSuffix)
	}
	if composed.NegativePrompt != "blurry, low quality" {
		t.Fatalf("NegativePrompt = %q", composed.NegativePrompt)
	}
	if composed.DestinationKey != "ai-images/apply-ok" {
		t.Fatalf("DestinationKey = %q, want styled key", composed.DestinationKey)
	}
}

// TestApplyStyle_ErrUnknownStyle_EmptyStyleName pins the canonical
// fail-closed gate: empty styleName pre-A2 was a silent fallback
// (returned the prompt unchanged). A2 surfaces ErrUnknownStyle so
// callers learn the implicit "no style" was a typed outcome.
func TestApplyStyle_ErrUnknownStyle_EmptyStyleName(t *testing.T) {
	reg := commonApplyStyleRegistry(t)
	composed, err := reg.ApplyStyle("hello", "", 0)
	if composed != nil {
		t.Fatalf("ApplyStyle with empty styleName must return nil composed envelope")
	}
	if !errors.Is(err, styleerrors.ErrUnknownStyle) {
		t.Fatalf("empty styleName must emit ErrUnknownStyle; got: %v", err)
	}
	// Cross-dispatch safety: ErrUnknownStyle must NOT match
	// unrelated sentinel families.
	if errors.Is(err, styleerrors.ErrStyleDisabled) {
		t.Fatalf("empty styleName err must not dispatch as ErrStyleDisabled (cross-leak)")
	}
	if errors.Is(err, styleerrors.ErrEmptyPrompt) {
		t.Fatalf("empty styleName err must not dispatch as ErrEmptyPrompt (cross-leak)")
	}
}

// TestApplyStyle_ErrUnknownStyle_NotInRegistry pins the canonical
// fail-closed gate for the registry-miss path: pre-A2 this was a
// silent fallback (returned the prompt unchanged). A2 surfaces
// ErrUnknownStyle with the ghost styleName in the wrap context.
func TestApplyStyle_ErrUnknownStyle_NotInRegistry(t *testing.T) {
	reg := commonApplyStyleRegistry(t)
	composed, err := reg.ApplyStyle("hello", "ghost-style", 0)
	if composed != nil {
		t.Fatalf("ApplyStyle with unknown styleName must return nil composed envelope")
	}
	if !errors.Is(err, styleerrors.ErrUnknownStyle) {
		t.Fatalf("unknown styleName must emit ErrUnknownStyle; got: %v", err)
	}
	// Verify the wrap context carries the ghost styleName (godlike/07
	// minimal-context dispatch info for log lines).
	if msg := err.Error(); msg == "" || !containsAll(msg, "ghost-style") {
		t.Fatalf("wrap message must carry the ghost styleName for audit log; got %q", msg)
	}
}

// TestApplyStyle_ErrStyleDisabled_EnabledFalse pins the canonical
// fail-closed gate: pre-A2 this was a silent prompt-only fallback.
// A2 surfaces ErrStyleDisabled (resolved via the image/styles
// alias — byte-identical to pkg/styleerrors.ErrStyleDisabled).
func TestApplyStyle_ErrStyleDisabled_EnabledFalse(t *testing.T) {
	reg := commonApplyStyleRegistry(t)
	composed, err := reg.ApplyStyle("a sunset", "apply-disabled", 0)
	if composed != nil {
		t.Fatalf("ApplyStyle with disabled style must return nil composed envelope")
	}
	if !errors.Is(err, styleerrors.ErrStyleDisabled) {
		t.Fatalf("disabled style must emit ErrStyleDisabled; got: %v", err)
	}
	// Cross-dispatch safety: ensure the disabled path is distinct
	// from the unknown path.
	if errors.Is(err, styleerrors.ErrUnknownStyle) {
		t.Fatalf("disabled style err must not dispatch as ErrUnknownStyle (cross-leak)")
	}
}

// TestApplyStyle_ErrEmptyPrompt_PromptAndSuffixBothEmpty pins the
// canonical fail-closed gate: pre-A2 this was a silent return of
// the empty string (or the suffix alone). A2 surfaces ErrEmptyPrompt
// so the caller learns the render is empty rather than emitting
// "" downstream silently.
func TestApplyStyle_ErrEmptyPrompt_PromptAndSuffixBothEmpty(t *testing.T) {
	reg := commonApplyStyleRegistry(t)
	composed, err := reg.ApplyStyle("", "apply-nosuffix", 0)
	if composed != nil {
		t.Fatalf("ApplyStyle with empty prompt + empty suffix must return nil composed envelope")
	}
	if !errors.Is(err, styleerrors.ErrEmptyPrompt) {
		t.Fatalf("empty prompt + empty suffix must emit ErrEmptyPrompt; got: %v", err)
	}
	// Sanity: a non-empty prompt against the same style SHOULD
	// succeed (the gate is "BOTH empty", not "either empty").
	if ok, oerr := reg.ApplyStyle("something", "apply-nosuffix", 0); oerr != nil || ok == nil || ok.ComposedText != "something" {
		t.Fatalf("non-empty prompt with apply-nosuffix (empty suffix) must compose prompt-only: err=%v out=%+v", oerr, ok)
	}
}

// TestApplyStyle_ErrStyleVersionMismatch_VersionMismatch pins the
// canonical fail-closed gate for caller version pins: pre-A2 there
// was no version check at all (ApplyStyle took 2 args). A2 adds the
// version arg AND surfaces ErrStyleVersionMismatch when the pin
// doesn't match the loaded StyleVersion.
func TestApplyStyle_ErrStyleVersionMismatch_VersionMismatch(t *testing.T) {
	reg := commonApplyStyleRegistry(t)
	composed, err := reg.ApplyStyle("hi", "apply-curated", 2) // loaded=7, want=2
	if composed != nil {
		t.Fatalf("ApplyStyle with version mismatch must return nil composed envelope")
	}
	if !errors.Is(err, styleerrors.ErrStyleVersionMismatch) {
		t.Fatalf("version mismatch must emit ErrStyleVersionMismatch; got: %v", err)
	}
	// Wrap message should carry both directions of the version divergence
	// (loaded=N want=M) per the production emitter format.
	if msg := err.Error(); !containsAll(msg, "loaded=7", "want=2") {
		t.Fatalf("wrap message must carry the version divergence; got %q", msg)
	}
}

// TestApplyStyle_Success_VersionZero_AcceptsAnyVersion pins the
// version=0 wildcard contract: callers that don't pin a version
// accept whatever the registry loaded. Without this, the wildcard
// contract would fail-closed and break legitimate "I don't care
// about the version" callers.
func TestApplyStyle_Success_VersionZero_AcceptsAnyVersion(t *testing.T) {
	reg := commonApplyStyleRegistry(t)
	composed, err := reg.ApplyStyle("hi", "apply-curated", 0) // loaded=7, version=0 wildcard
	if err != nil {
		t.Fatalf("version=0 wildcard must accept any loaded version; got: %v", err)
	}
	if composed == nil {
		t.Fatal("ApplyStyle with version=0 wildcard must return non-nil composed envelope")
	}
	if composed.StyleVersion != 7 {
		t.Fatalf("StyleVersion = %d, want 7 (loaded version surfaced for inspection)", composed.StyleVersion)
	}
	if !containsAny(composed.ComposedText, "hi", "test") {
		t.Fatalf("ComposedText = %q, want canonical compose", composed.ComposedText)
	}
}

// ── Surface-3 (July 2026) sentinel retirement stubs ────────────────────

func TestResolve_ProviderUnsupported_Retired(t *testing.T) {
	t.Skip("surface-3 (July 2026): per-style AllowedProviders check retired; ErrStyleProviderUnsupported never raised. See resolver.go doc-comment + sentinel non-nil contract TestStyleResolver_AllSentinelErrorsNonNil.")
}

func TestResolve_ModelUnsupported_Retired(t *testing.T) {
	t.Skip("surface-3 (July 2026): per-style AllowedModels check retired; ErrStyleModelUnsupported never raised. See resolver.go doc-comment + sentinel non-nil contract TestStyleResolver_AllSentinelErrorsNonNil.")
}

// ── Local string helpers (testing-only) ─────────────────────────────────

// containsAll reports whether needle appears in haystack for every
// entry in needles. testing-only — avoids the textutil dependency
// (which would cycle the test back into the package under test
// via pkg/textutil → internal/{...}).
func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strContains(haystack, n) {
			return false
		}
	}
	return true
}

// containsAny reports whether needle appears in haystack for any
// entry in needles. testing-only — see containsAll rationale.
func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strContains(haystack, n) {
			return true
		}
	}
	return false
}

// strContains is the canonical Go strings.Contains wrapper for the
// testing-only helpers above. Kept inline so the test file doesn't
// pay for an additional import path.
func strContains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestListEnabled(t *testing.T) {
	// Step-1 typed migration (A1, July 2026): silent flip absent
	// → false. c must specify `enabled: true` explicitly (was
	// previously absent-and-defaults-true under the tri-state
	// pointer). The fixture exercises the new contract.
	yaml := `
styles:
  - name: "a"
    prompt_suffix: "a test suffix"
    enabled: true
  - name: "b"
    prompt_suffix: "b test suffix"
    enabled: false
  - name: "c"
    prompt_suffix: "c test suffix"
    enabled: true
`
	reg, err := NewStyleRegistry(testYAML(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	all := reg.List()
	if len(all) != 3 {
		t.Fatalf("List = %d, want 3", len(all))
	}

	enabled := reg.ListEnabled()
	if len(enabled) != 2 {
		t.Fatalf("ListEnabled = %d, want 2 (a + c)", len(enabled))
	}
}
