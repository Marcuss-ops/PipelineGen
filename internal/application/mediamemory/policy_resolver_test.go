package mediamemory

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

func ptr(b bool) *bool { return &b }

// TestResolutionPolicyResolver_Defaults verifies that the zero
// OptionalResolvePolicy resolves to the conservative canonical
// defaults.
func TestResolutionPolicyResolver_Defaults(t *testing.T) {
	r := NewResolutionPolicyResolver()
	policy := r.Resolve("it", OptionalResolvePolicy{})

	if !policy.PreferApprovedBindings {
		t.Errorf("PreferApprovedBindings: want true, got false")
	}
	if policy.AllowExternalSearch {
		t.Errorf("AllowExternalSearch: want false, got true")
	}
	if policy.AvoidRecentAssets {
		t.Errorf("AvoidRecentAssets: want false, got true")
	}
	if policy.MaxCandidatesPerSlot != defaultMaxCandidatesPerSlot {
		t.Errorf("MaxCandidatesPerSlot: want %d, got %d", defaultMaxCandidatesPerSlot, policy.MaxCandidatesPerSlot)
	}
	if !policy.SearchPolicy.CacheRead {
		t.Errorf("SearchPolicy.CacheRead: want true, got false")
	}
	if policy.SearchPolicy.Mode != media.SearchModeANN {
		t.Errorf("SearchPolicy.Mode: want %q, got %q", media.SearchModeANN, policy.SearchPolicy.Mode)
	}
	if policy.SearchPolicy.AllowExternal {
		t.Errorf("SearchPolicy.AllowExternal: want false, got true")
	}
	if policy.SearchPolicy.MaxCandidates != defaultMaxCandidatesPerSlot {
		t.Errorf("SearchPolicy.MaxCandidates: want %d, got %d", defaultMaxCandidatesPerSlot, policy.SearchPolicy.MaxCandidates)
	}
}

// TestResolutionPolicyResolver_ExplicitFalseOverridesDefaults
// verifies that callers can explicitly set a boolean to false.
func TestResolutionPolicyResolver_ExplicitFalseOverridesDefaults(t *testing.T) {
	r := NewResolutionPolicyResolver()
	policy := r.Resolve("it", OptionalResolvePolicy{
		PreferApprovedBindings: ptr(false),
		AllowExternalSearch:    ptr(true),
		AvoidRecentAssets:      ptr(true),
		CacheRead:              ptr(false),
	})

	if policy.PreferApprovedBindings {
		t.Errorf("PreferApprovedBindings: want false, got true")
	}
	if !policy.AllowExternalSearch {
		t.Errorf("AllowExternalSearch: want true, got false")
	}
	if !policy.AvoidRecentAssets {
		t.Errorf("AvoidRecentAssets: want true, got false")
	}
	if policy.SearchPolicy.CacheRead {
		t.Errorf("SearchPolicy.CacheRead: want false, got true")
	}
	if !policy.SearchPolicy.AllowExternal {
		t.Errorf("SearchPolicy.AllowExternal: want true, got false")
	}
}

// TestResolutionPolicyResolver_ExplicitTrueOverridesDefaults
// verifies that callers can explicitly set a boolean to true.
func TestResolutionPolicyResolver_ExplicitTrueOverridesDefaults(t *testing.T) {
	r := NewResolutionPolicyResolver()
	policy := r.Resolve("it", OptionalResolvePolicy{
		PreferApprovedBindings: ptr(true),
		AllowExternalSearch:    ptr(false),
		AvoidRecentAssets:      ptr(false),
		CacheRead:              ptr(true),
	})

	if !policy.PreferApprovedBindings {
		t.Errorf("PreferApprovedBindings: want true, got false")
	}
	if policy.AllowExternalSearch {
		t.Errorf("AllowExternalSearch: want false, got true")
	}
	if policy.AvoidRecentAssets {
		t.Errorf("AvoidRecentAssets: want false, got true")
	}
	if !policy.SearchPolicy.CacheRead {
		t.Errorf("SearchPolicy.CacheRead: want true, got false")
	}
}

// TestResolutionPolicyResolver_MaxCandidates verifies that a
// positive MaxCandidatesPerSlot is forwarded and propagated into
// SearchPolicy.
func TestResolutionPolicyResolver_MaxCandidates(t *testing.T) {
	r := NewResolutionPolicyResolver()
	policy := r.Resolve("it", OptionalResolvePolicy{MaxCandidatesPerSlot: 42})
	if policy.MaxCandidatesPerSlot != 42 {
		t.Errorf("MaxCandidatesPerSlot: want 42, got %d", policy.MaxCandidatesPerSlot)
	}
	if policy.SearchPolicy.MaxCandidates != 42 {
		t.Errorf("SearchPolicy.MaxCandidates: want 42, got %d", policy.SearchPolicy.MaxCandidates)
	}
}

// TestResolutionPolicyResolver_ModeAndProviders verifies that the
// mode and allowed providers are forwarded into SearchPolicy.
func TestResolutionPolicyResolver_ModeAndProviders(t *testing.T) {
	r := NewResolutionPolicyResolver()
	policy := r.Resolve("it", OptionalResolvePolicy{
		Mode:             "hybrid",
		AllowedProviders: []string{"artlist", "youtube"},
	})
	if policy.SearchPolicy.Mode != media.SearchModeHybrid {
		t.Errorf("Mode: want %q, got %q", media.SearchModeHybrid, policy.SearchPolicy.Mode)
	}
	if len(policy.SearchPolicy.AllowedProviders) != 2 {
		t.Errorf("AllowedProviders: want 2 entries, got %d", len(policy.SearchPolicy.AllowedProviders))
	}
}
