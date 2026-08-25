package audit

import "testing"

func TestSelectTrueOrphans_SkipsNowReferenced(t *testing.T) {
	orphans := []clipDriveOrphan{
		{FileID: "orphan-1", Name: "yt_a_1_v1_x.mp4"},
		{FileID: "orphan-2", Name: "yt_b_1_v1_x.mp4"},
		{FileID: "orphan-3", Name: "yt_c_1_v1_x.mp4"},
	}
	referenced := map[string]struct{}{
		"orphan-2": {},
	}

	got := selectTrueOrphans(orphans, referenced)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 (orphan-2 skipped as now-referenced)", len(got))
	}
	if got[0].FileID != "orphan-1" || got[1].FileID != "orphan-3" {
		t.Fatalf("got %+v, want orphan-1 + orphan-3", got)
	}
}

func TestSelectTrueOrphans_EmptyReferencedKeepsAll(t *testing.T) {
	orphans := []clipDriveOrphan{{FileID: "o1", Name: "n1.mp4"}}

	got := selectTrueOrphans(orphans, nil)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
}
