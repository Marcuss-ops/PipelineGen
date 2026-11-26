package model

import "testing"

// SupportsTransparency is the single owner of "may this plan be transparent?".
// r80 forced transparency on a noalpha plan and the opaque windows turned into
// black artifacts; a caller must ask this function, not infer alpha from a
// style value.
func TestSupportsTransparencyFollowsTheContract(t *testing.T) {
	cases := []struct {
		contractID string
		want       bool
	}{
		{"", true}, // renderer default is the alpha-bearing overlay-v1
		{DefaultOverlayContractV1.ID, true},
		{DefaultOverlayContractNoAlpha.ID, false},
	}
	for _, testCase := range cases {
		got, err := SupportsTransparency(testCase.contractID)
		if err != nil {
			t.Fatalf("SupportsTransparency(%q): %v", testCase.contractID, err)
		}
		if got != testCase.want {
			t.Fatalf("SupportsTransparency(%q) = %v, want %v", testCase.contractID, got, testCase.want)
		}
	}
	if _, err := SupportsTransparency("overlay-v9-unknown"); err == nil {
		t.Fatal("an unknown contract must fail closed")
	}
}
