// Package asset — duration.go defines the canonical duration contract shared
// by the asset/catalog domain and the media/timeline consumers.
//
// The contract exists to kill the ambiguous generic duration_ms field that
// was reused for three different meanings (asset total, source window,
// timeline window). Instead every duration is either:
//
//   - AssetDuration — the certified TOTAL duration of the complete original
//     file, with provenance (probe / provider_metadata / unknown);
//   - MediaWindow — the usage window, which separates the source range
//     (SourceInUS / SourceDurationUS) from the timeline placement
//     (TimelineStartUS / TimelineDurationUS);
//   - OptionalDuration — a duration that may legitimately be unknown,
//     expressed as an explicit Known=false state (never a fabricated 0).
//
// godlike/07 NO-FAKE-AVAILABILITY: an unknown duration is NEVER encoded as
// zero. Zero is a real (if degenerate) duration value; unknown is a distinct
// state so consumers can fail closed instead of treating "we don't know" as
// "zero-length".
package asset

import (
	"fmt"
	"strings"
)

// DurationSource tags how an asset's total duration was obtained. It is the
// canonical provenance carried alongside a duration value so consumers can
// distinguish a probed measurement from provider-declared metadata from a
// genuinely unknown duration.
type DurationSource string

const (
	// DurationProbe is an authoritative local-binary probe (ffprobe / the
	// Rust prober) of the actual file bytes.
	DurationProbe DurationSource = "probe"
	// DurationProvider is a provider-declared duration carried as metadata
	// (e.g. a Drive manifest fallback). It is trusted only when the caller
	// marks it reliable.
	DurationProvider DurationSource = "provider_metadata"
	// DurationUnknown means no reliable duration is known. It is the
	// explicit "we don't know" state — never a fabricated zero.
	DurationUnknown DurationSource = "unknown"
)

// AssetDuration is the certified total duration of the complete original
// asset file, with provenance. DurationUS is in integer microseconds; Source
// identifies how it was obtained.
type AssetDuration struct {
	DurationUS int64          `json:"duration_us"`
	Source     DurationSource `json:"source"`
}

// Known reports whether the duration carries a real, sourced value. An
// unknown duration (Source == DurationUnknown, or a non-positive value) is
// not known.
func (d AssetDuration) Known() bool {
	return d.Source != DurationUnknown && d.DurationUS > 0
}

// Validate enforces the "never fake 0" invariant: a known duration must be
// positive and carry a probe/provider provenance; an unknown duration must
// not claim a positive value.
func (d AssetDuration) Validate() error {
	if d.Source == DurationUnknown {
		if d.DurationUS != 0 {
			return fmt.Errorf("asset duration: unknown source must not carry a duration value (got %d)", d.DurationUS)
		}
		return nil
	}
	if d.Source != DurationProbe && d.Source != DurationProvider {
		return fmt.Errorf("asset duration: invalid source %q", d.Source)
	}
	if d.DurationUS <= 0 {
		return fmt.Errorf("asset duration: %s duration must be positive (got %d)", d.Source, d.DurationUS)
	}
	return nil
}

// ProbedDuration returns an authoritative probe-derived duration.
func ProbedDuration(durationUS int64) AssetDuration {
	return AssetDuration{DurationUS: durationUS, Source: DurationProbe}
}

// ProviderDuration returns a provider-metadata duration.
func ProviderDuration(durationUS int64) AssetDuration {
	return AssetDuration{DurationUS: durationUS, Source: DurationProvider}
}

// UnknownDuration returns the explicit unknown sentinel (never a fake 0).
func UnknownDuration() AssetDuration {
	return AssetDuration{Source: DurationUnknown}
}

// NormalizeDurationSource maps a raw duration_source metadata string — the
// legacy wire values ("measured", "ffprobe_backfill", "declared_fallback")
// as well as the canonical enum values — onto the canonical DurationSource.
// Anything unrecognized collapses to DurationUnknown.
func NormalizeDurationSource(raw string) DurationSource {
	switch strings.TrimSpace(raw) {
	case string(DurationProbe), "measured", "ffprobe", "ffprobe_backfill", "probed":
		return DurationProbe
	case string(DurationProvider), "declared_fallback", "provider":
		return DurationProvider
	default:
		return DurationUnknown
	}
}

