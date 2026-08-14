package catalogsync

import (
	"strings"
	"testing"
)

// TestRemoteFileFingerprint_NeverFabricatesContentHash locks the mediaregistry
// hash contract at the Drive-only discovery seam: a remote Drive file has no
// materialized bytes, so the content fingerprint must be the "unknown" state
// (empty) and MUST NEVER be derived from the Drive file ID, name, mime type,
// size or MD5 checksum. Source metadata is never byte identity.
func TestRemoteFileFingerprint_NeverFabricatesContentHash(t *testing.T) {
	file := RemoteFile{
		ID:          "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
		Name:        "interview.mp4",
		MimeType:    "video/mp4",
		Size:        123456789,
		MD5Checksum: "d41d8cd98f00b204e9800998ecf8427e",
	}

	got := remoteFileFingerprint(file)
	if got != "" {
		t.Fatalf("remoteFileFingerprint = %q, want empty (unknown) — must not fabricate a content hash", got)
	}

	// The previous implementation emitted drive-md5: / drive-meta-sha256:
	// prefixes built from the Drive ID/metadata. Assert none of those inputs
	// leak into the fingerprint.
	for _, forbidden := range []string{file.ID, file.Name, file.MD5Checksum, "drive-md5:", "drive-meta-sha256:"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("remoteFileFingerprint %q must not contain %q (fabricated from source metadata)", got, forbidden)
		}
	}
}

// TestRemoteFileFingerprint_UnknownWhenNoMD5 covers the MD5-absent branch:
// even without a Drive checksum the fingerprint stays unknown rather than
// falling back to a metadata-derived digest.
func TestRemoteFileFingerprint_UnknownWhenNoMD5(t *testing.T) {
	file := RemoteFile{
		ID:       "folder-file-42",
		Name:     "clip.mp4",
		MimeType: "video/mp4",
		Size:     42,
	}
	if got := remoteFileFingerprint(file); got != "" {
		t.Fatalf("remoteFileFingerprint without MD5 = %q, want empty (unknown)", got)
	}
}
