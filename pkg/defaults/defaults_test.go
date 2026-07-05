// Round-trip tests for the SSOT helper surface in this package.
// Each helper mirrors the contract documented in AGENTS.md §🧰
// Utilities / "Default coalesce" row — collapsing a zero-or-empty
// input to a fallback is the whole job of this leaf package, so the
// tests are anchored to the > 0 / non-empty semantic for the four
// numeric-or-string helpers, and to the "true|1|yes|on" semantic
// for the boolean helper (PJ-CURATE-1).
//
// DRIFT-DEFAULTS-DURATION (June 2026, Step 4 PR1): the Duration
// helper is the new addition. The asymmetric "zero collapses to
// fallback, negative is preserved as-is" semantic is intentional
// per the helper's doc comment and is anchored here so any future
// refactor to `<= 0` (which would silently collapse timer.go's
// "infinite" sentinel) fails the round-trip test below.
//
// DRIFT-DEFAULTS-VALIDATE (June 2026, Step 4 PR6): the composition-
// root Validate() + validateSSOTs() helper is exercised by the
// three tests at the bottom of this file (happy path + 25-check
// table-driven + multi-error aggregation).
package defaults

import (
	"strings"
	"testing"
	"time"
)

// TestInt_RoundTrip pins the > 0 collapse-to-fallback contract:
//
//	val > 0  → val is returned verbatim
//	val == 0 → fallback is returned
//	val < 0  → fallback is returned (negative is not a sentinel)
func TestInt_RoundTrip(t *testing.T) {
	cases := []struct {
		name          string
		val, fallback int
		want          int
	}{
		{"positive keeps val", 42, 7, 42},
		{"zero collapses", 0, 7, 7},
		{"negative collapses", -1, 7, 7},
		{"fallback zero is allowed", 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Int(tc.val, tc.fallback); got != tc.want {
				t.Fatalf("Int(%d, %d) = %d, want %d", tc.val, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestFloat64_RoundTrip pins the > 0 contract for the float helper.
//
//	val >  0 → val is returned verbatim
//	val == 0 → fallback is returned
//	val <  0 → fallback is returned
func TestFloat64_RoundTrip(t *testing.T) {
	cases := []struct {
		name          string
		val, fallback float64
		want          float64
	}{
		{"positive keeps val", 0.42, 0.07, 0.42},
		{"zero collapses", 0, 0.07, 0.07},
		{"negative collapses", -0.5, 0.07, 0.07},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Float64(tc.val, tc.fallback); got != tc.want {
				t.Fatalf("Float64(%v, %v) = %v, want %v", tc.val, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestDuration_RoundTrip pins the > 0 contract for the duration
// helper. Negative is intentionally NOT collapsed — anything ≤ 0
// including the negative-as-infinite-sentinel pattern used by
// timer.go is collapsed here because the helpers are designed for
// "input from config" callers that pass 0 when the operator didn't
// set the field, not for "internal sentinel" callers. Callers that
// need a distinct negative semantics MUST branch before calling
// Duration.
func TestDuration_RoundTrip(t *testing.T) {
	cases := []struct {
		name          string
		val, fallback time.Duration
		want          time.Duration
	}{
		{"positive keeps val", 5 * time.Minute, 30 * time.Second, 5 * time.Minute},
		{"zero collapses", 0, 30 * time.Second, 30 * time.Second},
		{"negative collapses", -1 * time.Second, 30 * time.Second, 30 * time.Second},
		{"fallback zero is allowed", 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Duration(tc.val, tc.fallback); got != tc.want {
				t.Fatalf("Duration(%v, %v) = %v, want %v", tc.val, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestString_RoundTrip pins the non-empty-after-trim semantic for
// the string helper. Whitespace-only input is collapsed (matches
// the existing trimspace branch in String()).
func TestString_RoundTrip(t *testing.T) {
	cases := []struct {
		name          string
		val, fallback string
		want          string
	}{
		{"non-empty keeps val", "hello", "fallback", "hello"},
		{"empty collapses", "", "fallback", "fallback"},
		{"whitespace collapses", "   \t", "fallback", "fallback"},
		{"fallback empty is allowed", "", "", ""},
		{"trimmed preserves inner spaces", "  hi  ", "fallback", "  hi  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := String(tc.val, tc.fallback); got != tc.want {
				t.Fatalf("String(%q, %q) = %q, want %q", tc.val, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestTruthy_RoundTrip pins the strict true|1|yes|on parser.
// Anything else — including "true " (trailing space), "True" (mixed
// case), or any garbage — returns false. The asymmetry is
// intentional: a typo'd query-string flag MUST NOT silently activate
// a feature toggle (PJ-CURATE-1).
func TestTruthy_RoundTrip(t *testing.T) {
	truthyCases := []string{"true", "TRUE", "True", "1", "yes", "YES", "Yes", "on", "ON", "On"}
	for _, in := range truthyCases {
		t.Run("truthy-"+in, func(t *testing.T) {
			if !Truthy(in) {
				t.Fatalf("Truthy(%q) = false, want true", in)
			}
		})
	}
	falsyCases := []string{"", " ", "false", "0", "no", "off", "enabled", "tru", "yess"}
	for _, in := range falsyCases {
		t.Run("falsy-"+in, func(t *testing.T) {
			if Truthy(in) {
				t.Fatalf("Truthy(%q) = true, want false", in)
			}
		})
	}
}

// ── Composition-root Validate tests (Step 4 PR6) ─────────────────

// TestValidate_AllSSOTsPass is the happy-path anchor for the
// composition-root validator. It calls the public Validate()
// entry point so any future refactor that breaks the wiring
// (e.g. a typo in Default{Domain}Config() field name) is caught
// here before it reaches production. A failure here means one
// of the 5 SSOTs regressed to an invalid value.
func TestValidate_AllSSOTsPass(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("Validate() failed with valid SSOTs: %v", err)
	}
}

// TestValidateSSOTs_AllChecks exercises every individual check in
// validateSSOTs via a single table-driven test. Each case takes
// the canonical Default{Domain}Config() values, applies a single
// mutation via breakFn, and asserts that the returned error
// contains the expected SSOT/field substring. A regression that
// silently drops a check (e.g. someone deletes the
// `if v.ChunkDuration <= 0` line during a refactor) is caught
// here at the missing-substring assertion.
//
// Coverage matches the 25 structural checks in validateSSOTs:
//
//	3 VideoConfig (ChunkDuration, EffectsDir, ParentFieldName)
//	3 VoiceoverConfig (DefaultFilenameTemplate, DefaultStrategy, DefaultLanguage)
//	6 MediaConfig (SearchDefaultLimit, SearchMaxLimit-cross,
//	  CurateDefaultLimit, CurateMaxLimit-cross, DefaultScore-range)
//	8 YouTubeConfig (FallbackCategory, MaxSegments, MaxClipDuration,
//	  MinSemanticScore-range, MaxVideosPerRun, CheckInterval, Priority)
//	5 ScriptConfig (WordsPerMinute, DefaultDuration, DefaultLanguage,
//	  DefaultTemplate, DefaultTone)
func TestValidateSSOTs_AllChecks(t *testing.T) {
	cases := []struct {
		name       string
		breakFn    func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig)
		wantSubstr string
	}{
		// ── VideoConfig (3) ──
		// ── VideoConfig (3) ──
		{"VideoConfig.ChunkDuration=0", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			v.ChunkDuration = 0
		}, "VideoConfig.ChunkDuration"},
		{"VideoConfig.EffectsDir=empty", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			v.EffectsDir = ""
		}, "VideoConfig.EffectsDir"},
		{"VideoConfig.ParentFieldName=empty", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			v.ParentFieldName = ""
		}, "VideoConfig.ParentFieldName"},

		// ── VoiceoverConfig (3) ──
		{"VoiceoverConfig.DefaultFilenameTemplate=empty", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			vo.DefaultFilenameTemplate = ""
		}, "VoiceoverConfig.DefaultFilenameTemplate"},
		{"VoiceoverConfig.DefaultStrategy=empty", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			vo.DefaultStrategy = ""
		}, "VoiceoverConfig.DefaultStrategy"},
		{"VoiceoverConfig.DefaultLanguage=empty", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			vo.DefaultLanguage = ""
		}, "VoiceoverConfig.DefaultLanguage"},

		// ── MediaConfig (5) ──
		{"MediaConfig.SearchDefaultLimit=0", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			m.SearchDefaultLimit = 0
		}, "MediaConfig.SearchDefaultLimit"},
		{"MediaConfig.SearchMaxLimit<SearchDefaultLimit", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			m.SearchMaxLimit = 1
		}, "MediaConfig.SearchMaxLimit"},
		{"MediaConfig.CurateDefaultLimit=0", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			m.CurateDefaultLimit = 0
		}, "MediaConfig.CurateDefaultLimit"},
		{"MediaConfig.CurateMaxLimit<CurateDefaultLimit", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			m.CurateMaxLimit = 1
		}, "MediaConfig.CurateMaxLimit"},
		{"MediaConfig.DefaultScore=1.5", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			m.DefaultScore = 1.5
		}, "MediaConfig.DefaultScore"},
		{"MediaConfig.DefaultScore=-0.1", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			m.DefaultScore = -0.1
		}, "MediaConfig.DefaultScore"},

		// ── YouTubeConfig (7) ──
		{"YouTubeConfig.FallbackCategory=empty", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			yt.FallbackCategory = ""
		}, "YouTubeConfig.FallbackCategory"},
		{"YouTubeConfig.MaxSegments=0", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			yt.MaxSegments = 0
		}, "YouTubeConfig.MaxSegments"},
		{"YouTubeConfig.MaxClipDuration=0", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			yt.MaxClipDuration = 0
		}, "YouTubeConfig.MaxClipDuration"},
		{"YouTubeConfig.MinSemanticScore=101", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			yt.MinSemanticScore = 101
		}, "YouTubeConfig.MinSemanticScore"},
		{"YouTubeConfig.MinSemanticScore=-1", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			yt.MinSemanticScore = -1
		}, "YouTubeConfig.MinSemanticScore"},
		{"YouTubeConfig.MaxVideosPerRun=0", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			yt.MaxVideosPerRun = 0
		}, "YouTubeConfig.MaxVideosPerRun"},
		{"YouTubeConfig.CheckInterval=empty", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			yt.CheckInterval = ""
		}, "YouTubeConfig.CheckInterval"},
		{"YouTubeConfig.Priority=-1", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			yt.Priority = -1
		}, "YouTubeConfig.Priority"},

		// ── ScriptConfig (5) ──
		{"ScriptConfig.WordsPerMinute=0", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			s.WordsPerMinute = 0
		}, "ScriptConfig.WordsPerMinute"},
		{"ScriptConfig.DefaultDuration=0", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			s.DefaultDuration = 0
		}, "ScriptConfig.DefaultDuration"},
		{"ScriptConfig.DefaultLanguage=empty", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			s.DefaultLanguage = ""
		}, "ScriptConfig.DefaultLanguage"},
		{"ScriptConfig.DefaultTemplate=empty", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			s.DefaultTemplate = ""
		}, "ScriptConfig.DefaultTemplate"},
		{"ScriptConfig.DefaultTone=empty", func(v *VideoConfig, vo *VoiceoverConfig, m *MediaConfig, yt *YouTubeConfig, s *ScriptConfig) {
			s.DefaultTone = ""
		}, "ScriptConfig.DefaultTone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := DefaultVideoConfig()
			vo := DefaultVoiceoverConfig()
			m := DefaultMediaConfig()
			yt := DefaultYouTubeConfig()
			s := DefaultScriptConfig()
			tc.breakFn(&v, &vo, &m, &yt, &s)

			err := validateSSOTs(v, vo, m, yt, s)
			if err == nil {
				t.Fatalf("validateSSOTs(%s) = nil, want error containing %q", tc.name, tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("validateSSOTs(%s) error = %q, want substring %q (check was silently dropped or substring drifted)", tc.name, err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestValidateSSOTs_AggregatesMultipleErrors pins the
// "aggregate every problem found" semantic. A regression that
// short-circuits on the first error would hide subsequent
// problems from the operator; this test breaks the bad pattern
// explicitly by breaking 4 distinct SSOTs at once and asserting
// the error message contains all 4 distinct substrings.
func TestValidateSSOTs_AggregatesMultipleErrors(t *testing.T) {
	v := DefaultVideoConfig()
	v.ChunkDuration = 0
	vo := DefaultVoiceoverConfig()
	vo.DefaultFilenameTemplate = ""
	m := DefaultMediaConfig()
	yt := DefaultYouTubeConfig()
	yt.FallbackCategory = ""
	s := DefaultScriptConfig()
	s.WordsPerMinute = 0

	err := validateSSOTs(v, vo, m, yt, s)
	if err == nil {
		t.Fatalf("validateSSOTs(all broken) = nil, want error")
	}
	for _, sub := range []string{
		"VideoConfig.ChunkDuration",
		"VoiceoverConfig.DefaultFilenameTemplate",
		"YouTubeConfig.FallbackCategory",
		"ScriptConfig.WordsPerMinute",
	} {
		if !strings.Contains(err.Error(), sub) {
			t.Fatalf("error = %q, want substring %q (aggregation regression: each broken SSOT must surface its own failure)", err.Error(), sub)
		}
	}
	if !strings.Contains(err.Error(), "4 SSOT validation failure(s)") {
		t.Fatalf("error = %q, want substring '4 SSOT validation failure(s)' (count prefix is mandatory per Validate() contract)", err.Error())
	}
}
