package wiring

import (
	"database/sql"
	"testing"
)

// TestNewMediaRepoBundle_ExposesCanonicalReaderAndIdentity pins MEDIA-SSOT
// P1-7: the media bundle must offer the canonical PostgreSQL read surface and
// identity resolver directly, so consumers have one obvious place to look
// instead of reaching into the operational SQLite RepoBundle.
func TestNewMediaRepoBundle_ExposesCanonicalReaderAndIdentity(t *testing.T) {
	bundle := NewMediaRepoBundle(&sql.DB{}, nil)
	if bundle == nil {
		t.Fatal("NewMediaRepoBundle returned nil for a non-nil handle")
	}
	if bundle.Reader == nil {
		t.Error("MediaRepoBundle.Reader must be wired from the PostgreSQL handle")
	}
	if bundle.Identity == nil {
		t.Error("MediaRepoBundle.Identity must be wired from the PostgreSQL handle")
	}
	if bundle.DB == nil {
		t.Error("MediaRepoBundle.DB must expose the PostgreSQL handle")
	}
}

// TestNewMediaRepoBundle_DisabledIsNil pins the degraded mode: no PostgreSQL
// handle means no media bundle at all (never a half-wired one).
func TestNewMediaRepoBundle_DisabledIsNil(t *testing.T) {
	if got := NewMediaRepoBundle(nil, nil); got != nil {
		t.Fatalf("NewMediaRepoBundle(nil, nil) = %#v, want nil", got)
	}
}
