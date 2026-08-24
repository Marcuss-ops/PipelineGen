package security

import (
	"path/filepath"
	"testing"
)

func TestValidateDownloadURL_Empty(t *testing.T) {
	if err := ValidateDownloadURL(""); err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestValidateDownloadURL_ForbiddenChars(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"semicolon", "https://example.com;a"},
		{"pipe", "https://example.com|a"},
		{"ampersand", "https://example.com&a"},
		{"dollar", "https://example.com$a"},
		{"backtick", "https://example.com`a"},
		{"backslash", "https://example.com\\a"},
		{"single_quote", "https://example.com'a"},
		{"double_quote", `https://example.com"a`},
		{"exclamation", "https://example.com!a"},
		{"angle_bracket", "https://example.com<a>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateDownloadURL(tt.url); err == nil {
				t.Errorf("expected error for URL with %s", tt.name)
			}
		})
	}
}

func TestValidateDownloadURL_FlagInjection(t *testing.T) {
	if err := ValidateDownloadURL("--help"); err == nil {
		t.Error("expected error for flag injection")
	}
	if err := ValidateDownloadURL("-o output.txt"); err == nil {
		t.Error("expected error for flag injection")
	}
}

func TestValidateDownloadURL_Scheme(t *testing.T) {
	tmp := filepath.Join("/tmp", "validate-download-url.mp4")
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"ftp", "ftp://example.com/video.mp4", true},
		{"file", "file:///etc/passwd", false},
		{"file_relative", "file://relative/path.mp4", false},
		{"javascript", "javascript:alert(1)", true},
		{"data", "data:text/plain;base64,x", true},
		{"file_absolute_path", "file://" + tmp, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDownloadURL(tt.url)
			if tt.want && err == nil {
				t.Errorf("expected error for %s scheme", tt.name)
			}
			if !tt.want && err != nil {
				t.Errorf("unexpected error for %s scheme: %v", tt.name, err)
			}
		})
	}
}

func TestValidateDownloadURL_Host(t *testing.T) {
	SetAllowedHosts([]string{"youtube.com", "www.example.com"})

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"allowed", "https://www.youtube.com/watch?v=test", false},
		{"allowed_subdomain", "https://sub.youtube.com/watch?v=test", false},
		{"blocked", "https://evil.com/video.mp4", true},
		{"no_host", "https:///path", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDownloadURL(tt.url)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for %s: %v", tt.name, err)
			}
		})
	}
}

func TestValidateDownloadURL_Userinfo(t *testing.T) {
	SetAllowedHosts([]string{"youtube.com"})
	if err := ValidateDownloadURL("https://evil@youtube.com/watch"); err == nil {
		t.Error("expected error for URL with userinfo")
	}
}

func TestValidateDownloadURL_Fragment(t *testing.T) {
	SetAllowedHosts([]string{"example.com"})
	if err := ValidateDownloadURL("https://example.com/video#fragment"); err == nil {
		t.Error("expected error for URL with fragment")
	}
}

func TestValidateDownloadURL_Valid(t *testing.T) {
	SetAllowedHosts([]string{"example.com", "artlist.io", "youtube.com"})
	tests := []struct {
		name string
		url  string
	}{
		{"simple", "https://example.com/video.mp4"},
		{"with_path", "https://example.com/path/to/video.mp4"},
		{"with_query", "https://example.com/video?id=123"},
		{"subdomain", "https://cdn.example.com/video.mp4"},
		{"artlist", "https://artlist.io/stock-footage/clip/123"},
		{"www_youtube", "https://www.youtube.com/watch?v=abc123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateDownloadURL(tt.url); err != nil {
				t.Errorf("unexpected error for %s: %v", tt.name, err)
			}
		})
	}
}

func TestIsAllowedHost(t *testing.T) {
	SetAllowedHosts([]string{"youtube.com", "example.com"})

	tests := []struct {
		host  string
		allow bool
	}{
		{"youtube.com", true},
		{"www.youtube.com", true},
		{"sub.youtube.com", true},
		{"example.com", true},
		{"evil.com", false},
		{"notyoutube.co", false},
		{"youtube.co.evil", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := isAllowedHost(tt.host)
			if got != tt.allow {
				t.Errorf("isAllowedHost(%q) = %v, want %v", tt.host, got, tt.allow)
			}
		})
	}
}

func TestAddAllowedHost(t *testing.T) {
	SetAllowedHosts(nil)
	AddAllowedHost("test.com")
	if !isAllowedHost("test.com") {
		t.Error("expected test.com to be allowed after AddAllowedHost")
	}
}

func TestSetAllowedHosts(t *testing.T) {
	SetAllowedHosts([]string{"host1.com", "host2.com"})
	if !isAllowedHost("host1.com") {
		t.Error("expected host1.com to be allowed")
	}
	if !isAllowedHost("host2.com") {
		t.Error("expected host2.com to be allowed")
	}
	if isAllowedHost("host3.com") {
		t.Error("expected host3.com to NOT be allowed")
	}
}

func TestSanitizeTimestamp(t *testing.T) {
	tests := []struct {
		ts      string
		wantErr bool
	}{
		{"", true},
		{"01:23", false},
		{"1:23:45", false},
		{"01:23.5", false},
		{"*01:23-01:45", false},
		{"01:23-01:45", false},
		{"abc", true},
		{"01:23:45;rm -rf /", true},
		{"01:23\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.ts, func(t *testing.T) {
			err := SanitizeTimestamp(tt.ts)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for timestamp %q", tt.ts)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for timestamp %q: %v", tt.ts, err)
			}
		})
	}
}

func TestValidateVideoID(t *testing.T) {
	tests := []struct {
		id      string
		wantErr bool
	}{
		{"", true},
		{"abc123", false},
		{"ABC_DEF-xyz", false},
		{"a", false},
		{"abc def", true},
		{"abc/def", true},
		{"abc;def", true},
		{string(make([]byte, 65)), true},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			err := ValidateVideoID(tt.id)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for id %q", tt.id)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for id %q: %v", tt.id, err)
			}
		})
	}
}
