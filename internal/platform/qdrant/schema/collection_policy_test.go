package schema

import "testing"

func TestValidateEmergencyCollection_AllowsPhysicalRecoveryCollection(t *testing.T) {
	for _, name := range []string{
		"recovery",
		"media_assets_v4_recovery_20260817_1712",
		"media_assets_candidate-01",
	} {
		if err := ValidateEmergencyCollection(name); err != nil {
			t.Fatalf("ValidateEmergencyCollection(%q): %v", name, err)
		}
	}
}

func TestValidateEmergencyCollection_RejectsAliasAndUnsafeNames(t *testing.T) {
	for _, name := range []string{
		"",
		CanonicalRuntimeAlias,
		"media_assets/recovery",
		"media_assets recovery",
		"media_assets;drop",
	} {
		if err := ValidateEmergencyCollection(name); err == nil {
			t.Fatalf("ValidateEmergencyCollection(%q) should fail", name)
		}
	}
}

func TestValidateRuntimeCollection_RemainsProductionOnly(t *testing.T) {
	if err := ValidateRuntimeCollection(ProductionCollection); err != nil {
		t.Fatalf("production collection must be valid: %v", err)
	}
	for _, name := range []string{
		CanonicalRuntimeAlias,
		"media_assets_v3",
		"media_assets_v4_recovery_20260817_1712",
	} {
		if err := ValidateRuntimeCollection(name); err == nil {
			t.Fatalf("runtime collection %q should fail validation", name)
		}
	}
}
