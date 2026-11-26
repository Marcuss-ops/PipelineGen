// cmd/admin/rehydrate_clip_test.go — pure-function tests for rehydrate-clip.
//
// The runtime path (Drive download → probe → canonical commit) needs the live
// composition root; the tests below pin the part that decides the asset
// identity, because that is what the YouTube uniqueness index depends on: a
// wrong start/end or a missing policy version collapses the whole clip set
// onto one row.

package drive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureInventory(t *testing.T) *driveInventoryDoc {
	t.Helper()
	return &driveInventoryDoc{Tree: &inventoryNode{
		Children: []inventoryNode{
			{
				Files: []inventoryFile{
					{Name: "yt_JP6BrYSa_yw_0_58_v1_vantaggi-e-svantaggi.mp4", ID: "drive-0", Size: 100, MimeType: "video/mp4", Path: "Saggezza/Ilary/yt_JP6BrYSa_yw_0_58_v1.mp4"},
					{Name: "yt_JP6BrYSa_yw_117_150_v1_il-successo.mp4", ID: "drive-117", Size: 200, MimeType: "video/mp4"},
					{Name: "cover.jpg", ID: "drive-cover", MimeType: "image/jpeg"},
					{Name: "yt_OTHERVIDEO_0_10_v1_x.mp4", ID: "drive-other", MimeType: "video/mp4"},
				},
			},
			{
				Children: []inventoryNode{{
					Files: []inventoryFile{
						{Name: "yt_JP6BrYSa_yw_58_110_v1_visibilita.mp4", ID: "drive-58", Size: 300, MimeType: "video/mp4"},
					},
				}},
			},
		},
	}}
}

func TestCollectRehydrateCandidatesFiltersAndSorts(t *testing.T) {
	cands, err := collectRehydrateCandidates(fixtureInventory(t), "JP6BrYSa_yw")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(cands) != 3 {
		t.Fatalf("candidates = %d, want 3 (%+v)", len(cands), cands)
	}
	if cands[0].ClipID != "yt_JP6BrYSa_yw_0_58_v1" || cands[1].ClipID != "yt_JP6BrYSa_yw_58_110_v1" || cands[2].ClipID != "yt_JP6BrYSa_yw_117_150_v1" {
		t.Fatalf("order = %s, %s, %s", cands[0].ClipID, cands[1].ClipID, cands[2].ClipID)
	}
	if cands[1].StartMs != 58000 || cands[1].EndMs != 110000 {
		t.Fatalf("window = %d..%d, want 58000..110000", cands[1].StartMs, cands[1].EndMs)
	}
	if cands[0].PolicyVersion != "v1" {
		t.Fatalf("policy = %q, want v1", cands[0].PolicyVersion)
	}
	if !strings.HasPrefix(cands[0].Name, "Vantaggi") {
		t.Fatalf("display name = %q", cands[0].Name)
	}
}

func TestCollectRehydrateCandidatesRejectsDuplicateIdentity(t *testing.T) {
	doc := &driveInventoryDoc{Tree: &inventoryNode{Files: []inventoryFile{
		{Name: "yt_JP6BrYSa_yw_0_58_v1_a.mp4", ID: "drive-a", MimeType: "video/mp4"},
		{Name: "yt_JP6BrYSa_yw_0_58_v1_b.mp4", ID: "drive-b", MimeType: "video/mp4"},
	}}}
	if _, err := collectRehydrateCandidates(doc, "JP6BrYSa_yw"); err == nil {
		t.Fatal("expected a duplicate-identity error")
	}
}

func TestCollectRehydrateCandidatesRejectsInvalidWindow(t *testing.T) {
	doc := &driveInventoryDoc{Tree: &inventoryNode{Files: []inventoryFile{
		{Name: "yt_JP6BrYSa_yw_80_20_v1_x.mp4", ID: "drive-x", MimeType: "video/mp4"},
	}}}
	if _, err := collectRehydrateCandidates(doc, "JP6BrYSa_yw"); err == nil {
		t.Fatal("expected an invalid-window error")
	}
}

