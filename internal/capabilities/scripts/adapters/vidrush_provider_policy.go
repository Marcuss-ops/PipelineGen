package adapters

import (
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type vidRushProviderPolicy struct {
	name           string
	acquireTimeout time.Duration
	budget         func(*scriptpkg.ResolvedGenerationPlan) int
}

var vidRushProviderPolicies = map[string]vidRushProviderPolicy{
	scriptpkg.VidRushProviderArtlist: {
		name: scriptpkg.VidRushProviderArtlist, acquireTimeout: vidRushArtlistAcquireTimeout,
		budget: func(*scriptpkg.ResolvedGenerationPlan) int { return vidRushArtlistAcquireBudget },
	},
	scriptpkg.VidRushProviderInternetImages: {
		name: scriptpkg.VidRushProviderInternetImages, acquireTimeout: vidRushImageAcquireTimeout,
		budget: func(plan *scriptpkg.ResolvedGenerationPlan) int {
			return imageAcquireBudget(plan, vidRushImageAcquireSlack)
		},
	},
	scriptpkg.VidRushProviderImageGeneration: {
		name: scriptpkg.VidRushProviderImageGeneration, acquireTimeout: vidRushGenerationAcquireTimeout,
		budget: func(plan *scriptpkg.ResolvedGenerationPlan) int { return imageAcquireBudget(plan, 0) },
	},
	scriptpkg.VidRushProviderYouTube: {
		name: scriptpkg.VidRushProviderYouTube, acquireTimeout: vidRushImageAcquireTimeout,
		budget: func(*scriptpkg.ResolvedGenerationPlan) int { return 0 },
	},
}

func providerPolicy(provider string) (vidRushProviderPolicy, bool) {
	policy, ok := vidRushProviderPolicies[provider]
	return policy, ok
}

func imageAcquireBudget(plan *scriptpkg.ResolvedGenerationPlan, slack int) int {
	target := vidRushImageTarget(plan)
	if target == 0 {
		target = vidRushDefaultImagesPerScene
	}
	return target + slack
}

func vidRushProviderTimeout(provider string) time.Duration {
	if policy, ok := providerPolicy(provider); ok {
		return policy.acquireTimeout
	}
	return vidRushImageAcquireTimeout
}

func vidRushAcquireBudget(plan *scriptpkg.ResolvedGenerationPlan, provider string) int {
	if policy, ok := providerPolicy(provider); ok && policy.budget != nil {
		return policy.budget(plan)
	}
	return 0
}
