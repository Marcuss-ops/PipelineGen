package renderinggen

import (
	"path/filepath"
	"testing"

	assetmaterializer "github.com/Marcuss-ops/PipelineGen/internal/platform/assets/materializer"
)

// TestPrefetchGateAgreesWithTheCanonicalMaterializer pins the unification.
//
// Before this change the prefetch bridge owned its own answer to "do these
// bytes hash to this address?" while the overlay cache owned another, and the
// two drifted: one trusted a cached file because it existed, the other had no
// verification at all. The fix was not to make both correct, it was to make
// only one of them exist.
//
// The test compares the bridge's gate against an independently constructed
// canonical materializer over a matrix of inputs. If someone re-introduces a
// bespoke digest implementation inside the bridge, the two verdicts diverge and
// this fails — which is the entire point: the property is "there is one owner",
// not "the owner is right today".
func TestPrefetchGateAgreesWithTheCanonicalMaterializer(t *testing.T) {
	payload := []byte("the bytes a content address names")
	address := hashOf(payload)

	dir := t.TempDir()
	matching := writeTempFile(t, dir, "matching.bin", payload)
	mismatching := writeTempFile(t, dir, "mismatching.bin", []byte("other bytes"))
	empty := writeTempFile(t, dir, "empty.bin", nil)
	missing := filepath.Join(dir, "never-written.bin")

	upper := ""
	for _, r := range address {
		if r >= 'a' && r <= 'f' {
			upper += string(r - 'a' + 'A')
			continue
		}
		upper += string(r)
	}

	canonical := assetmaterializer.New(assetmaterializer.Options{})

	cases := []struct {
		name string
		path string
		hash string
	}{
		{"matching bytes", matching, address},
		{"matching bytes, upper-case address", matching, upper},
		{"mismatching bytes", mismatching, address},
		{"empty file", empty, address},
		{"empty file under the empty-payload address", empty, hashOf(nil)},
		{"missing file", missing, address},
		{"directory instead of file", dir, address},
		{"empty hint", "", address},
		{"empty address", matching, ""},
		{"whitespace around a valid hint", "  " + matching + "  ", "  " + address + "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := verifiedLocalPath(tc.path, tc.hash) != ""
			want := canonical.Matches(tc.path, tc.hash)
			if gate != want {
				t.Fatalf("prefetch gate verdict = %v, canonical materializer verdict = %v for (%q, %q); "+
					"the repository must have exactly one answer to \"do these bytes hash to this address?\"",
					gate, want, tc.path, tc.hash)
			}
			// And the package-level authority must be the same authority: a
			// fresh instance and the process instance cannot disagree.
			if got := prefetchAssets.Matches(tc.path, tc.hash); got != want {
				t.Fatalf("prefetchAssets.Matches = %v, fresh canonical instance = %v for (%q, %q)",
					got, want, tc.path, tc.hash)
			}
		})
	}
}
