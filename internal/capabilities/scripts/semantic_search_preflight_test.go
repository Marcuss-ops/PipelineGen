package scriptgeneration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/providerbudget"
)

// TestPreflightImageProvidersFailsClosedWhenEveryLaneCoolsDown pins the
// PERFLIVE-03 preflight: a run that needs images must stop before generation
// when no provider lane can answer. One available lane is enough to continue.
func TestPreflightImageProvidersFailsClosedWhenEveryLaneCoolsDown(t *testing.T) {
	registry := providerbudget.New(providerbudget.Config{Interval: 0, Burst: 1, Cooldown: 30 * time.Second})
	for _, lane := range imageProviderPreflightLanes {
		registry.ObserveRateLimit(lane, 30*time.Second)
	}
	planner, err := NewSemanticSearchBatchPlanner(&batchTestPort{}, SemanticSearchBatchConfig{Budget: registry})
	if err != nil {
		t.Fatalf("planner: %v", err)
	}
	err = planner.PreflightProviders(context.Background())
	if !errors.Is(err, ErrImageProviderUnavailable) {
		t.Fatalf("PreflightProviders = %v, want ErrImageProviderUnavailable", err)
	}
}

func TestPreflightImageProvidersContinuesWithOneAvailableLane(t *testing.T) {
	registry := providerbudget.New(providerbudget.Config{Interval: 0, Burst: 1, Cooldown: 30 * time.Second})
	registry.ObserveRateLimit("wikimedia_commons", 30*time.Second)
	registry.ObserveRateLimit("searxng", 30*time.Second)
	planner, err := NewSemanticSearchBatchPlanner(&batchTestPort{}, SemanticSearchBatchConfig{Budget: registry})
	if err != nil {
		t.Fatalf("planner: %v", err)
	}
	if err := planner.PreflightProviders(context.Background()); err != nil {
		t.Fatalf("PreflightProviders = %v, want nil while duckduckgo is available", err)
	}
}
