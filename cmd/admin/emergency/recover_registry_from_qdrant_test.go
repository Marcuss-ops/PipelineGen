package emergency

import "testing"

func TestParseRecoverRegistryFlags_AllowsRepeatedAssetIDs(t *testing.T) {
	got, err := parseRecoverRegistryFlags([]string{
		"--purpose=forensics",
		"--collection=recovery",
		"--asset-id=yt_video_1_2_v1",
		"--asset-id=yt_video_3_4_v1",
		"--apply",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Apply || got.All || len(got.AssetIDs) != 2 {
		t.Fatalf("unexpected flags: %+v", got)
	}
	if got.Purpose != purposeForensics {
		t.Fatalf("purpose=%q, want %q", got.Purpose, purposeForensics)
	}
}

func TestParseRecoverRegistryFlags_ApplyAllIsExplicitlyBlocked(t *testing.T) {
	got, err := parseRecoverRegistryFlags([]string{
		"--purpose=disaster-recovery",
		"--collection=recovery",
		"--all",
		"--apply",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Apply || !got.All {
		t.Fatalf("unexpected flags: %+v", got)
	}
}

func TestParseRecoverRegistryFlags_ApplyNeedsScope(t *testing.T) {
	if _, err := parseRecoverRegistryFlags([]string{
		"--purpose=migration-recovery",
		"--collection=recovery",
		"--apply",
	}); err == nil {
		t.Fatal("expected --apply without --asset-id/--all to fail")
	}
}

func TestParseRecoverRegistryFlags_AllAndAssetIDAreMutuallyExclusive(t *testing.T) {
	if _, err := parseRecoverRegistryFlags([]string{
		"--purpose=forensics",
		"--collection=recovery",
		"--all",
		"--asset-id=yt_video_1_2_v1",
	}); err == nil {
		t.Fatal("expected --all and --asset-id to fail")
	}
}

func TestParseRecoverRegistryFlags_RequiresEmergencyPurpose(t *testing.T) {
	if _, err := parseRecoverRegistryFlags([]string{
		"--collection=recovery",
		"--asset-id=yt_video_1_2_v1",
	}); err == nil {
		t.Fatal("expected missing --purpose to fail closed")
	}
}

func TestParseRecoverRegistryFlags_RejectsOperationalPurpose(t *testing.T) {
	if _, err := parseRecoverRegistryFlags([]string{
		"--purpose=sync",
		"--collection=recovery",
		"--asset-id=yt_video_1_2_v1",
	}); err == nil {
		t.Fatal("expected operational sync purpose to fail closed")
	}
}
