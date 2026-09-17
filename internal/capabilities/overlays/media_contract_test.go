package overlays

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestOverlayRender_ValidatesMediaContract(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err != nil {
		t.Errorf("valid probe should pass: %v", err)
	}
}

func TestOverlayRender_RejectsInvalidMedia(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1280, // wrong width
		Height:       720,  // wrong height
		DurationUS:   5000000,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("wrong resolution should fail validation")
	}
}

func TestOverlayRender_HasZeroAudioStreams(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 1, // overlay must have 0 audio streams
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("audio streams != 0 should fail validation")
	}
}

func TestOverlayRender_RejectsWrongCodec(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "h264", // wrong codec
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("wrong codec should fail validation")
	}
}

func TestOverlayRender_RejectsWrongPixelFormat(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuv420p", // wrong pixel format
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("wrong pixel format should fail validation")
	}
}

func TestOverlayRender_RejectsAlphaRequiredButMissing(t *testing.T) {
	contract := OverlayMediaContract{
		ID:            "test-alpha",
		Version:       1,
		RequiresAlpha: true,
		Width:         1920,
		Height:        1080,
		FPSNum:        24,
		FPSDen:        1,
		AudioStreams:  0,
		PixelFormat:   "yuv420p", // no alpha
	}
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuv420p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("alpha required but missing should fail validation")
	}
}

func TestOverlayRender_AcceptsAlphaInPixelFormat(t *testing.T) {
	contract := OverlayMediaContract{
		ID:            "test-alpha",
		Version:       1,
		RequiresAlpha: true,
		Width:         1920,
		Height:        1080,
		FPSNum:        24,
		FPSDen:        1,
		AudioStreams:  0,
		PixelFormat:   "yuva444p",
	}
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err != nil {
		t.Errorf("valid alpha contract should pass: %v", err)
	}
}

func TestOverlayRender_RejectsZeroDuration(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   0, // zero duration
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("zero duration should fail validation")
	}
}

func TestOverlayRender_RejectsZeroFileSize(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    0, // 262-byte MP4 class of bug
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("zero file size should fail validation")
	}
}

func TestOverlayContractForCanvas_Alpha(t *testing.T) {
	c := OverlayContractForCanvas(1280, 720, 24, 1, true)
	if c.Width != 1280 || c.Height != 720 {
		t.Errorf("canvas = %dx%d, want 1280x720", c.Width, c.Height)
	}
	if c.FPSNum != 24 || c.FPSDen != 1 {
		t.Errorf("fps = %d/%d, want 24/1", c.FPSNum, c.FPSDen)
	}
	if !c.RequiresAlpha {
		t.Error("requires_alpha should be true")
	}
	if c.AudioStreams != 0 {
		t.Errorf("audio_streams = %d, want 0", c.AudioStreams)
	}
}

func TestOverlayContractForCanvas_NoAlpha(t *testing.T) {
	c := OverlayContractForCanvas(1920, 1080, 24, 1, false)
	if c.RequiresAlpha {
		t.Error("requires_alpha should be false")
	}
	if c.Codec != "h264" {
		t.Errorf("codec = %q, want h264", c.Codec)
	}
}

func TestResolveMediaContract_KnownIDs(t *testing.T) {
	for _, id := range []string{"", "overlay-v1", DefaultOverlayContractV1.ID} {
		c, err := ResolveMediaContract(id)
		if err != nil {
			t.Fatalf("ResolveMediaContract(%q): %v", id, err)
		}
		if c.ID != DefaultOverlayContractV1.ID {
			t.Fatalf("ResolveMediaContract(%q).ID = %q, want %q", id, c.ID, DefaultOverlayContractV1.ID)
		}
	}
	c, err := ResolveMediaContract(DefaultOverlayContractNoAlpha.ID)
	if err != nil {
		t.Fatalf("ResolveMediaContract(no-alpha): %v", err)
	}
	if c.Codec != "h264" {
		t.Fatalf("no-alpha codec = %q, want h264", c.Codec)
	}
}

