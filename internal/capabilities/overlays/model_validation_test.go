package overlays

import "testing"

func TestOverlayPlanValidateRejectsDuplicateItemIDs(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	plan.Items[1].ID = plan.Items[0].ID

	if err := plan.Validate(); err == nil {
		t.Fatal("expected duplicate item id to be rejected")
	}
}

func TestOverlayPlanValidateRejectsMissingAssetID(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	plan.Items[0].AssetRefs[0].AssetID = ""

	if err := plan.Validate(); err == nil {
		t.Fatal("expected asset without asset_id to be rejected")
	}
}

func TestOverlayPlanValidateRejectsStaleFingerprint(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	plan.Items[1].Text = "PIANO MODIFICATO"

	if err := plan.Validate(); err == nil {
		t.Fatal("expected stale fingerprint to be rejected")
	}
}

func TestOverlayPlanValidateRemainsIdempotent(t *testing.T) {
	plan := GoldenOverlayPlanV1()
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	fingerprint := plan.Fingerprint
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if plan.Fingerprint != fingerprint {
		t.Fatalf("fingerprint changed on repeated validation: %q != %q", plan.Fingerprint, fingerprint)
	}
}
