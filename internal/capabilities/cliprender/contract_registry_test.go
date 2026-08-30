package cliprender

import (
	"errors"
	"testing"
)

func TestContractTablesAreValidAtBoot(t *testing.T) {
	if err := validateContractTables(); err != nil {
		t.Fatalf("contract tables must be valid: %v", err)
	}
}

func TestValidateContractTablesRejectsDuplicateDimensions(t *testing.T) {
	checks := append([]contractCheck(nil), contractChecks...)
	checks = append(checks, contractChecks[0])
	if err := validateContractCheckTable(checks); err == nil {
		t.Fatal("duplicate contract dimensions must be rejected")
	}
}

func TestV2ResolverRejectsNonCanonicalFPS(t *testing.T) {
	req := &RenderRequest{Output: &OutputSpec{
		Contract: OutputContractVeloxAssemblyReadyV2,
		Width:    1920, Height: 1080, FPSNum: 30000, FPSDen: 1001,
	}}
	if _, err := NewContractResolver().Resolve(nil, req); err == nil || !errors.Is(err, ErrOutputContractMismatch) {
		t.Fatalf("expected OUTPUT_CONTRACT_MISMATCH, got %v", err)
	}
}

func TestV2ResolverAcceptsCanonicalFPS(t *testing.T) {
	req := &RenderRequest{Output: &OutputSpec{
		Contract: OutputContractVeloxAssemblyReadyV2,
		Width:    1920, Height: 1080, FPSNum: 24, FPSDen: 1,
	}}
	resolved, err := NewContractResolver().Resolve(nil, req)
	if err != nil {
		t.Fatalf("canonical FPS must resolve: %v", err)
	}
	if resolved.FPSNum != 24 || resolved.FPSDen != 1 {
		t.Fatalf("unexpected resolved FPS: %d/%d", resolved.FPSNum, resolved.FPSDen)
	}
}

func TestValidateContractTablesRejectsInvalidRegistryEntry(t *testing.T) {
	builders := map[string]func(*RenderRequest) (*ResolvedContract, error){
		"": nil,
	}
	if err := validateContractBuilderRegistry(builders); err == nil {
		t.Fatal("empty ID and nil builder must be rejected")
	}
}
