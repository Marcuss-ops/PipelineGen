package reconcile

import "testing"

func TestParseRecoverRegistryFlags_AllowsRepeatedAssetIDs(t *testing.T) {
	got, err := parseRecoverRegistryFlags([]string{
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
}

func TestParseRecoverRegistryFlags_ApplyAllIsExplicitlyBlocked(t *testing.T) {
	got, err := parseRecoverRegistryFlags([]string{"--collection=recovery", "--all", "--apply"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Apply || !got.All {
		t.Fatalf("unexpected flags: %+v", got)
	}
}

func TestParseRecoverRegistryFlags_ApplyNeedsScope(t *testing.T) {
	if _, err := parseRecoverRegistryFlags([]string{"--collection=recovery", "--apply"}); err == nil {
		t.Fatal("expected --apply without --asset-id/--all to fail")
	}
}

func TestParseRecoverRegistryFlags_AllAndAssetIDAreMutuallyExclusive(t *testing.T) {
	if _, err := parseRecoverRegistryFlags([]string{"--collection=recovery", "--all", "--asset-id=yt_video_1_2_v1"}); err == nil {
		t.Fatal("expected --all and --asset-id to fail")
	}
}
