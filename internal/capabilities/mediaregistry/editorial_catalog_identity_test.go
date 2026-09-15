package mediaregistry

import "testing"

func TestEditorialCatalogIsCanonicalAcrossDomains(t *testing.T) {
	if err := ValidateEditorialCatalog(); err != nil {
		t.Fatal(err)
	}
	ids := EditorialAssetIdentities()
	// 6 background plates + 6 BGM + 6 whop + 3 bound whoosh.
	if len(ids) != 21 {
		t.Fatalf("unified editorial identities = %d, want 21", len(ids))
	}
	if ids["drive-background-01"] != "16j67if3LUqMeVVSpOjPd5rD0cPwn9l0A" {
		t.Errorf("drive-background-01 identity = %q", ids["drive-background-01"])
	}
	if ids["bgm3"] != "1BiVWCTGOLnaeLmg8lTSSuDzo_gWWz0jq" {
		t.Errorf("bgm3 identity = %q", ids["bgm3"])
	}
	if ids["whop1"] != "1Fgr2jWQC1G6EHo-jhBAwjGtdcZo1PfaX" {
		t.Errorf("whop1 identity = %q", ids["whop1"])
	}
}

func TestValidateEditorialCatalogIdentityRejectsCrossDomainCollisions(t *testing.T) {
	backgrounds := []EditorialBackgroundAsset{{ID: "plate-01", DriveFileID: "drive-bg"}}

	// Same alias bound by two domains.
	aliasCollision := []EditorialAudioAsset{{Alias: "plate-01", DriveFileID: "drive-audio"}}
	if err := validateEditorialCatalogIdentity(backgrounds, aliasCollision); err == nil {
		t.Fatal("alias collision across domains must fail closed")
	}

	// Different alias, same Drive identity.
	identityCollision := []EditorialAudioAsset{{Alias: "bgm-x", DriveFileID: "drive-bg"}}
	if err := validateEditorialCatalogIdentity(backgrounds, identityCollision); err == nil {
		t.Fatal("shared Drive identity across domains must fail closed")
	}

	// Disjoint domains are accepted.
	clean := []EditorialAudioAsset{{Alias: "bgm-x", DriveFileID: "drive-audio"}}
	if err := validateEditorialCatalogIdentity(backgrounds, clean); err != nil {
		t.Fatalf("disjoint domains rejected: %v", err)
	}
}
