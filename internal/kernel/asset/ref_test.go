package asset

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const fullDigest = "c4813c9d7d4f0f6b1a2c3d4e5f60718293a4b5c6d7e8f9012345678901abcdef"

// TestRefExposesIdentityFieldsOnly is the local, unit-speed guard for the one
// rule this type exists to enforce: an identity may not carry a location.
//
// cmd/archcheck's media identity gate enforces the same invariant across the
// whole repository; this pins the type the gate wants everything to converge
// on, so a future DriveLink/LocalPath added "just for convenience" fails in
// milliseconds instead of in a full archcheck run — and fails at the exact
// place whose doc comment promises it can never happen.
func TestRefExposesIdentityFieldsOnly(t *testing.T) {
	want := []string{"AssetID", "MediaType", "SHA256", "SizeBytes"}

	typ := reflect.TypeOf(Ref{})
	var got []string
	for i := 0; i < typ.NumField(); i++ {
		got = append(got, typ.Field(i).Name)
	}
	sort.Strings(got)
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Ref fields = %v, want %v; a location field (LocalPath/DriveLink/DriveFileID/DownloadLink) or a compatibility digest (LegacyFileMD5) must not live on the identity", got, want)
	}
}

// TestRefJSONContract freezes the wire keys. The identity is published to
// RenderingGen and recorded in the media SSOT, so a rename is a breaking
// change to both.
func TestRefJSONContract(t *testing.T) {
	raw, err := json.Marshal(Ref{AssetID: "a", SHA256: fullDigest, MediaType: "image/jpeg", SizeBytes: 42})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"asset_id", "sha256", "media_type", "size_bytes"} {
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("Ref JSON is missing %q: %s", key, raw)
		}
	}
	// The wire form must never be able to describe a location, because there is
	// no field to describe it with.
	for _, forbidden := range []string{"local_path", "drive_link", "drive_file_id", "download_link", "legacy_file_md5"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Errorf("Ref JSON carries a location/compatibility key %q: %s", forbidden, raw)
		}
	}
}

func TestRefValidate(t *testing.T) {
	cases := []struct {
		name string
		ref  Ref
		want error
	}{
		{"complete", Ref{AssetID: "person:michael-jordan", SHA256: fullDigest}, nil},
		{"no asset id", Ref{SHA256: fullDigest}, ErrRefAssetIDRequired},
		{"no content address", Ref{AssetID: "person:michael-jordan"}, ErrRefContentHashRequired},
		{"whitespace asset id", Ref{AssetID: "   ", SHA256: fullDigest}, ErrRefAssetIDRequired},
		{"whitespace digest", Ref{AssetID: "a", SHA256: " \t "}, ErrRefContentHashRequired},
		{"zero value", Ref{}, ErrRefAssetIDRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ref.Validate()
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestRefValidateAcceptsSyntheticDigests: fixtures and golden plans carry short
// markers, and refusing them would refuse plans the pipeline is supposed to
// render. The STRONG claim is opt-in via IsCanonicalDigest.
func TestRefValidateAcceptsSyntheticDigests(t *testing.T) {
	ref := Ref{AssetID: "background", SHA256: "ABC"}
	if err := ref.Validate(); err != nil {
		t.Fatalf("a synthetic digest must still be a usable identity: %v", err)
	}
	if ref.IsCanonicalDigest() {
		t.Error("a 3-character marker must not be reported as a canonical sha256")
	}
}

func TestRefCanonical(t *testing.T) {
	ref := Ref{AssetID: "  person:michael-jordan ", SHA256: "  " + strings.ToUpper(fullDigest) + " ", MediaType: " image/jpeg "}
	got := ref.Canonical()

	if got.AssetID != "person:michael-jordan" {
		t.Errorf("AssetID = %q", got.AssetID)
	}
	if got.SHA256 != fullDigest {
		t.Errorf("SHA256 = %q, want the lower-cased digest", got.SHA256)
	}
	if got.MediaType != "image/jpeg" {
		t.Errorf("MediaType = %q", got.MediaType)
	}
	if !got.IsCanonicalDigest() {
		t.Error("the canonical form of a full digest must pass IsCanonicalDigest")
	}
	if got.ContentHash() != fullDigest {
		t.Errorf("ContentHash() = %q", got.ContentHash())
	}
}

// TestRefEqualIsCaseInsensitive pins the reason Canonical exists at the
// identity boundary: two spellings of one digest are one asset, not two.
func TestRefEqualIsCaseInsensitive(t *testing.T) {
	lower := Ref{AssetID: "a", SHA256: fullDigest}
	upper := Ref{AssetID: "a", SHA256: strings.ToUpper(fullDigest)}

	if !lower.Equal(upper) {
		t.Error("a differently-cased digest must be the same asset")
	}
	if lower.DedupKey() != upper.DedupKey() {
		t.Errorf("dedup keys differ: %q vs %q", lower.DedupKey(), upper.DedupKey())
	}
	if lower.Equal(Ref{AssetID: "a", SHA256: "other"}) {
		t.Error("different bytes must not be equal")
	}
}

// TestRefDedupKey: the address wins when present, so one payload is staged
// once; the logical id is the fallback for a ref that names no bytes yet.
func TestRefDedupKey(t *testing.T) {
	withBytes := Ref{AssetID: "logo", SHA256: "AbC"}
	if got := withBytes.DedupKey(); got != "abc" {
		t.Errorf("DedupKey() = %q, want the canonical digest", got)
	}
	identityOnly := Ref{AssetID: " logo "}
	if got := identityOnly.DedupKey(); got != "logo" {
		t.Errorf("DedupKey() = %q, want the trimmed asset id", got)
	}
}

func TestRefIsZeroAndHasContentAddress(t *testing.T) {
	if !(Ref{}).IsZero() {
		t.Error("the zero value must report IsZero")
	}
	if (Ref{AssetID: "a"}).IsZero() {
		t.Error("a ref with an asset id is not zero")
	}
	if !(Ref{SHA256: "x"}).HasContentAddress() {
		t.Error("a ref with a digest names bytes")
	}
	if (Ref{AssetID: "a"}).HasContentAddress() {
		t.Error("a ref with only an asset id names no bytes")
	}
}

// TestRefStringNeverCarriesAPath keeps logs safe by construction: the type has
// no location field, so the rendering cannot leak one.
func TestRefStringNeverCarriesAPath(t *testing.T) {
	if got := (Ref{}).String(); strings.Contains(got, "/") {
		t.Errorf("String() = %q", got)
	}
	ref := Ref{AssetID: "person:michael-jordan", SHA256: fullDigest, MediaType: "image/jpeg"}
	got := ref.String()
	if !strings.Contains(got, "person:michael-jordan") || !strings.Contains(got, fullDigest[:12]) {
		t.Errorf("String() = %q", got)
	}
	if strings.Contains(got, fullDigest) {
		t.Errorf("String() must abbreviate the digest, got %q", got)
	}
}
