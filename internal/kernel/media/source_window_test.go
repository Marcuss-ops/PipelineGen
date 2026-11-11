package media

import (
	"strings"
	"testing"
)

func TestSourceWindowValidate(t *testing.T) {
	valid := SourceWindow{AssetSHA256: strings.Repeat("a", 64), SizeBytes: 10, StartUS: 1, EndUS: 2}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid source window rejected: %v", err)
	}
	for name, window := range map[string]SourceWindow{
		"noncanonical digest": {AssetSHA256: strings.Repeat("A", 64), SizeBytes: 10, StartUS: 0, EndUS: 1},
		"empty asset":         {AssetSHA256: strings.Repeat("a", 64), StartUS: 0, EndUS: 1},
		"negative start":      {AssetSHA256: strings.Repeat("a", 64), SizeBytes: 10, StartUS: -1, EndUS: 1},
		"empty interval":      {AssetSHA256: strings.Repeat("a", 64), SizeBytes: 10, StartUS: 1, EndUS: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := window.Validate(); err == nil {
				t.Fatal("invalid source window accepted")
			}
		})
	}
}
