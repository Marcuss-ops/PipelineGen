package gencore

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestValidateScriptMinImagesPerSourceBounds(t *testing.T) {
	cases := []struct {
		name    string
		value   int
		wantErr string
	}{
		{name: "disabled", value: 0},
		{name: "valid floor", value: 3},
		{name: "negative rejected", value: -1, wantErr: "min_images_per_source cannot be negative"},
		{name: "above maximum rejected", value: 21, wantErr: "min_images_per_source exceeds maximum of 20"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sp := scriptpkg.ScriptSpec{MinImagesPerSource: tc.value}
			diags := validateScript(sp, "ref")
			found := ""
			for _, d := range diags {
				if strings.Contains(d, "min_images_per_source") {
					found = d
				}
			}
			if tc.wantErr == "" {
				if found != "" {
					t.Fatalf("unexpected diagnostic: %s", found)
				}
				return
			}
			if !strings.Contains(found, tc.wantErr) {
				t.Fatalf("diagnostics %v, want %q", diags, tc.wantErr)
			}
		})
	}
}
