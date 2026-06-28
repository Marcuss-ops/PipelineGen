// Package defaults provides coalesce-style helper functions for applying
// default values when a variable is zero or empty. These are useful for
// simplifying the common pattern: if x == "" { x = y } or if x <= 0 { x = y }.
package defaults

import (
	"fmt"
	"strings"
	"time"
)

// String returns val if it is non-empty (after trimming whitespace), otherwise fallback.
func String(val, fallback string) string {
	if strings.TrimSpace(val) != "" {
		return val
	}
	return fallback
}

// Int returns val if it is greater than zero, otherwise fallback.
func Int(val, fallback int) int {
	if val > 0 {
		return val
	}
	return fallback
}

// Float64 returns val if it is greater than zero, otherwise fallback.
func Float64(val, fallback float64) float64 {
	if val > 0 {
		return val
	}
	return fallback
}

// Duration returns val if it is strictly positive (val > 0),
// otherwise fallback. The strictly-positive semantic mirrors Int /
// Float64 so a caller reading any of the four helpers sees the same
// "zero collapses to fallback" contract — a `<= 0` collapse would
// be inconsistent and would also silently swallow caller-side
// negative sentinels (where some callers express "no timeout", "use
// parent context", etc. with a negative value). Callers that
// genuinely need a distinct negative semantic MUST branch on sign
// BEFORE calling Duration.
//
// DRIFT-DEFAULTS-DURATION (June 2026, Step 4 PR1): this helper closes
// the four-way type gap. Before Step 4 PR1, callers needing a
// duration default had to write the `if val > 0 { val } else {
// fallback }` pattern inline; the duplicate copies across the
// codebase were the drift class this helper targets (see e.g.
// internal/infrastructure/artlist/cache/cache.go::TTLDuration
// collapse for one concrete pre-Step-4 instance).
func Duration(val, fallback time.Duration) time.Duration {
	if val > 0 {
		return val
	}
	return fallback
}