func TestResolveMediaContract_UnknownFailsClosed(t *testing.T) {
	if _, err := ResolveMediaContract("overlay-v99"); err == nil {
		t.Fatal("unknown contract id must fail closed")
	}
}

func TestContractIDForCanvas(t *testing.T) {
	if id := ContractIDForCanvas(1280, 720, 24, 1, true); id != DefaultOverlayContractV1.ID {
		t.Fatalf("alpha contract id = %q, want %q", id, DefaultOverlayContractV1.ID)
	}
	if id := ContractIDForCanvas(1280, 720, 24, 1, false); id != DefaultOverlayContractNoAlpha.ID {
		t.Fatalf("no-alpha contract id = %q, want %q", id, DefaultOverlayContractNoAlpha.ID)
	}
}

func TestOverlayMediaContract_FPSComparisonRational(t *testing.T) {
	contract := OverlayMediaContract{
		ID:      "test-fps",
		Version: 1,
		Width:   1920, Height: 1080,
		FPSNum: 24000, FPSDen: 1001, // 23.976
	}
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       24000,
		FPSDen:       1001,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err != nil {
		t.Errorf("23.976 fps rational comparison should pass: %v", err)
	}
}

func TestOverlayMediaContract_ContainerCommaTokenAware(t *testing.T) {
	contract := DefaultOverlayContractV1
	// ffprobe's format_name is a comma-joined container family list; the
	// first token is the canonical container identity.
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov,mp4,m4a,3gp,3g2,mj2",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err != nil {
		t.Errorf("comma-token container should match: %v", err)
	}
}

func TestOverlayMediaContract_RejectsZeroFPS(t *testing.T) {
	contract := DefaultOverlayContractV1
	probed := OverlayProbeResult{
		Width:        1920,
		Height:       1080,
		DurationUS:   5000000,
		FPSNum:       0, // no usable frame rate reported
		FPSDen:       0,
		AudioStreams: 0,
		Codec:        "prores",
		PixelFormat:  "yuva444p",
		Container:    "mov",
		SizeBytes:    1024000,
	}
	if err := contract.Validate(probed); err == nil {
		t.Error("a 0/0 fps probe must fail closed (not trivially cross-multiply to zero)")
	}
}

// ─── Phase 2: the canonical media identity (kernel/asset.Ref) ────────────
//
// OverlayAssetRef is the semantic layer's PROJECTION of kernel/asset.Ref. These
// tests pin the property that motivated the convergence: the identity of an
// asset is independent of where its bytes currently are, so two plan items that
// describe the same image through different locations are the SAME asset — and
// the queue must therefore stage one payload, not two.

// TestOverlayAssetRefProjectsOntoCanonicalIdentity pins the projection's two
// obligations: it carries the identity half (asset_id + content address) and it
// is unable to carry the location half.
func TestOverlayAssetRefProjectsOntoCanonicalIdentity(t *testing.T) {
	ref := OverlayAssetRef{
		AssetID:   "person:michael-jordan",
		URL:       "https://cdn.example/jordan.jpg",
		LocalPath: "/tmp/producer-only/jordan.jpg",
		SHA256:    "C4813C9D7D4F0F6B1A2C3D4E5F60718293A4B5C6D7E8F9012345678901ABCDEF",
		MediaType: "image/jpeg",
	}

	identity := ref.Ref()
	if identity.AssetID != ref.AssetID {
		t.Errorf("projected asset id = %q, want %q", identity.AssetID, ref.AssetID)
	}
	if want := strings.ToLower(ref.SHA256); identity.SHA256 != want {
		t.Errorf("projected content address = %q, want canonical %q", identity.SHA256, want)
	}
	if identity.MediaType != ref.MediaType {
		t.Errorf("projected media type = %q, want %q", identity.MediaType, ref.MediaType)
	}
	if err := identity.Validate(); err != nil {
		t.Errorf("a fully-populated overlay ref must project onto a valid identity: %v", err)
	}

	// The identity must be unable to describe a location: the tagged projection
	// (what a consumer reads) must not contain the fields the ref holds.
	raw, err := json.Marshal(identity)
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	for _, forbidden := range []string{"local_path", "url", "drive_link", "drive_file_id", "download_link", "legacy_file_md5"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Errorf("canonical identity carries location key %q: %s", forbidden, raw)
		}
	}
}

