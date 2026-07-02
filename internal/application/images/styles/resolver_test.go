// Package styles — resolver_test.go locks the FASE 2A-2C fail-closed
// contract via table-driven tests.
//
// Each row covers ONE failure mode (or the canonical success path) and
// asserts both the wrapped sentinel error AND Validate's response. The
// fake source backend is keyed by style id; tests either share a
// "default + cinematic + disabled" fixture or replace it inline for
// the "empty input + no default registered" case.
//
// surface-3 (July 2026): the per-style AllowedProviders / AllowedModels
// negative-path rows (formerly rows 6+7 of the table) were retired
// along with the underlying checks; the test now exercises only the
// remaining fail-closed gates (not-found, default fallback, missing
// default, disabled). The ErrStyleProviderUnsupported /
// ErrStyleModelUnsupported sentinels stay validated as non-nil by
// TestStyleResolver_AllSentinelErrorsNonNil below — that's the
// godlike/06 audit-pinning contract that callers can rely on.
package styles

import (
	"errors"
	"testing"
)

// fakeSourceBackend is a hand-rolled SourceBackend. Missing keys return
// (StyleSnapshot{}, ErrStyleNotFound) so the resolver's gate-1 branch
// confirms "missing key from a real source" matches the sentinel.
type fakeSourceBackend struct {
	styles map[string]StyleSnapshot
}

func (f *fakeSourceBackend) GetStyle(id string) (StyleSnapshot, error) {
	if f == nil || f.styles == nil {
		return StyleSnapshot{}, ErrStyleNotFound
	}
	s, ok := f.styles[id]
	if !ok {
		return StyleSnapshot{}, ErrStyleNotFound
	}
	return s, nil
}

func TestNew_NilSource_FailsClosed(t *testing.T) {
	r := New(nil)
	// surface-3 (July 2026): inputs use the canonical google-slides
	// provider + nano-banana-pro model. The choice is irrelevant for
	// the nil-source fail-closed assertion (the gate-1 branch fires
	// regardless of provider/model) but we keep the canonical values
	// for consistency with the rest of the suite.
	_, err := r.Resolve("cinematic", "google-slides", "nano-banana-pro")
	if !errors.Is(err, ErrStyleNotFound) {
		t.Fatalf("New(nil) must fail-closed: got %v, want ErrStyleNotFound", err)
	}
	if verr := r.Validate("anything", "any", "any"); !errors.Is(verr, ErrStyleNotFound) {
		t.Fatalf("nil-source Validate must return ErrStyleNotFound: got %v", verr)
	}
}

