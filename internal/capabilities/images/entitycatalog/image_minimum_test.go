package entitycatalog

import "testing"

func TestCatalogImageMinimumIsShared(t *testing.T) {
	if !ImageMeetsCatalogMinimum(800, 500) {
		t.Fatal("800x500 should meet the catalog minimum")
	}
	if ImageMeetsCatalogMinimum(799, 700) {
		t.Fatal("a maximum side below 800 must fail")
	}
	if ImageMeetsCatalogMinimum(800, 499) {
		t.Fatal("a pixel count below 400000 must fail")
	}
}
