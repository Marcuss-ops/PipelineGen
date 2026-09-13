// Package ytdlp — health.go
//
// yt-dlp toolchain health probes.
//
// YouTube's extractor + anti-bot surface changes frequently, and a stale
// yt-dlp or a yt-dlp that cannot reach a PO Token provider is the single
// most common cause of "The page needs to be reloaded" / HTTP 403 download
// failures. This file owns the two probes that make that drift observable:
//
//   - version staleness: `yt-dlp --version` yields a date-based release
//     (YYYY.MM.DD[.NNNN]); we compare it against a max-age threshold.
//   - POT provider availability: yt-dlp only emits the
//     `[pot] PO Token Providers: ...` line while initialising an extractor,
//     so this probe runs a bounded `--verbose --simulate` extraction and
//     parses that line.
//
// Parsing is kept in pure functions (ParseVersionAge / ParsePOTProviders) so
// the threshold + detection logic is unit-testable without a subprocess; the
// Probe* functions are the thin process wrappers.
package ytdlp

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/process"
)

const (
	// DefaultMaxVersionAgeDays is the staleness threshold. yt-dlp releases
	// frequently; beyond ~3 months the extractor is usually behind YouTube.
	DefaultMaxVersionAgeDays = 90

	// DefaultProbeURL is the lightweight extraction used only to surface the
	// POT-provider line. It is a stable, very short public video; the probe
	// is `--simulate` so nothing is downloaded.
	DefaultProbeURL = "https://www.youtube.com/watch?v=jNQXAC9IVRw"
)

const (
	versionProbeTimeout = 20 * time.Second
	potProbeTimeout     = 60 * time.Second
)

// potProvidersLineRE matches the yt-dlp debug line that lists the detected
// PO Token providers, e.g.
//
//	[debug] [pot] PO Token Providers: bgutil:http-1.3.1 (external), ...
//	[debug] [pot] PO Token Providers: none
var potProvidersLineRE = regexp.MustCompile(`(?i)\[pot\]\s*PO Token Providers:\s*(.*)`)

// versionDateRE captures the leading YYYY.MM.DD of a yt-dlp version, ignoring
// an optional nightly build suffix (2026.08.19.123456).
var versionDateRE = regexp.MustCompile(`^\s*(\d{4})\.(\d{1,2})\.(\d{1,2})`)

// ParseVersionAge returns the age in whole days of a date-based yt-dlp
// version relative to now. ok is false when the version is not date-shaped.
//
// A version dated in the future (clock skew, or a build stamped ahead of the
// host clock) reports age 0 rather than a negative age.
func ParseVersionAge(version string, now time.Time) (ageDays int, ok bool) {
	match := versionDateRE.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return 0, false
	}
	year, _ := strconv.Atoi(match[1])
	month, _ := strconv.Atoi(match[2])
	day, _ := strconv.Atoi(match[3])
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return 0, false
	}
	released := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	// Reject impossible dates: time.Date normalizes (e.g. Feb 31 -> Mar 3),
	// so a round-trip check is the only reliable validation.
	if released.Year() != year || int(released.Month()) != month || released.Day() != day {
		return 0, false
	}
	delta := now.UTC().Sub(released)
	if delta < 0 {
		return 0, true
	}
	return int(delta.Hours() / 24), true
}

// VersionIsStale reports whether a version age exceeds the threshold. A
// non-positive threshold disables the check.
func VersionIsStale(ageDays, maxAgeDays int) bool {
	return maxAgeDays > 0 && ageDays > maxAgeDays
}

// ParsePOTProviders extracts the provider names from a yt-dlp verbose output.
//
// It scans for the `PO Token Providers:` line and splits the comma-separated
// list. The `none` sentinel (and an empty list) returns nil, so callers can
// treat "no providers" and "no line" uniformly as unavailable.
func ParsePOTProviders(output string) []string {
	for _, line := range strings.Split(output, "\n") {
		match := potProvidersLineRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		raw := strings.TrimSpace(match[1])
		if raw == "" || strings.EqualFold(raw, "none") {
			return nil
		}
		providers := splitPOTProviders(raw)
		if len(providers) == 0 {
			return nil
		}
		return providers
	}
	return nil
}

// splitPOTProviders splits the provider list on top-level commas only:
// individual entries carry parenthesised qualifiers that themselves contain
// commas (e.g. `bgutil:script-node-1.3.1 (external, unavailable)`), so a
// naive strings.Split would corrupt them.
func splitPOTProviders(raw string) []string {
	var providers []string
	depth := 0
	start := 0
	flush := func(end int) {
		if entry := strings.TrimSpace(raw[start:end]); entry != "" {
			providers = append(providers, entry)
		}
	}
	for i, r := range raw {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(raw))
	return providers
}

// ProbeVersion runs `yt-dlp --version` and returns the trimmed version
// string. It is offline (no network access).
func ProbeVersion(ctx context.Context, path string) (string, error) {
	result, err := process.Run(ctx, path, []string{"--version"}, process.Options{
		Timeout: versionProbeTimeout,
	})
	if err != nil {
		return "", fmt.Errorf("yt-dlp --version: %w", err)
	}
	version := strings.TrimSpace(result.Stdout)
	if version == "" {
		version = strings.TrimSpace(result.Output)
	}
	if version == "" {
		return "", fmt.Errorf("yt-dlp --version returned no output")
	}
	return version, nil
}

// ProbePOTProviders runs a bounded, simulate-only extraction and parses the
// POT-provider line from the combined process output. A nil slice with a nil
// error means yt-dlp reported no provider (the degraded state the guard
// warns about); an error means the probe itself could not run.
func ProbePOTProviders(ctx context.Context, path, probeURL string) ([]string, error) {
	if strings.TrimSpace(probeURL) == "" {
		probeURL = DefaultProbeURL
	}
	result, err := process.Run(ctx, path, []string{
		"--verbose", "--simulate", "--no-warnings", probeURL,
	}, process.Options{
		Timeout:        potProbeTimeout,
		CombinedOutput: true,
	})
	combined := result.Output
	if err != nil && combined == "" {
		return nil, fmt.Errorf("yt-dlp POT probe: %w", err)
	}
	providers := ParsePOTProviders(combined)
	if providers == nil && err != nil {
		// The probe reached the extractor but failed before reporting a
		// provider line — surface the failure so the warning carries a cause.
		return nil, fmt.Errorf("yt-dlp POT probe: %w", err)
	}
	return providers, nil
}