func TestStyleResolver_TableDriven_Cases(t *testing.T) {
	dst := "ai-images/cinematic"
	cinematic := StyleSnapshot{
		ID: "cinematic", Version: 1,
		PromptSuffix:      "cinematic movie still, dramatic natural lighting",
		NegativePrompt:    "blurry, low res, cartoon",
		Width: 1920, Height: 1080,
		DestinationKey:    dst,
		Enabled:           true,
		AllowedProviders:  []string{"google-slides"}, // surface-3: canonical, dead-control
		AllowedModels:     []string{"nano-banana-pro"}, // surface-3: canonical, dead-control
	}
	def := StyleSnapshot{
		ID: "default", Version: 1,
		PromptSuffix:      "neutral baseline",
		Width: 1280, Height: 720,
		Enabled:           true,
		AllowedProviders:  nil, // empty (post surface-3: never read; kept for yaml back-compat)
		AllowedModels:     nil, // empty (post surface-3: never read; kept for yaml back-compat)
	}
	disabled := StyleSnapshot{
		ID: "disabled-style", Version: 1,
		PromptSuffix: "anything",
		Enabled:      false,
	}
	full := &fakeSourceBackend{styles: map[string]StyleSnapshot{
		"cinematic":      cinematic,
		"default":        def,
		"disabled-style": disabled,
	}}
	missing := &fakeSourceBackend{styles: map[string]StyleSnapshot{
		"cinematic": cinematic,
		// no "default" entry — used by "empty input + no default"
		// case to exercise the ErrStyleNotFound branch on default id.
	}}

	tests := []struct {
		name      string
		src       *fakeSourceBackend
		styleID   string
		provider  string
		model     string
		wantErr   error
		wantStyle ResolvedStyle
	}{
		{
			name: "success: cinematic + google-slides + nano-banana-pro",
			src: full, styleID: "cinematic", provider: "google-slides", model: "nano-banana-pro",
			wantErr: nil,
			wantStyle: ResolvedStyle{
				ID: "cinematic", Version: 1,
				PromptSuffix:   "cinematic movie still, dramatic natural lighting",
				NegativePrompt: "blurry, low res, cartoon",
				Width: 1920, Height: 1080,
				DestinationKey: dst, Enabled: true,
			},
		},
		{
			name: "empty styleID falls back to magic id 'default'",
			src: full, styleID: "", provider: "google-slides", model: "nano-banana-pro",
			wantErr: nil,
			wantStyle: ResolvedStyle{
				ID: "default", Version: 1,
				PromptSuffix: "neutral baseline",
				Width: 1280, Height: 720,
				Enabled: true,
			},
		},
		{
			name: "missing styleID -> ErrStyleNotFound",
			src: full, styleID: "ghost-style", provider: "google-slides", model: "nano-banana-pro",
			wantErr: ErrStyleNotFound,
		},
		{
			name: "empty styleID + no default registered -> ErrStyleNotFound",
			src: missing, styleID: "", provider: "google-slides", model: "nano-banana-pro",
			wantErr: ErrStyleNotFound,
		},
		{
			name: "style found but Enabled=false -> ErrStyleDisabled",
			src: full, styleID: "disabled-style", provider: "google-slides", model: "nano-banana-pro",
			wantErr: ErrStyleDisabled,
		},
		// surface-3 (July 2026): the per-style AllowedProviders /
		// AllowedModels negative-path rows were retired because the
		// checks that fired ErrStyleProviderUnsupported /
		// ErrStyleModelUnsupported were themselves retired. See
		// resolver.go doc-comment for the canonical rationale.
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New(tc.src)
			got, err := r.Resolve(tc.styleID, tc.provider, tc.model)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Resolve(%q,%q,%q): err = %v, want errors.Is(_, %v)",
					tc.styleID, tc.provider, tc.model, err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if got.ID != tc.wantStyle.ID {
					t.Errorf("ID = %q, want %q", got.ID, tc.wantStyle.ID)
				}
				if got.PromptSuffix != tc.wantStyle.PromptSuffix {
					t.Errorf("PromptSuffix = %q, want %q", got.PromptSuffix, tc.wantStyle.PromptSuffix)
				}
				if got.DestinationKey != tc.wantStyle.DestinationKey {
					t.Errorf("DestinationKey = %q, want %q", got.DestinationKey, tc.wantStyle.DestinationKey)
				}
				if got.Width != tc.wantStyle.Width || got.Height != tc.wantStyle.Height {
					t.Errorf("dim = %dx%d, want %dx%d",
						got.Width, got.Height, tc.wantStyle.Width, tc.wantStyle.Height)
				}
				if got.Version != tc.wantStyle.Version {
					t.Errorf("Version = %d, want %d", got.Version, tc.wantStyle.Version)
				}
			}
			if verr := r.Validate(tc.styleID, tc.provider, tc.model); !errors.Is(verr, tc.wantErr) {
				t.Errorf("Validate did not match Resolve: got %v, want %v", verr, tc.wantErr)
			}
		})
	}
}

// TestStyleResolver_AllSentinelErrorsNonNil is the godlike/06
// audit-pinning contract: every sentinel — including the sur-face-3
// "dead-code" ErrStyleProviderUnsupported / ErrStyleModelUnsupported —
// must remain non-nil even after their checks are retired. The
// re-export surface in
// internal/application/assets/generation/style_registry.go (lines
// 39-44) keeps these aliases stable so external consumers and
// future wiring can still reference them.
func TestStyleResolver_AllSentinelErrorsNonNil(t *testing.T) {
	sentinels := []error{
		ErrStyleNotFound,
		ErrStyleProviderUnsupported,
		ErrStyleModelUnsupported,
		ErrStyleDisabled,
	}
	for _, s := range sentinels {
		if s == nil {
			t.Fatalf("sentinel must be non-nil: %v", s)
		}
	}
}

func TestStyleResolver_WhitespaceStyleIDFallsBackToDefault(t *testing.T) {
	full := &fakeSourceBackend{styles: map[string]StyleSnapshot{
		"default": {
			ID: "default", Version: 1, Enabled: true,
			PromptSuffix: "neutral",
		},
	}}
	r := New(full)
	got, err := r.Resolve("   ", "google-slides", "nano-banana-pro")
	if err != nil {
		t.Fatalf("whitespace-only styleID should resolve to default-magic-id: %v", err)
	}
	if got.ID != "default" {
		t.Fatalf("ID = %q, want default", got.ID)
	}
}
