package config

import "testing"

func TestLoopbackBaseURLCarriesTheCanonicalHostAndPort(t *testing.T) {
	if got, want := LoopbackBaseURL(8001), "http://127.0.0.1:8001"; got != want {
		t.Fatalf("LoopbackBaseURL(8001) = %q, want %q", got, want)
	}
	if got, want := LoopbackURLPrefix+"9000", LoopbackBaseURL(9000); got != want {
		t.Fatalf("prefix + port = %q, want %q", got, want)
	}
}

func TestIsLoopbackHostAcceptsEveryLocalSpelling(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{" 127.0.0.1:8080 ", true},
		{"localhost", true},
		{"LOCALHOST:11434", true},
		{"api.localhost", true},
		{"::1", true},
		{"[::1]:8000", true},
		{"", false},
		{"::", false},
		{"0.0.0.0", false},
		{"example.com", false},
		{"localhost.example.com", false},
	}
	for _, tc := range cases {
		if got := IsLoopbackHost(tc.host); got != tc.want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
