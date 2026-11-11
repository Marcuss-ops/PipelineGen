package client

import (
	"encoding/json"
	"strings"
	"testing"
)

func validPreparedRenderPackageRef() *PreparedRenderPackageRef {
	return &PreparedRenderPackageRef{
		PackageID: strings.Repeat("a", 64), PlanSHA256: strings.Repeat("b", 64),
		AssetManifestSHA: strings.Repeat("c", 64), TotalFrames: 200, CompilerVersion: "chronon-3.4.1",
		ProfileID: "velox-h264-1080p30-v1",
	}
}

func TestPreparedRenderPackageRefValidatesAndRoundTrips(t *testing.T) {
	ref := validPreparedRenderPackageRef()
	if err := ref.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	raw, err := json.Marshal(Job{ID: "job-1", PreparedRenderPackage: ref})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Job
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.PreparedRenderPackage == nil || *got.PreparedRenderPackage != *ref {
		t.Fatalf("prepared package ref = %+v, want %+v", got.PreparedRenderPackage, ref)
	}
}

func TestPreparedRenderPackageRefRejectsIncompleteOrNonCanonicalIdentity(t *testing.T) {
	cases := []struct {
		name string
		edit func(*PreparedRenderPackageRef)
	}{
		{"missing package id", func(ref *PreparedRenderPackageRef) { ref.PackageID = "" }},
		{"short plan hash", func(ref *PreparedRenderPackageRef) { ref.PlanSHA256 = "abcd" }},
		{"uppercase asset hash", func(ref *PreparedRenderPackageRef) { ref.AssetManifestSHA = strings.Repeat("C", 64) }},
		{"missing compiler", func(ref *PreparedRenderPackageRef) { ref.CompilerVersion = " " }},
		{"missing profile", func(ref *PreparedRenderPackageRef) { ref.ProfileID = " " }},
		{"missing frame total", func(ref *PreparedRenderPackageRef) { ref.TotalFrames = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := *validPreparedRenderPackageRef()
			tc.edit(&ref)
			if err := ref.Validate(); err == nil {
				t.Fatal("expected invalid prepared package reference to be rejected")
			}
		})
	}
}
