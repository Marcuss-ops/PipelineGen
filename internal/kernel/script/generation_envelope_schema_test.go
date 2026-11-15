package script

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeEnvelopeV2ReportsSchemaFieldPaths(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "version type", raw: `{"version":"2","items":[]}`, want: "version: expected integer 2"},
		{name: "correlation type", raw: `{"version":2,"correlation_id":7,"items":[]}`, want: "correlation_id: expected string"},
		{name: "unknown top-level", raw: `{"version":2,"correlation":7,"items":[]}`, want: "unknown field (accepted schema:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeEnvelopeV2([]byte(tc.raw))
			var invalid *PlanInvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("error = %v, want PlanInvalidError", err)
			}
			if !strings.Contains(strings.Join(invalid.Details, " "), tc.want) {
				t.Fatalf("details = %v, want substring %q", invalid.Details, tc.want)
			}
		})
	}
}
