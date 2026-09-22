package config

import "testing"

// TestIsLoopbackBindHost pins the single definition the /metrics posture keys
// on (through RouterConfig.ServerLoopbackOnly) and that any other
// reachability-based posture must reuse.
//
// The two dangerous spellings are the empty host and "::": net.Listen reads
// both as "all interfaces", so calling them loopback would publish the metric
// surface to the network — the exact failure this helper exists to prevent.
func TestIsLoopbackBindHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"[::1]", true},
		{"localhost", true},
		{" 127.0.0.1 ", true},
		{"0.0.0.0", false},
		{"", false},
		{"::", false},
		{"77.93.152.122", false},
		{"192.168.1.5", false},
	}
	for _, tc := range cases {
		if got := IsLoopbackBindHost(tc.host); got != tc.want {
			t.Errorf("IsLoopbackBindHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