// Truthy parses a string as boolean. Accepts true|1|yes|on (case-insensitive).
// Anything else (including empty string) returns false. The asymmetry is
// intentional: a missing or typo'd query-string flag MUST NOT silently
// activate a feature toggle. Used by `?search=`, `?allow_text_only=`,
// future-gate-style env-parsing patterns.
//
// PJ-CURATE-1 (June 2026): this helper was extracted from a one-off
// truthyQuery() in internal/api/script/handler_flow_ops.go so future
// flag-parsing handlers reuse the same canonical interpretation.
func Truthy(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// ── Composition-root SSOT validator ─────────────────────────────────
//
// Validate runs the canonical SSOT validator across every
// Default{Domain}Config() in this package. Returns nil if every
// SSOT is safe to consume at runtime; otherwise returns a single
// error aggregating every problem found, prefixed with the failure
// count so an operator scanning logs can see at a glance whether
// the failure is a single typo or a systemic refactor regression.
//
// Called from internal/platform/config/config.go::Validate() at
// composition-root startup so a broken SSOT fails the boot
// immediately rather than silently disabling a feature later. The
// check is structural (zero/empty field detection + value range +
// cross-field ordering) — it does NOT validate business rules like
// "MinSemanticScore should be 0-100" beyond the obvious
// out-of-range failure mode (those semantic rules belong in the
// consumer that knows the meaning).
//
// DRIFT-DEFAULTS-VALIDATE (June 2026, Step 4 PR6): this closes the
// composition-root gap exposed by the per-domain SSOTs (PR1-PR5).
// Before PR6, the SSOTs were leaf-only with no compile-time or
// runtime guarantee that the values were sane — a refactor that
// reduced DefaultScriptConfig().WordsPerMinute to 0 (e.g. while
// testing a per-locale override) would silently break every
// duration estimate downstream. The per-domain test files in
// PR1-PR5 catch regressions at `go test`, but a production deploy
// that does not run the full test suite would not. PR6's
// composition-root call covers the deploy path.
func Validate() error {
	return validateSSOTs(
		DefaultVideoConfig(),
		DefaultVoiceoverConfig(),
		DefaultMediaConfig(),
		DefaultYouTubeConfig(),
		DefaultScriptConfig(),
	)
}

// validateSSOTs is the testable internal helper. Public Validate()
// calls this with the canonical Default{Domain}Config() values;
// tests call this with hand-built values to exercise failure
// paths. Kept unexported so the public surface is the single
// no-args Validate() entry point.
func validateSSOTs(v VideoConfig, vo VoiceoverConfig, m MediaConfig, yt YouTubeConfig, s ScriptConfig) error {
	var errs []string

	// VideoConfig
	if v.ChunkDuration <= 0 {
		errs = append(errs, "VideoConfig.ChunkDuration must be > 0")
	}
	if v.EffectsDir == "" {
		errs = append(errs, "VideoConfig.EffectsDir must not be empty")
	}
	if v.ParentFieldName == "" {
		errs = append(errs, "VideoConfig.ParentFieldName must not be empty")
	}

	// VoiceoverConfig
	if vo.DefaultFilenameTemplate == "" {
		errs = append(errs, "VoiceoverConfig.DefaultFilenameTemplate must not be empty")
	}
	if vo.DefaultStrategy == "" {
		errs = append(errs, "VoiceoverConfig.DefaultStrategy must not be empty")
	}
	if vo.DefaultLanguage == "" {
		errs = append(errs, "VoiceoverConfig.DefaultLanguage must not be empty")
	}

	// MediaConfig
	if m.SearchDefaultLimit <= 0 {
		errs = append(errs, "MediaConfig.SearchDefaultLimit must be > 0")
	}
	if m.SearchMaxLimit < m.SearchDefaultLimit {
		errs = append(errs, fmt.Sprintf("MediaConfig.SearchMaxLimit (%d) must be >= SearchDefaultLimit (%d)", m.SearchMaxLimit, m.SearchDefaultLimit))
	}
	if m.CurateDefaultLimit <= 0 {
		errs = append(errs, "MediaConfig.CurateDefaultLimit must be > 0")
	}
	if m.CurateMaxLimit < m.CurateDefaultLimit {
		errs = append(errs, fmt.Sprintf("MediaConfig.CurateMaxLimit (%d) must be >= CurateDefaultLimit (%d)", m.CurateMaxLimit, m.CurateDefaultLimit))
	}
	if m.DefaultScore < 0 || m.DefaultScore > 1 {
		errs = append(errs, fmt.Sprintf("MediaConfig.DefaultScore (%v) must be in [0,1]", m.DefaultScore))
	}

	// YouTubeConfig
	if yt.FallbackCategory == "" {
		errs = append(errs, "YouTubeConfig.FallbackCategory must not be empty")
	}
	if yt.MaxSegments <= 0 {
		errs = append(errs, "YouTubeConfig.MaxSegments must be > 0")
	}
	if yt.MaxClipDuration <= 0 {
		errs = append(errs, "YouTubeConfig.MaxClipDuration must be > 0")
	}
	if yt.MinSemanticScore < 0 || yt.MinSemanticScore > 100 {
		errs = append(errs, fmt.Sprintf("YouTubeConfig.MinSemanticScore (%d) must be in [0,100]", yt.MinSemanticScore))
	}
	if yt.MaxVideosPerRun <= 0 {
		errs = append(errs, "YouTubeConfig.MaxVideosPerRun must be > 0")
	}
	if yt.CheckInterval == "" {
		errs = append(errs, "YouTubeConfig.CheckInterval must not be empty")
	}
	if yt.Priority < 0 {
		errs = append(errs, fmt.Sprintf("YouTubeConfig.Priority (%d) must be >= 0", yt.Priority))
	}

	// ScriptConfig
	if s.WordsPerMinute <= 0 {
		errs = append(errs, "ScriptConfig.WordsPerMinute must be > 0")
	}
	if s.DefaultDuration <= 0 {
		errs = append(errs, "ScriptConfig.DefaultDuration must be > 0")
	}
	if s.DefaultLanguage == "" {
		errs = append(errs, "ScriptConfig.DefaultLanguage must not be empty")
	}
	if s.DefaultTemplate == "" {
		errs = append(errs, "ScriptConfig.DefaultTemplate must not be empty")
	}
	if s.DefaultTone == "" {
		errs = append(errs, "ScriptConfig.DefaultTone must not be empty")
	}

	if len(errs) > 0 {
		return fmt.Errorf("pkg/defaults: %d SSOT validation failure(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}
