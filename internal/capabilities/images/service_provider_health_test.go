package images

import (
	"context"
	"errors"
	"strings"
	"testing"

	retrieved "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/search"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

type healthProbeProvider struct {
	name       detail.ImageProvider
	healthyErr error
}

func (p healthProbeProvider) Name() detail.ImageProvider    { return p.name }
func (p healthProbeProvider) Healthy(context.Context) error { return p.healthyErr }
func (p healthProbeProvider) Search(context.Context, string, retrieved.RetrievalSearchOptions) ([]retrieved.RetrievalSearchResult, error) {
	return nil, nil
}

func serviceWithProviders(providers ...retrieved.RetrievalProvider) *Service {
	return &Service{Store: &ImageStorageService{retrievalRegistry: retrieved.NewRetrievalProviderRegistry(zap.NewNop(), providers)}}
}

func TestProbeImageProviderHealth_FailsClosedWithoutRegistry(t *testing.T) {
	err := (&Service{}).ProbeImageProviderHealth(context.Background())
	if !errors.Is(err, ErrNoHealthyImageProvider) {
		t.Fatalf("ProbeImageProviderHealth = %v, want ErrNoHealthyImageProvider", err)
	}
}

func TestProbeImageProviderHealth_FailsWhenEveryProviderIsUnhealthy(t *testing.T) {
	svc := serviceWithProviders(
		healthProbeProvider{name: detail.ProviderWikipedia, healthyErr: errors.New("down")},
		healthProbeProvider{name: detail.ProviderDuckDuckGo, healthyErr: errors.New("rate limited")},
	)
	err := svc.ProbeImageProviderHealth(context.Background())
	if !errors.Is(err, ErrNoHealthyImageProvider) {
		t.Fatalf("ProbeImageProviderHealth = %v, want ErrNoHealthyImageProvider", err)
	}
	if !strings.Contains(err.Error(), string(detail.ProviderWikipedia)) || !strings.Contains(err.Error(), string(detail.ProviderDuckDuckGo)) {
		t.Fatalf("probe error %q must name the unhealthy providers", err)
	}
}

func TestProbeImageProviderHealth_SucceedsWithOneHealthyProvider(t *testing.T) {
	svc := serviceWithProviders(
		healthProbeProvider{name: detail.ProviderWikipedia},
		healthProbeProvider{name: detail.ProviderDuckDuckGo, healthyErr: errors.New("rate limited")},
	)
	if err := svc.ProbeImageProviderHealth(context.Background()); err != nil {
		t.Fatalf("ProbeImageProviderHealth = %v, want nil with one healthy provider", err)
	}
}