// TestOverlayAssetRefIdentityIsIndependentOfLocation is the regression guard for
// the production failure this whole programme exists to remove: two records that
// describe one asset through different locations must not become two assets. If
// Ref() ever starts folding a location into the identity, this fails.
func TestOverlayAssetRefIdentityIsIndependentOfLocation(t *testing.T) {
	digest := "c4813c9d7d4f0f6b1a2c3d4e5f60718293a4b5c6d7e8f9012345678901abcdef"
	viaDrive := OverlayAssetRef{
		AssetID:   "person:michael-jordan",
		SHA256:    digest,
		MediaType: "image/jpeg",
		URL:       "https://drive.google.com/file/d/drive-file-id/view",
		LocalPath: "/mnt/drive/jordan.jpg",
	}
	viaCdn := OverlayAssetRef{
		AssetID:   "person:michael-jordan",
		SHA256:    digest,
		MediaType: "image/jpeg",
		URL:       "https://cdn.example/jordan.jpg",
	}

	if !viaDrive.Ref().Equal(viaCdn.Ref()) {
		t.Fatalf("one asset with two locations projected onto two identities:\n  %v\n  %v", viaDrive.Ref(), viaCdn.Ref())
	}
	if viaDrive.Ref().DedupKey() != viaCdn.Ref().DedupKey() {
		t.Errorf("dedup keys disagree for one asset: %q vs %q", viaDrive.Ref().DedupKey(), viaCdn.Ref().DedupKey())
	}
	// And the digest case must not create a third asset either.
	shouted := viaCdn
	shouted.SHA256 = strings.ToUpper(digest)
	if !shouted.Ref().Equal(viaCdn.Ref()) {
		t.Errorf("one digest in two cases projected onto two identities: %v vs %v", shouted.Ref(), viaCdn.Ref())
	}
}

// TestNewOverlayAssetRefTakesLocationsAsExplicitArguments pins the builder's
// shape: the canonical type cannot carry a location, so every location a caller
// wants on the projection has to be passed explicitly and visibly here. A future
// caller cannot smuggle one in through the identity.
func TestNewOverlayAssetRefTakesLocationsAsExplicitArguments(t *testing.T) {
	identity := asset.New("person:michael-jordan", "C4813C9D7D4F0F6B1A2C3D4E5F60718293A4B5C6D7E8F9012345678901ABCDEF", "image/jpeg", 0)
	built := NewOverlayAssetRef(identity, "https://cdn.example/jordan.jpg", "/tmp/jordan.jpg")

	if built.SHA256 != "c4813c9d7d4f0f6b1a2c3d4e5f60718293a4b5c6d7e8f9012345678901abcdef" {
		t.Errorf("builder did not canonicalise the digest: %q", built.SHA256)
	}
	if built.AssetID != identity.AssetID || built.MediaType != identity.MediaType {
		t.Errorf("builder dropped part of the identity: %+v", built)
	}
	if built.URL != "https://cdn.example/jordan.jpg" || built.LocalPath != "/tmp/jordan.jpg" {
		t.Errorf("builder dropped an explicitly-passed location: %+v", built)
	}
	// build→project must return what went in, so a builder used at N call sites
	// cannot become a second, silently-different spelling of identity.
	if back := built.Ref(); back != identity.Canonical() {
		t.Errorf("build→project is lossy: got %+v, want %+v", back, identity.Canonical())
	}
}
