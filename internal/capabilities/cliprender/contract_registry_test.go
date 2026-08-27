package cliprender

import (
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

func TestValidateContractTablesRejectsInvalidRegistryEntry(t *testing.T) {
	builders := map[string]func(*RenderRequest) (*ResolvedContract, error){
		"": nil,
	}
	if err := validateContractBuilderRegistry(builders); err == nil {
		t.Fatal("empty ID and nil builder must be rejected")
	}
}
