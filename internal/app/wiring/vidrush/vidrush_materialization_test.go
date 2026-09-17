package vidrush

import (
	"reflect"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

const testImagesRoot = "1kr8c1KZmUus10mkIdqJlYqAzXDyoNZeY"

// TestEntityImageLibraryDestinationPinsCanonicalSlugUnderTheImagesRoot pins the
// library layout an entity image must land in:
//
//	<ImagesRootFolder>/<canonical-entity-slug>/<file>
//
// i.e. the images root as the resolved folder and EXACTLY ONE subpath segment,
// the canonical entity slug — never <Title>/<Language>/images, and never the
// duplicated <slug>/<slug> nesting that produced
// Immagini/scottie-pippen/scottie-pippen/scottie-pippen.jpg.
func TestEntityImageLibraryDestinationPinsCanonicalSlugUnderTheImagesRoot(t *testing.T) {
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID:  "entity-image-7",
		Provider: scriptpkg.VidRushProviderInternetImages,
		Entity:   "Michael Jordan",
	}
	root, subpath, ok := entityImageLibraryDestination(candidate, testImagesRoot)
	if !ok {
		t.Fatal("entity image must resolve to a library destination")
	}
	if root != testImagesRoot {
		t.Fatalf("library root = %q, want %q", root, testImagesRoot)
	}
	if !reflect.DeepEqual(subpath, []string{"michael-jordan"}) {
		t.Fatalf("library subpath = %#v, want [michael-jordan]", subpath)
	}
	if len(subpath) != 1 {
		t.Fatalf("the per-image layout is ONE folder level below the root, got %d levels: %#v", len(subpath), subpath)
	}
}

// TestEntityImageLibraryDestinationIsIdentityNotRunScoped pins the reuse
// contract: two different runs requesting the same person resolve to the SAME
// folder, and the plan title/language cannot influence it (the function has no
// plan input by construction, so this test documents the invariant the call site
// must keep).
func TestEntityImageLibraryDestinationIsIdentityNotRunScoped(t *testing.T) {
	variants := []string{"Michael Jordan", "MICHAEL JORDAN", "  Michael   Jordan  ", "Michael Jordan's"}
	for _, variant := range variants {
		root, subpath, ok := entityImageLibraryDestination(scriptpkg.SegmentAssetCandidate{
			AssetID: "entity-image-8", Provider: scriptpkg.VidRushProviderImageGeneration, Entity: variant,
		}, testImagesRoot)
		if !ok || root != testImagesRoot || !reflect.DeepEqual(subpath, []string{"michael-jordan"}) {
			t.Fatalf("variant %q = (%q, %#v, %v), want (%q, [michael-jordan], true)", variant, root, subpath, ok, testImagesRoot)
		}
	}
}

// TestEntityImageLibraryDestinationDeclinesWhenUndeterminable proves the
// fail-soft half: a caller with no images root, a non-entity image, or an entity
// name that yields no canonical identity keeps the generic per-image destination
// instead of inventing a library folder.
func TestEntityImageLibraryDestinationDeclinesWhenUndeterminable(t *testing.T) {
	entityImage := scriptpkg.SegmentAssetCandidate{
		AssetID: "entity-image-9", Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Michael Jordan",
	}
	cases := []struct {
		name      string
		candidate scriptpkg.SegmentAssetCandidate
		root      string
	}{
		{name: "no images root", candidate: entityImage, root: "   "},
		{name: "entity-less image", candidate: scriptpkg.SegmentAssetCandidate{AssetID: "scene-image", Provider: scriptpkg.VidRushProviderInternetImages}, root: testImagesRoot},
		{name: "clip provider", candidate: scriptpkg.SegmentAssetCandidate{AssetID: "yt_clip", Provider: scriptpkg.VidRushProviderYouTube, Entity: "Michael Jordan"}, root: testImagesRoot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, subpath, ok := entityImageLibraryDestination(tc.candidate, tc.root)
			if ok || root != "" || subpath != nil {
				t.Fatalf("expected the generic destination to stay in charge, got (%q, %#v, %v)", root, subpath, ok)
			}
		})
	}
}

// TestSafeArtifactFilenameDoesNotDoubleTheExtension pins the file name an image
// receives inside its per-image folder. A web-image asset id commonly carries
// the source file name (or a URL ending in ".jpg"), and appending the extension
// unconditionally published `…jpg.jpg` into the library — visible in the real
// Drive tree as the folder `File_Scottie Pippen 5-2-22_jpg_jpg`.
func TestSafeArtifactFilenameDoesNotDoubleTheExtension(t *testing.T) {
	cases := []struct{ assetID, ext, want string }{
		// The doubled-extension regression, in both spellings.
		{"File_Scottie Pippen 5-2-22.jpg", ".jpg", "File_Scottie Pippen 5-2-22.jpg"},
		{"File_Scottie Pippen 5-2-22.JPG", ".jpg", "File_Scottie Pippen 5-2-22.JPG"},
		{"https://cdn.example/images/michael-jordan.jpg", ".jpg", "michael-jordan.jpg"},
		// A distinct extension must still be appended (the id is not the name).
		{"entity-image-9073", ".jpg", "entity-image-9073.jpg"},
		{"2c5be396a34aa655", ".png", "2c5be396a34aa655.png"},
		// A dot inside the stem is not an extension.
		{"Michael B. Jordan", ".jpg", "Michael B. Jordan.jpg"},
		// Degenerate ids keep a usable name instead of producing a bare ".jpg".
		{"", ".jpg", "vidrush-asset.jpg"},
		{".", ".jpg", "vidrush-asset.jpg"},
		// A trailing slash resolves to the last path segment, not to ".jpg".
		{"https://cdn.example/images/", ".jpg", "images.jpg"},
	}
	for _, tc := range cases {
		if got := safeArtifactFilename(tc.assetID, tc.ext); got != tc.want {
			t.Errorf("safeArtifactFilename(%q, %q) = %q, want %q", tc.assetID, tc.ext, got, tc.want)
		}
	}
}

// TestCanonicalImagesRootIsTheDefaultImagesRoot pins the operator-facing half of
// the contract: the library root the finalizer pins is the canonical
// DefaultImagesRootFolderID when the local config does not override it, so the
// per-image layout is the same on every node even with an empty/ignored
// config.yaml.
func TestCanonicalImagesRootIsTheDefaultImagesRoot(t *testing.T) {
	if got := (config.DriveConfig{}).ImagesFolder(); got != config.DefaultImagesRootFolderID {
		t.Fatalf("DriveConfig{}.ImagesFolder() = %q, want %q", got, config.DefaultImagesRootFolderID)
	}
	if root, _, ok := entityImageLibraryDestination(scriptpkg.SegmentAssetCandidate{
		AssetID: "entity-image-10", Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Scottie Pippen",
	}, (config.DriveConfig{}).ImagesFolder()); !ok || root != config.DefaultImagesRootFolderID {
		t.Fatalf("library root = %q (ok=%v), want %q", root, ok, config.DefaultImagesRootFolderID)
	}
}
