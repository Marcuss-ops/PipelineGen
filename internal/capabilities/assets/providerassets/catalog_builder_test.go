package assets

import (
	"errors"
	"testing"
)

func TestCatalogBuilderAppliesPolicyAndFreezesRegistry(t *testing.T) {
	policies, err := NewProviderPolicyRegistry([]ProviderPolicy{
		{Name: "artlist", Enabled: true, MediaType: "video", Priority: 10},
		{Name: "pexels", Enabled: true, MediaType: "image", Priority: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	builder := NewCatalogBuilder(policies)
	if err := builder.Add(&fakeAdapter{name: "artlist"}); err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(&fakeAdapter{name: "pexels"}); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !registry.IsFrozen() {
		t.Fatal("provider catalog must be frozen after Build")
	}
	if got, want := registry.Names(), []string{"artlist", "pexels"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	if got := policies.Names(); len(got) != 2 || got[0] != "artlist" || got[1] != "pexels" {
		t.Fatalf("policy Names() = %v", got)
	}
}

func TestCatalogBuilderRejectsDisabledProvider(t *testing.T) {
	policies, err := NewProviderPolicyRegistry([]ProviderPolicy{{Name: "artlist", Enabled: false}})
	if err != nil {
		t.Fatal(err)
	}
	builder := NewCatalogBuilder(policies)
	_ = builder.Add(&fakeAdapter{name: "artlist"})
	_, err = builder.Build()
	if !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("Build error = %v, want ErrProviderDisabled", err)
	}
}

func TestCatalogBuilderRejectsNilPolicy(t *testing.T) {
	builder := NewCatalogBuilder(nil)
	_ = builder.Add(&fakeAdapter{name: "artlist"})
	_, err := builder.Build()
	if !errors.Is(err, ErrNilProviderPolicy) {
		t.Fatalf("Build error = %v, want ErrNilProviderPolicy", err)
	}
}

func TestCatalogBuilderRejectsUnlistedProvider(t *testing.T) {
	policies, err := NewProviderPolicyRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	builder := NewCatalogBuilder(policies)
	_ = builder.Add(&fakeAdapter{name: "artlist"})
	_, err = builder.Build()
	if !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("Build error = %v, want ErrProviderDisabled", err)
	}
}

func TestProviderPolicyRegistryRejectsDuplicate(t *testing.T) {
	_, err := NewProviderPolicyRegistry([]ProviderPolicy{{Name: "artlist"}, {Name: "artlist"}})
	if !errors.Is(err, ErrDuplicatePolicy) {
		t.Fatalf("error = %v, want ErrDuplicatePolicy", err)
	}
}

var _ ProviderAdapter = (*fakeAdapter)(nil)
