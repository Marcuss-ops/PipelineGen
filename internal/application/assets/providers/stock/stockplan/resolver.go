package stockplan

import (
	"context"
	"fmt"
)

// defaultResolver is the canonical PlanResolver.
type defaultResolver struct {
	sampler ClipSampler
}

// NewDefaultResolver returns a PlanResolver that uses the canonical
// deterministic sampler.
func NewDefaultResolver() PlanResolver {
	return &defaultResolver{
		sampler: NewDeterministicSampler(),
	}
}

// NewResolverWithSampler returns a PlanResolver backed by a custom sampler.
func NewResolverWithSampler(sampler ClipSampler) PlanResolver {
	return &defaultResolver{
		sampler: sampler,
	}
}

// Resolve implements PlanResolver.
func (r *defaultResolver) Resolve(_ context.Context, spec BatchSpec) (*BatchPlan, error) {
	if spec.SourceURL == "" {
		return nil, fmt.Errorf("stockplan.resolver: source_url is required")
	}
	if len(spec.Groups) == 0 {
		return nil, fmt.Errorf("stockplan.resolver: at least one group is required")
	}

	policy := spec.Sampling
	policy.Normalize()

	plan := &BatchPlan{
		SourceURL:   spec.SourceURL,
		Destination: spec.Destination,
		Groups:      make([]PlannedGroup, 0, len(spec.Groups)),
	}

	for _, group := range spec.Groups {
		if group.Key == "" {
			return nil, fmt.Errorf("stockplan.resolver: group key is required")
		}

		clips, err := r.sampler.Sample(group, policy)
		if err != nil {
			return nil, fmt.Errorf("stockplan.resolver: sample group %q: %w", group.Key, err)
		}

		for i := range clips {
			clips[i].URL = spec.SourceURL
			clips[i].Slug = group.Key
			clips[i].ParentSlug = group.Key
		}

		plan.Groups = append(plan.Groups, PlannedGroup{
			Key:      group.Key,
			Title:    group.Title,
			StartSec: group.StartSec,
			EndSec:   group.EndSec,
			Clips:    clips,
		})
	}

	return plan, nil
}

// Compile-time assertion: defaultResolver satisfies PlanResolver.
var _ PlanResolver = (*defaultResolver)(nil)

// Compile-time assertion: deterministicSampler satisfies ClipSampler.
var _ ClipSampler = (*deterministicSampler)(nil)
