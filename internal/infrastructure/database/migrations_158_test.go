// Package storage — migrations_158_test.go holds the scenario tests
// for migration 158 (rights-extension columns). PR-CLIPINGEST-PIPELINE
// step 10 added 6 rights-extension columns to media_assets to bring
// the rights surface inline with the AssetLicense catalog and the
// rights_state.go canonical alphabet.
//
// Column ORDER matches canonical.go's CREATE TABLE block
// (godlike/06 SSOT — the canonical constant plus the migration
// SQL MUST agree on order).
//
// Covers:
//   - TestMigrations_158_RightsExtensionColumnsPresent
//   - TestMigrations_158_RightsExtensionDefaultsPermissive
//   - TestMigrations_158_RightsExtensionColumnsRoundTrip
package storage

import (
	"testing"
)

func TestMigrations_158_RightsExtensionColumnsPresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	seen := scanColumnNames(t, db, "media_assets")
	for _, col := range []string{
		"license_basis",
		"owner_channel_id",
		"allowed_channels",
		"allowed_regions",
		"expires_at",
		"review_status",
	} {
		if _, ok := seen[col]; !ok {
			t.Errorf("media_assets missing rights-extension column %q (added by migration 158; canonical.go in this package must mirror it)", col)
		}
	}
}

func TestMigrations_158_RightsExtensionDefaultsPermissive(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	const assetID = "rt-step10-defaults-1"
	_, err := db.Exec(
		`INSERT INTO media_assets (id, source, name, media_type, lifecycle_state)
		 VALUES (?, 'artlist', 'step10-defaults', 'video', 'ACTIVE')`,
		assetID,
	)
	if err != nil {
		t.Fatalf("insert step10-defaults row: %v", err)
	}
	var (
		licenseBasis    string
		ownerChannelID  string
		allowedChannels string
		allowedRegions  string
		expiresAt       string
		reviewStatus    string
	)
	if err := db.QueryRow(
		`SELECT license_basis, owner_channel_id, allowed_channels,
		        allowed_regions, expires_at, review_status
		 FROM media_assets WHERE id = ?`,
		assetID,
	).Scan(&licenseBasis, &ownerChannelID, &allowedChannels,
		&allowedRegions, &expiresAt, &reviewStatus); err != nil {
		t.Fatalf("read step10-defaults row: %v", err)
	}
	// license_basis + owner_channel_id + expires_at default ''.
	if licenseBasis != "" {
		t.Errorf("default license_basis should be ''; got %q", licenseBasis)
	}
	if ownerChannelID != "" {
		t.Errorf("default owner_channel_id should be ''; got %q", ownerChannelID)
	}
	if expiresAt != "" {
		t.Errorf("default expires_at should be ''; got %q", expiresAt)
	}
	// allowed_channels + allowed_regions default '[]' (JSON empty array).
	if allowedChannels != "[]" {
		t.Errorf("default allowed_channels should be '[]'; got %q", allowedChannels)
	}
	if allowedRegions != "[]" {
		t.Errorf("default allowed_regions should be '[]'; got %q", allowedRegions)
	}
	// review_status default 'none' (fail-OPEN on the review
	// dimension per rights_state.go's DefaultReviewStatus).
	if reviewStatus != "none" {
		t.Errorf("default review_status should be 'none'; got %q", reviewStatus)
	}
}

func TestMigrations_158_RightsExtensionColumnsRoundTrip(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	const assetID = "rt-step10-1"
	_, err := db.Exec(
		`INSERT INTO media_assets (
			id, source, name, media_type, lifecycle_state,
			license_basis, owner_channel_id, allowed_channels,
			allowed_regions, expires_at, review_status
		) VALUES (
			?, 'artlist', 'step10 round-trip', 'video', 'ACTIVE',
			?, ?, ?, ?, ?, ?
		)`,
		assetID,
		// license_basis — freeform pointer to AssetLicense.id
		// (operator workflow; not dereferenced on planner hot path).
		"license-asset-license-001",
		// owner_channel_id — single YouTube channel ID.
		"UC_step10_owner",
		// allowed_channels — JSON array (single-element for compactness).
		`["UC_step10_owner"]`,
		// allowed_regions — JSON array of ISO country codes.
		`["US","IT","DE"]`,
		// expires_at — RFC3339-numeric timestamp.
		"2030-01-01T00:00:00Z",
		// review_status — canonical alphabet value.
		"approved",
	)
	if err != nil {
		t.Fatalf("insert step10 round-trip row: %v", err)
	}
	var (
		gotLicenseBasis    string
		gotOwnerChannelID  string
		gotAllowedChannels string
		gotAllowedRegions  string
		gotExpiresAt       string
		gotReviewStatus    string
	)
	if err := db.QueryRow(
		`SELECT license_basis, owner_channel_id, allowed_channels,
		        allowed_regions, expires_at, review_status
		 FROM media_assets WHERE id = ?`,
		assetID,
	).Scan(&gotLicenseBasis, &gotOwnerChannelID, &gotAllowedChannels,
		&gotAllowedRegions, &gotExpiresAt, &gotReviewStatus); err != nil {
		t.Fatalf("read step10 round-trip row: %v", err)
	}
	expectations := map[string]string{
		"license_basis":    gotLicenseBasis,
		"owner_channel_id": gotOwnerChannelID,
		"allowed_channels": gotAllowedChannels,
		"allowed_regions":  gotAllowedRegions,
		"expires_at":       gotExpiresAt,
		"review_status":    gotReviewStatus,
	}
	wants := map[string]string{
		"license_basis":    "license-asset-license-001",
		"owner_channel_id": "UC_step10_owner",
		"allowed_channels": `["UC_step10_owner"]`,
		"allowed_regions":  `["US","IT","DE"]`,
		"expires_at":       "2030-01-01T00:00:00Z",
		"review_status":    "approved",
	}
	for col, got := range expectations {
		if got != wants[col] {
			t.Errorf("rights-extension round-trip %s = %q, want %q", col, got, wants[col])
		}
	}
}
