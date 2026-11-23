package config

import "testing"

func TestNormalizeClipRenderDeliveryMode(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: ClipRenderDeliveryDrive},
		{in: "drive", want: ClipRenderDeliveryDrive},
		{in: " Object_Store ", want: ClipRenderDeliveryObjectStore},
		{in: "BOTH", want: ClipRenderDeliveryBoth},
		{in: "s3", wantErr: true},
	}
	for _, tc := range cases {
		got, err := NormalizeClipRenderDeliveryMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeClipRenderDeliveryMode(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("NormalizeClipRenderDeliveryMode(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}
