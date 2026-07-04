// Package styleerrors — sentinel contract tests.
//
// godlike/06 audit-pinning: every sentinel MUST be non-nil and
// emit a stable message string so consumers can rely on
// `errors.Is(err, Err<...>)` regardless of fmt.Errorf wrap chains
// at the dispatch seam.
//
// godlike/07 fail-closed: errors.Is must traverse the
// concrete-wrap hierarchy (the production emitters wrap via
// `fmt.Errorf("%w: <context>", ErrXxx, ...)` to attach the
// styleName/version context).
package styleerrors

import (
	"errors"
	"fmt"
	"testing"
)

// TestSentinels_AllNonNil locks the godlike/06 audit-pinning contract:
// every canonical sentinel one-of-four defined in errors.go MUST
// remain non-nil even after later retirement of the underlying check
// (mirrors the resolver.go pattern for ErrStyleProviderUnsupported /
// ErrStyleModelUnsupported — those stay defined as audit-pins even
// though surface-3 retired the checks).
func TestSentinels_AllNonNil(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
	}{
		{"ErrUnknownStyle", ErrUnknownStyle},
		{"ErrStyleDisabled", ErrStyleDisabled},
		{"ErrEmptyPrompt", ErrEmptyPrompt},
		{"ErrStyleVersionMismatch", ErrStyleVersionMismatch},
	}
	for _, s := range sentinels {
		if s.err == nil {
			t.Fatalf("%s must be non-nil (sentinel contract violation)", s.name)
		}
	}
}

// TestErrUnknownStyle_ErrorsIsDispatchable pins the godlike/07
// fail-closed contract for the ErrUnknownStyle emission. The
// production wrap shape is `fmt.Errorf("%w: %q", ErrUnknownStyle, styleName)`
// (or with empty-styleName variant). errors.Is must succeed through
// that wrap chain.
func TestErrUnknownStyle_ErrorsIsDispatchable(t *testing.T) {
	wrapped := fmt.Errorf("%w: %q", ErrUnknownStyle, "ghost-style")
	if !errors.Is(wrapped, ErrUnknownStyle) {
		t.Fatalf("errors.Is(wrapped, ErrUnknownStyle) = false; want true (got %v)", wrapped)
	}
	// Inverse dispatch: ErrUnknownStyle must NOT match unrelated sentinels.
	if errors.Is(wrapped, ErrStyleDisabled) {
		t.Fatalf("errors.Is(wrapped, ErrStyleDisabled) = true; want false (dispatch must be exclusive)")
	}
}

// TestErrStyleDisabled_ErrorsIsDispatchable — production wrap shape
// `fmt.Errorf("%w: %q", ErrStyleDisabled, styleName)`.
func TestErrStyleDisabled_ErrorsIsDispatchable(t *testing.T) {
	wrapped := fmt.Errorf("%w: %q", ErrStyleDisabled, "disabled-style")
	if !errors.Is(wrapped, ErrStyleDisabled) {
		t.Fatalf("errors.Is(wrapped, ErrStyleDisabled) = false; want true (got %v)", wrapped)
	}
	if errors.Is(wrapped, ErrUnknownStyle) {
		t.Fatalf("errors.Is(wrapped, ErrUnknownStyle) = true; want false (cross-dispatch leak)")
	}
}

// TestErrEmptyPrompt_ErrorsIsDispatchable — production wrap shape
// `fmt.Errorf("%w: %q", ErrEmptyPrompt, styleName)`.
func TestErrEmptyPrompt_ErrorsIsDispatchable(t *testing.T) {
	wrapped := fmt.Errorf("%w: %q", ErrEmptyPrompt, "apply-nosuffix")
	if !errors.Is(wrapped, ErrEmptyPrompt) {
		t.Fatalf("errors.Is(wrapped, ErrEmptyPrompt) = false; want true (got %v)", wrapped)
	}
	if errors.Is(wrapped, ErrStyleVersionMismatch) {
		t.Fatalf("errors.Is(wrapped, ErrStyleVersionMismatch) = true; want false")
	}
}

// TestErrStyleVersionMismatch_ErrorsIsDispatchable — production
// wrap shape `fmt.Errorf("%w: %q loaded=%d want=%d", ErrStyleVersionMismatch,
// styleName, loadedVersion, wantedVersion)`.
func TestErrStyleVersionMismatch_ErrorsIsDispatchable(t *testing.T) {
	wrapped := fmt.Errorf("%w: %q loaded=%d want=%d",
		ErrStyleVersionMismatch, "apply-curated", 7, 2)
	if !errors.Is(wrapped, ErrStyleVersionMismatch) {
		t.Fatalf("errors.Is(wrapped, ErrStyleVersionMismatch) = false; want true (got %v)", wrapped)
	}
	if errors.Is(wrapped, ErrEmptyPrompt) {
		t.Fatalf("errors.Is(wrapped, ErrEmptyPrompt) = true; want false")
	}
}

// TestSentinels_DistinctValues confirms the 4 sentinels are 4
// distinct error values — errors.Is must not collide. This is the
// godlike/06 audit-pinning boundary test: a future regression that
// (mistakenly) re-declares a sentinel in image/styles or pkg/styleerrors
// with the same message string would still surface as distinct values
// via Go's error-interface equality (pointer-distinct), but the
// canonical equality is via errors.Is — sentinel value identity,
// not message text equality.
func TestSentinels_DistinctValues(t *testing.T) {
	sentinels := map[string]error{
		"ErrUnknownStyle":         ErrUnknownStyle,
		"ErrStyleDisabled":        ErrStyleDisabled,
		"ErrEmptyPrompt":          ErrEmptyPrompt,
		"ErrStyleVersionMismatch": ErrStyleVersionMismatch,
	}
	seen := make(map[error]string, len(sentinels))
	for name, s := range sentinels {
		if other, dup := seen[s]; dup {
			t.Fatalf("sentinel value collision: %s == %s (godlike/06 SSOT violation)", name, other)
		}
		seen[s] = name
	}
}
