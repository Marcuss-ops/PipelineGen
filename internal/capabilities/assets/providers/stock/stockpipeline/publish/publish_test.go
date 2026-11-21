package publish

import "testing"

func TestNamingPreservesVerifiedFolderAndExplicitClipRules(t *testing.T) {
	in := NamingInput{FolderName: "Round 7", DriveFolderID: "drive-1", DriveFolderResolved: true}
	if got := RootFolderName(in); got != "Round 7" {
		t.Fatalf("RootFolderName() = %q", got)
	}
	if got := ResolvedFolderID(in); got != "drive-1" {
		t.Fatalf("ResolvedFolderID() = %q", got)
	}
	if got := ClipFolderName(in, ClipNamingInput{Round: 7}, "metadata"); got != "Round 7" {
		t.Fatalf("ClipFolderName() = %q", got)
	}
}

func TestNamingRejectsUnverifiedFolderID(t *testing.T) {
	if got := ResolvedFolderID(NamingInput{DriveFolderID: "workflow-id"}); got != "" {
		t.Fatalf("ResolvedFolderID() = %q", got)
	}
}