func TestLoadDriveInventoryFailClosed(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDriveInventory(bad); err == nil {
		t.Fatal("expected a decode error")
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDriveInventory(empty); err == nil {
		t.Fatal("expected a missing-tree error")
	}
}

func TestBuildRehydrateCommitRequestCarriesCanonicalIdentity(t *testing.T) {
	cand := rehydrateCandidate{
		ClipID: "yt_JP6BrYSa_yw_0_58_v1", VideoID: "JP6BrYSa_yw",
		DriveFileID: "drive-0", StartMs: 0, EndMs: 58000, PolicyVersion: "v1",
		Name: "Vantaggi e svantaggi", Filename: "yt_JP6BrYSa_yw_0_58_v1_vantaggi.mp4",
	}
	req, err := buildRehydrateCommitRequest(cand, "/data/media/clips/general/yt_JP6BrYSa_yw/clip.mp4",
		"general", "JP6BrYSa_yw", 58*time.Second, strings.Repeat("a", 64), 12345, true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if req.AssetID != cand.ClipID || req.Source != "youtube" || req.SourceProvider != "youtube" {
		t.Fatalf("identity drifted: %+v", req)
	}
	if req.SourceVideoID != "JP6BrYSa_yw" || req.StartMs != 0 || req.EndMs != 58000 || req.PolicyVersion != "v1" {
		t.Fatalf("source window drifted: video=%q window=%d..%d policy=%q",
			req.SourceVideoID, req.StartMs, req.EndMs, req.PolicyVersion)
	}
	if req.Taxonomy.IsZero() || req.Taxonomy.AssetID != cand.ClipID {
		t.Fatalf("taxonomy not resolved: %+v", req.Taxonomy)
	}
	if len(req.Locations) != 2 || req.Locations[0].Kind != "drive" || req.Locations[1].Kind != "local" {
		t.Fatalf("locations = %+v", req.Locations)
	}
	if req.Metadata.Extra["drive_file_id"] != "drive-0" || req.Metadata.Extra["local_path"] == "" {
		t.Fatalf("metadata extras = %+v", req.Metadata.Extra)
	}
	if !req.EmitIndexEvent {
		t.Fatal("a restored clip must emit the index event")
	}
}

func TestBuildRehydrateCommitRequestFailClosed(t *testing.T) {
	base := rehydrateCandidate{ClipID: "yt_x_0_1_v1", StartMs: 0, EndMs: 1000, PolicyVersion: "v1", Filename: "x.mp4"}
	inverted := base
	inverted.EndMs = 0
	cases := []struct {
		name string
		cand rehydrateCandidate
		hash string
	}{
		{"empty id", rehydrateCandidate{StartMs: 0, EndMs: 1000}, strings.Repeat("a", 64)},
		{"invalid window", inverted, strings.Repeat("a", 64)},
		{"missing hash", base, ""},
	}
	for _, tc := range cases {
		if _, err := buildRehydrateCommitRequest(tc.cand, "/tmp/x.mp4", "general", "x", time.Second, tc.hash, 1, true); err == nil {
			t.Fatalf("%s: expected an error", tc.name)
		}
	}
	zero := base
	zero.EndMs = 0
	if _, err := buildRehydrateCommitRequest(zero, "/tmp/x.mp4", "general", "x", time.Second, strings.Repeat("a", 64), 1, true); err == nil {
		t.Fatal("zero-length window must fail closed")
	}
}

// TestBuildRehydrateCommitRequestOmitsVanishedDriveCopy pins the from-local
// path: when the published copy no longer exists, the commit must not invent a
// Drive location (the resolver would trust a link that 404s).
func TestBuildRehydrateCommitRequestOmitsVanishedDriveCopy(t *testing.T) {
	cand := rehydrateCandidate{ClipID: "yt_JP6BrYSa_yw_0_58_v1", VideoID: "JP6BrYSa_yw", StartMs: 0, EndMs: 58000, PolicyVersion: "v1", Filename: "x.mp4"}
	req, err := buildRehydrateCommitRequest(cand, "/data/x.mp4", "general", "JP6BrYSa_yw", 58*time.Second, strings.Repeat("b", 64), 42, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(req.Locations) != 1 || req.Locations[0].Kind != "local" || !req.Locations[0].IsPrimary {
		t.Fatalf("locations = %+v", req.Locations)
	}
	if _, ok := req.Metadata.Extra["drive_link"]; ok {
		t.Fatalf("drive_link must not be fabricated: %+v", req.Metadata.Extra)
	}
}

func TestClipDisplayName(t *testing.T) {
	if got := clipDisplayName("yt_v_0_1_v1_hook-iconico--sposare-totti.mp4", "yt_v_0_1_v1_"); got != "Hook iconico sposare totti" {
		t.Fatalf("display name = %q", got)
	}
	if got := clipDisplayName("yt_v_0_1_v1.mp4", "yt_v_0_1_v1"); got != "yt_v_0_1_v1" {
		t.Fatalf("fallback display name = %q", got)
	}
}
