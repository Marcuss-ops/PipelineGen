package color

import "testing"

func TestIsHexRGBAcceptsExactlySixHexDigits(t *testing.T) {
	for _, value := range []string{"#000000", "#ffffff", "#A1b2C3", "#00ff7F"} {
		if !IsHexRGB(value) {
			t.Fatalf("IsHexRGB(%q) = false, want true", value)
		}
	}
	for _, value := range []string{
		"", "#", "#fff", "#fffff", "#fffffff", "ffffff", "#gggggg",
		"# ff ff", " #ffffff", "#ffffff ", "#FFFFFF\n",
	} {
		if IsHexRGB(value) {
			t.Fatalf("IsHexRGB(%q) = true, want false", value)
		}
	}
}

func TestParseHexRGBDecodesComponents(t *testing.T) {
	r, g, b, err := ParseHexRGB("#1a2B3c")
	if err != nil {
		t.Fatalf("ParseHexRGB: %v", err)
	}
	if r != 0x1a || g != 0x2b || b != 0x3c {
		t.Fatalf("ParseHexRGB = (%d,%d,%d), want (26,43,60)", r, g, b)
	}
}

func TestParseHexRGBRejectsInvalidValues(t *testing.T) {
	if _, _, _, err := ParseHexRGB("#12345"); err == nil {
		t.Fatal("ParseHexRGB accepted a short colour")
	}
	if _, _, _, err := ParseHexRGB("#12345z"); err == nil {
		t.Fatal("ParseHexRGB accepted a non-hex digit")
	}
}
