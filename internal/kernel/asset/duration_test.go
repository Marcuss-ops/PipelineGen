package asset_test

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestAssetDuration_Known(t *testing.T) {
	if !asset.ProbedDuration(1_000_000).Known() {
		t.Fatal("probed positive duration must be known")
	}
	if !asset.ProviderDuration(1_000_000).Known() {
		t.Fatal("provider positive duration must be known")
	}
	if asset.UnknownDuration().Known() {
		t.Fatal("unknown duration must not be known")
	}
	if asset.ProbedDuration(0).Known() {
		t.Fatal("non-positive probe duration must not be known")
	}
	if asset.ProbedDuration(-1).Known() {
		t.Fatal("negative probe duration must not be known")
	}
}

func TestAssetDuration_Validate(t *testing.T) {
	cases := []struct {
		name    string
		in      asset.AssetDuration
		wantErr bool
	}{
		{"probe positive", asset.ProbedDuration(1_000_000), false},
		{"provider positive", asset.ProviderDuration(2_000_000), false},
		{"unknown zero", asset.UnknownDuration(), false},
		{"unknown carries value", asset.AssetDuration{DurationUS: 5, Source: asset.DurationUnknown}, true},
		{"invalid source", asset.AssetDuration{DurationUS: 5, Source: asset.DurationSource("bogus")}, true},
		{"probe non-positive", asset.ProbedDuration(0), true},
		{"provider negative", asset.ProviderDuration(-1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %+v", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNormalizeDurationSource(t *testing.T) {
	cases := []struct {
		raw  string
		want asset.DurationSource
	}{
		{"probe", asset.DurationProbe},
		{"measured", asset.DurationProbe},
		{"ffprobe", asset.DurationProbe},
		{"ffprobe_backfill", asset.DurationProbe},
		{"probed", asset.DurationProbe},
		{"provider_metadata", asset.DurationProvider},
		{"declared_fallback", asset.DurationProvider},
		{"provider", asset.DurationProvider},
		{"", asset.DurationUnknown},
		{"  ", asset.DurationUnknown},
		{"something_else", asset.DurationUnknown},
	}
	for _, tc := range cases {
		if got := asset.NormalizeDurationSource(tc.raw); got != tc.want {
			t.Errorf("NormalizeDurationSource(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestResolveAssetDuration(t *testing.T) {
	t.Run("probe wins", func(t *testing.T) {
		got := asset.ResolveAssetDuration(18_420_000, nil, 12_000_000, true)
		if got.Source != asset.DurationProbe || got.DurationUS != 18_420_000 {
			t.Fatalf("probe must win: got %+v", got)
		}
	})
	t.Run("provider fallback when probe fails", func(t *testing.T) {
		got := asset.ResolveAssetDuration(0, errors.New("probe failed"), 12_000_000, true)
		if got.Source != asset.DurationProvider || got.DurationUS != 12_000_000 {
			t.Fatalf("reliable provider must be the fallback: got %+v", got)
		}
	})
	t.Run("unreliable provider is not trusted", func(t *testing.T) {
		got := asset.ResolveAssetDuration(0, errors.New("probe failed"), 12_000_000, false)
		if got.Source != asset.DurationUnknown {
			t.Fatalf("unreliable provider must collapse to unknown: got %+v", got)
		}
	})
	t.Run("degenerate probe result is not a fake success", func(t *testing.T) {
		got := asset.ResolveAssetDuration(0, nil, 0, false)
		if got.Source != asset.DurationUnknown {
			t.Fatalf("zero probe without error must be unknown, not a fake 0: got %+v", got)
		}
	})
	t.Run("unknown never fakes 0", func(t *testing.T) {
		got := asset.ResolveAssetDuration(0, errors.New("probe failed"), 0, false)
		if got.Known() {
			t.Fatalf("unknown must not be known: %+v", got)
		}
		if got.DurationUS != 0 {
			t.Fatalf("unknown must carry no duration value: %+v", got)
		}
	})
}

func TestOptionalDuration(t *testing.T) {
	some := asset.SomeDuration(3_000_000)
	if !some.Known {
		t.Fatal("SomeDuration must be known")
	}
	if us, ok := some.Value(); !ok || us != 3_000_000 {
		t.Fatalf("Value() = (%d, %t), want (3000000, true)", us, ok)
	}
	if err := some.Validate(); err != nil {
		t.Fatalf("SomeDuration must validate: %v", err)
	}

	none := asset.NoDuration()
	if none.Known {
		t.Fatal("NoDuration must be unknown")
	}
	if _, ok := none.Value(); ok {
		t.Fatal("NoDuration Value() must report unknown")
	}
	if err := none.Validate(); err != nil {
		t.Fatalf("NoDuration must validate: %v", err)
	}

	// Invariant: unknown must never carry a value, and a known duration must
	// be positive.
	if err := (asset.OptionalDuration{Known: true, DurationUS: 0}).Validate(); err == nil {
		t.Fatal("known duration of 0 must be rejected")
	}
	if err := (asset.OptionalDuration{Known: false, DurationUS: 5}).Validate(); err == nil {
		t.Fatal("unknown duration carrying a value must be rejected")
	}
}

func TestMediaWindow(t *testing.T) {
	w := asset.MediaWindow{
		SourceInUS:         5_000_000,
		SourceDurationUS:   8_000_000,
		TimelineStartUS:    1_000_000,
		TimelineDurationUS: 8_000_000,
	}
	if err := w.Validate(); err != nil {
		t.Fatalf("valid window must validate: %v", err)
	}
	if w.SourceEndUS() != 13_000_000 {
		t.Fatalf("SourceEndUS = %d, want 13000000", w.SourceEndUS())
	}
	if w.TimelineEndUS() != 9_000_000 {
		t.Fatalf("TimelineEndUS = %d, want 9000000", w.TimelineEndUS())
	}

	negatives := []asset.MediaWindow{
		{SourceInUS: -1},
		{SourceDurationUS: -1},
		{TimelineStartUS: -1},
		{TimelineDurationUS: -1},
	}
	for i, bad := range negatives {
		if err := bad.Validate(); err == nil {
			t.Fatalf("negative window[%d] must be rejected", i)
		}
	}
}
