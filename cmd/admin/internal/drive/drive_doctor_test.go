package drive

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	sqlitedelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/delivery"
)

func destByName(t *testing.T, report doctorReport, dest string) doctorDestination {
	t.Helper()
	for _, d := range report.Destinations {
		if d.Destination == dest {
			return d
		}
	}
	t.Fatalf("report has no destination %q (have %+v)", dest, report.Destinations)
	return doctorDestination{}
}

func TestBuildDoctorReport_EmptyCatalog(t *testing.T) {
	report := buildDoctorReport("", &config.Config{}, nil)

	if report.TotalEntries != 0 {
		t.Errorf("TotalEntries = %d, want 0", report.TotalEntries)
	}
	if len(report.Destinations) != len(canonicalDriveNamespaces) {
		t.Fatalf("Destinations = %d, want %d canonical entries", len(report.Destinations), len(canonicalDriveNamespaces))
	}
	for _, d := range report.Destinations {
		if d.Statuses != "missing" {
			t.Errorf("empty catalog: destination %q statuses = %q, want missing", d.Destination, d.Statuses)
		}
		if d.FolderCount != 0 {
			t.Errorf("empty catalog: destination %q count = %d, want 0", d.Destination, d.FolderCount)
		}
	}
}

func TestBuildDoctorReport_GroupsCanonicalAndExtras(t *testing.T) {
	entries := []sqlitedelivery.CatalogEntry{
		{Destination: "youtube_clip", Namespace: "clips", Status: sqlitedelivery.StatusActive},
		{Destination: "youtube_clip", Namespace: "clips", Status: sqlitedelivery.StatusActive},
		{Destination: "stock", Namespace: "stock", Status: sqlitedelivery.StatusActive},
		{Destination: "stock", Namespace: "stock", Status: sqlitedelivery.StatusInvalid},
		{Destination: "rogue_dest", Namespace: "rogue", Status: sqlitedelivery.StatusMissing},
	}

	report := buildDoctorReport("", &config.Config{}, entries)

	if report.TotalEntries != 5 {
		t.Errorf("TotalEntries = %d, want 5", report.TotalEntries)
	}
	// 9 canonical + 1 non-canonical.
	if len(report.Destinations) != len(canonicalDriveNamespaces)+1 {
		t.Fatalf("Destinations = %d, want %d", len(report.Destinations), len(canonicalDriveNamespaces)+1)
	}
	// Canonical order is preserved.
	if report.Destinations[0].Destination != "youtube_clip" {
		t.Errorf("Destinations[0] = %q, want youtube_clip", report.Destinations[0].Destination)
	}

	clips := destByName(t, report, "youtube_clip")
	if clips.FolderCount != 2 || clips.Statuses != "active=2" {
		t.Errorf("youtube_clip = %+v, want count 2 / active=2", clips)
	}
	stock := destByName(t, report, "stock")
	if stock.FolderCount != 2 || stock.Statuses != "active=1, invalid=1" {
		t.Errorf("stock = %+v, want count 2 / \"active=1, invalid=1\"", stock)
	}
	artlist := destByName(t, report, "artlist")
	if artlist.Statuses != "missing" || artlist.Namespace != "artlist" {
		t.Errorf("artlist = %+v, want missing with namespace artlist", artlist)
	}
	rogue := destByName(t, report, "rogue_dest")
	if rogue.Namespace != "rogue" || rogue.FolderCount != 1 || rogue.Statuses != "missing=1" {
		t.Errorf("rogue_dest = %+v, want rogue/1/missing=1", rogue)
	}
}

func TestBuildDoctorReport_FlagRootIsConfigured(t *testing.T) {
	report := buildDoctorReport("flag-root", &config.Config{}, nil)
	if report.Root != "flag-root" || report.RootSource != "flag" {
		t.Fatalf("root = %q/%q, want flag-root/flag", report.Root, report.RootSource)
	}
	if !report.RootConfigured {
		t.Error("RootConfigured = false, want true for --root")
	}
}

func TestBuildDoctorReport_ConfigRootIsConfigured(t *testing.T) {
	cfg := &config.Config{Drive: config.DriveConfig{MediaRootFolder: "cfg-root"}}
	report := buildDoctorReport("", cfg, nil)
	if report.Root != "cfg-root" || report.RootSource != "config" || !report.RootConfigured {
		t.Fatalf("root = %q/%q configured=%t, want cfg-root/config/true", report.Root, report.RootSource, report.RootConfigured)
	}
}

func TestBuildDoctorReport_DefaultRootNotConfigured(t *testing.T) {
	report := buildDoctorReport("", &config.Config{}, nil)
	if report.Root != config.DefaultMediaRootFolderID || report.RootSource != "default" {
		t.Fatalf("root = %q/%q, want default/%q", report.Root, report.RootSource, config.DefaultMediaRootFolderID)
	}
	if report.RootConfigured {
		t.Error("RootConfigured = true for a default root, want false")
	}
}

func TestFormatStatuses_DeterministicSorted(t *testing.T) {
	got := formatStatuses(map[string]int{"missing": 1, "active": 2, "invalid": 3})
	if got != "active=2, invalid=3, missing=1" {
		t.Fatalf("formatStatuses = %q, want sorted \"active=2, invalid=3, missing=1\"", got)
	}
	if formatStatuses(map[string]int{}) != "none" {
		t.Fatalf("formatStatuses(empty) = %q, want none", formatStatuses(map[string]int{}))
	}
}
