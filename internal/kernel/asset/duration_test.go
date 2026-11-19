package asset_test

import (
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
