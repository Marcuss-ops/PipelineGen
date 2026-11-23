package cliprender

import "testing"

// TestFingerprint_ForegroundScaleZeroEqualsHundred is the CR-01 golden: 0 is
// the wire spelling of the contract default 100, so both must address the same
// rendered bytes and the same cache entry.
func TestFingerprint_ForegroundScaleZeroEqualsHundred(t *testing.T) {
	zero := &RenderRequest{SourceAssetID: "asset-scale", Output: &OutputSpec{ForegroundScalePercent: 0}}
	hundred := &RenderRequest{SourceAssetID: "asset-scale", Output: &OutputSpec{ForegroundScalePercent: 100}}
	zero.Normalize()
	hundred.Normalize()

	if zero.Output.ForegroundScalePercent != 100 {
		t.Fatalf("Normalize must promote 0 to 100, got %d", zero.Output.ForegroundScalePercent)
	}
	fpZero, err := zero.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint zero: %v", err)
	}
	fpHundred, err := hundred.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint hundred: %v", err)
	}
	if fpZero != fpHundred {
		t.Fatalf("0 and 100 must have the same fingerprint: %q vs %q", fpZero, fpHundred)
	}
}

// TestFingerprint_DisabledBlocksDoNotChangeIdentity covers the inert blocks:
// a disabled watermark/subtitles block and a mode=none background are absent
// from the visual contract, so their leftover fields cannot split the cache.
func TestFingerprint_DisabledBlocksDoNotChangeIdentity(t *testing.T) {
	base := &RenderRequest{
		SourceAssetID: "asset-disabled",
		Watermark:     &WatermarkSpec{Enabled: false, Position: PositionTopLeft, MarginPX: 7, Opacity: 0.2, Text: "unused"},
		Subtitles:     &SubtitlesSpec{Enabled: false, Mode: SubtitlesModeSidecar, StyleID: "unused-a"},
		Background:    &BackgroundSpec{Mode: BackgroundModeNone, AssetID: "ignored-a"},
	}
	other := &RenderRequest{
		SourceAssetID: "asset-disabled",
		Watermark:     &WatermarkSpec{Enabled: false, Position: PositionBottomRight, MarginPX: 99, Opacity: 1, Text: "also-unused"},
		Subtitles:     &SubtitlesSpec{Enabled: false, Mode: SubtitlesModeBurn, StyleID: "unused-b"},
		Background:    &BackgroundSpec{Mode: BackgroundModeNone, AssetID: "ignored-b"},
	}
	base.Normalize()
	other.Normalize()
	if err := base.Validate(); err != nil {
		t.Fatalf("base must validate: %v", err)
	}
	if err := other.Validate(); err != nil {
		t.Fatalf("other must validate: %v", err)
	}
	fpBase, err := base.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint base: %v", err)
	}
	fpOther, err := other.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint other: %v", err)
	}
	if fpBase != fpOther {
		t.Fatalf("disabled blocks must not change identity: %q vs %q", fpBase, fpOther)
	}
}

// TestFingerprint_EnabledBlockChangesIdentity is the complement: once a block
// is enabled its visual fields are part of the identity.
func TestFingerprint_EnabledBlockChangesIdentity(t *testing.T) {
	a := &RenderRequest{
		SourceAssetID: "asset-enabled",
		Watermark:     &WatermarkSpec{Enabled: true, Text: "LOGO", Position: PositionTopRight, MarginPX: 40},
	}
	b := &RenderRequest{
		SourceAssetID: "asset-enabled",
		Watermark:     &WatermarkSpec{Enabled: true, Text: "LOGO", Position: PositionTopRight, MarginPX: 80},
	}
	a.Normalize()
	b.Normalize()
	fpA, err := a.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint A: %v", err)
	}
	fpB, err := b.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint B: %v", err)
	}
	if fpA == fpB {
		t.Fatalf("an enabled watermark margin must change identity: %q", fpA)
	}
}
