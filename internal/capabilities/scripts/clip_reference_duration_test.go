// Package scriptgeneration — clip_reference_duration_test.go certifies the
// canonical clip total-duration resolution: provenance is preserved, a legacy
// unprovenanced duration stays known as provider_metadata, and an unknown
// duration is never fabricated as a 0 value.
package scriptgeneration

import (
	"testing"

	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestClipReference_AssetDuration(t *testing.T) {
	t.Run("canonical DurationUS + probe source", func(t *testing.T) {
		c := &ClipReference{DurationUS: 18_420_000, DurationSource: kernelasset.DurationProbe}
		d := c.AssetDuration()
		if !d.Known() || d.Source != kernelasset.DurationProbe || d.DurationUS != 18_420_000 {
			t.Fatalf("unexpected %+v", d)
		}
	})
	t.Run("provider source", func(t *testing.T) {
		c := &ClipReference{DurationUS: 12_000_000, DurationSource: kernelasset.DurationProvider}
		d := c.AssetDuration()
		if !d.Known() || d.Source != kernelasset.DurationProvider || d.DurationUS != 12_000_000 {
			t.Fatalf("unexpected %+v", d)
		}
	})
	t.Run("legacy float duration maps to provider metadata", func(t *testing.T) {
		c := &ClipReference{Duration: 18.4205}
		d := c.AssetDuration()
		if !d.Known() || d.Source != kernelasset.DurationProvider {
			t.Fatalf("legacy duration must be known as provider_metadata: %+v", d)
		}
		if d.DurationUS != 18_420_500 {
			t.Fatalf("DurationUS = %d, want 18420500", d.DurationUS)
		}
	})
	t.Run("explicit unknown never fakes 0", func(t *testing.T) {
		c := &ClipReference{DurationSource: kernelasset.DurationUnknown}
		d := c.AssetDuration()
		if d.Known() {
			t.Fatalf("explicit unknown must not be known: %+v", d)
		}
		if d.DurationUS != 0 {
			t.Fatalf("unknown must carry no value: %+v", d)
		}
	})
	t.Run("absent duration is unknown", func(t *testing.T) {
		c := &ClipReference{}
		if d := c.AssetDuration(); d.Known() {
			t.Fatalf("absent duration must be unknown: %+v", d)
		}
	})
	t.Run("nil clip is unknown", func(t *testing.T) {
		var c *ClipReference
		if d := c.AssetDuration(); d.Known() {
			t.Fatalf("nil clip must be unknown: %+v", d)
		}
	})
}