// DurationProvenance resolves the canonical DurationSource for this asset's
// total duration: an explicit duration_source metadata tag wins; a positive
// catalog Duration is provider_metadata; otherwise unknown. A caller that
// resolves a fresh local probe when the catalog duration is absent maps that
// outcome to DurationProbe itself (via ResolveAssetDuration).
func (a *Asset) DurationProvenance() DurationSource {
	if a == nil {
		return DurationUnknown
	}
	if raw := strings.TrimSpace(a.GetMetadataString("duration_source")); raw != "" {
		return NormalizeDurationSource(raw)
	}
	if a.Duration > 0 {
		return DurationProvider
	}
	return DurationUnknown
}

// ResolveAssetDuration applies the canonical duration precedence. It is the
// single resolution policy every duration consumer must use instead of
// duplicating probe/fallback decision logic:
//
//  1. local binary probe (authoritative);
//  2. trusted provider metadata;
//  3. unknown — never a fabricated zero.
//
// probeErr is non-nil when the probe failed. providerReliable indicates the
// provider metadata is trusted enough to serve as a fallback.
func ResolveAssetDuration(probeUS int64, probeErr error, providerUS int64, providerReliable bool) AssetDuration {
	if probeErr == nil && probeUS > 0 {
		return ProbedDuration(probeUS)
	}
	if providerReliable && providerUS > 0 {
		return ProviderDuration(providerUS)
	}
	return UnknownDuration()
}

// OptionalDuration is a duration that may be unknown. Unknown is an explicit
// state (Known=false), never encoded as a 0 value that could be confused with
// a real zero-length duration.
type OptionalDuration struct {
	Known      bool  `json:"known"`
	DurationUS int64 `json:"duration_us,omitempty"`
}

// SomeDuration returns a known duration.
func SomeDuration(durationUS int64) OptionalDuration {
	return OptionalDuration{Known: true, DurationUS: durationUS}
}

// NoDuration returns the explicit unknown sentinel.
func NoDuration() OptionalDuration {
	return OptionalDuration{}
}

// Value returns (durationUS, ok) — the idiomatic comma-ok surface.
func (d OptionalDuration) Value() (int64, bool) {
	return d.DurationUS, d.Known
}

// Validate enforces the invariant: a known duration must be positive; an
// unknown duration must not carry a positive value.
func (d OptionalDuration) Validate() error {
	if d.Known {
		if d.DurationUS <= 0 {
			return fmt.Errorf("optional duration: known duration must be positive (got %d)", d.DurationUS)
		}
		return nil
	}
	if d.DurationUS != 0 {
		return fmt.Errorf("optional duration: unknown duration must not carry a value (got %d)", d.DurationUS)
	}
	return nil
}

// MediaWindow is the canonical "usage in the timeline" duration contract. It
// separates the source window (where in the original asset the clip starts
// and how long it runs) from the timeline window (where on the assembled
// timeline it is placed and how long it occupies), so neither is ever
// conflated with the asset's total duration.
type MediaWindow struct {
	SourceInUS       int64 `json:"source_in_us"`
	SourceDurationUS int64 `json:"source_duration_us"`

	TimelineStartUS    int64 `json:"timeline_start_us"`
	TimelineDurationUS int64 `json:"timeline_duration_us"`
}

// SourceEndUS returns the exclusive end of the source window.
func (w MediaWindow) SourceEndUS() int64 {
	return w.SourceInUS + w.SourceDurationUS
}

// TimelineEndUS returns the exclusive end of the timeline window.
func (w MediaWindow) TimelineEndUS() int64 {
	return w.TimelineStartUS + w.TimelineDurationUS
}

// Validate enforces the structural invariants of a usage window: the source
// range and the timeline range must each be non-negative.
func (w MediaWindow) Validate() error {
	if w.SourceInUS < 0 {
		return fmt.Errorf("media window: negative source_in_us %d", w.SourceInUS)
	}
	if w.SourceDurationUS < 0 {
		return fmt.Errorf("media window: negative source_duration_us %d", w.SourceDurationUS)
	}
	if w.TimelineStartUS < 0 {
		return fmt.Errorf("media window: negative timeline_start_us %d", w.TimelineStartUS)
	}
	if w.TimelineDurationUS < 0 {
		return fmt.Errorf("media window: negative timeline_duration_us %d", w.TimelineDurationUS)
	}
	return nil
}
