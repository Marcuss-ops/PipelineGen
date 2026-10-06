package media

import (
	"context"
	"errors"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

type providerLookupStub struct {
	name string
	err  error
}

func (s *providerLookupStub) Provider(name string) (scriptports.VidRushAssetProvider, error) {
	s.name = name
	return nil, s.err
}

func TestVidRushProviderAvailabilityProbe(t *testing.T) {
	t.Run("delegates provider lookup", func(t *testing.T) {
		lookup := &providerLookupStub{}
		probe := NewVidRushProviderAvailabilityProbe(lookup)
		if probe == nil {
			t.Fatal("probe is nil")
		}
		if err := probe.ProbeVidRushProvider(context.Background(), "image_generation"); err != nil {
			t.Fatalf("ProbeVidRushProvider() error = %v", err)
		}
		if lookup.name != "image_generation" {
			t.Fatalf("lookup name = %q, want image_generation", lookup.name)
		}
	})

	t.Run("propagates missing provider", func(t *testing.T) {
		lookup := &providerLookupStub{err: scriptports.ErrVidRushProviderNotFound}
		probe := NewVidRushProviderAvailabilityProbe(lookup)
		err := probe.ProbeVidRushProvider(context.Background(), "image_generation")
		if !errors.Is(err, scriptports.ErrVidRushProviderNotFound) {
			t.Fatalf("ProbeVidRushProvider() error = %v, want provider-not-found", err)
		}
	})

	t.Run("nil registry is unavailable", func(t *testing.T) {
		probe := NewVidRushProviderAvailabilityProbe(nil)
		if probe != nil {
			t.Fatal("probe for nil registry must remain nil so preflight fails closed")
		}
		if err := (vidRushProviderAvailabilityProbe{}).ProbeVidRushProvider(context.Background(), "image_generation"); err == nil || !strings.Contains(err.Error(), "registry not wired") {
			t.Fatalf("nil lookup error = %v, want registry-not-wired", err)
		}
	})

	t.Run("preflight adapter checks request policy", func(t *testing.T) {
		lookup := &providerLookupStub{}
		probe := NewVidRushProviderAvailabilityProbe(lookup)
		adapter := NewPreflightWithProviderAvailability(nil, nil, nil, nil, probe)
		result := adapter.Run(context.Background(), scriptgen.GenerateRequest{
			MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{
				ImageGeneration: mediadomain.MediaToggleEnabled,
			}},
		})
		if result.HasFailures() {
			t.Fatalf("preflight adapter result = %s, want available provider success", result.Error())
		}
		if lookup.name != "image_generation" {
			t.Fatalf("provider lookup name = %q, want image_generation", lookup.name)
		}
	})

	t.Run("provider failure is independent of asset resolver", func(t *testing.T) {
		lookup := &providerLookupStub{err: scriptports.ErrVidRushProviderNotFound}
		adapter := NewPreflightWithProviderAvailability(nil, nil, nil, nil, NewVidRushProviderAvailabilityProbe(lookup))
		result := adapter.Run(context.Background(), scriptgen.GenerateRequest{
			MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{
				ImageGeneration: mediadomain.MediaToggleEnabled,
			}},
		})
		if !result.HasFailures() || !strings.Contains(result.Error(), "image_generation") {
			t.Fatalf("preflight adapter result = %s, want missing-provider failure", result.Error())
		}
	})
}
