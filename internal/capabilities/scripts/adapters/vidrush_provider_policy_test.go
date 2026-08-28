package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushProviderPolicyCentralizesTimeoutAndBudget(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 2}
	for _, provider := range []string{
		scriptpkg.VidRushProviderArtlist,
		scriptpkg.VidRushProviderInternetImages,
		scriptpkg.VidRushProviderImageGeneration,
		scriptpkg.VidRushProviderYouTube,
	} {
		policy, ok := providerPolicy(provider)
		if !ok || policy.name != provider {
			t.Fatalf("policy for %q = %+v, found=%t", provider, policy, ok)
		}
		if vidRushProviderTimeout(provider) <= 0 {
			t.Fatalf("timeout for %q must be positive", provider)
		}
	}
	if got := vidRushAcquireBudget(plan, scriptpkg.VidRushProviderArtlist); got != vidRushArtlistAcquireBudget {
		t.Fatalf("Artlist budget = %d, want %d", got, vidRushArtlistAcquireBudget)
	}
	if got := vidRushAcquireBudget(plan, scriptpkg.VidRushProviderInternetImages); got != 22 {
		t.Fatalf("internet-images budget = %d, want 22", got)
	}
	if got := vidRushAcquireBudget(plan, scriptpkg.VidRushProviderImageGeneration); got != 2 {
		t.Fatalf("generation budget = %d, want 2", got)
	}
}

func TestVidRushProviderPolicyRejectsUnknownProvider(t *testing.T) {
	if _, ok := providerPolicy("unknown"); ok {
		t.Fatal("unknown provider must not have a policy")
	}
	if got := vidRushAcquireBudget(nil, "unknown"); got != 0 {
		t.Fatalf("unknown provider budget = %d, want 0", got)
	}
}
