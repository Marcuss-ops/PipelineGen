package drive

import (
	"testing"
)

// ── stringListFlag ────────────────────────────────────────────────────

func TestStringListFlag_SplitsTrimsAndAppends(t *testing.T) {
	var f stringListFlag
	if err := f.Set("a, b"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := f.Set(" c "); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := f.Set(",,,"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(f) != len(want) {
		t.Fatalf("stringListFlag = %v, want %v", f, want)
	}
	for i := range want {
		if f[i] != want[i] {
			t.Fatalf("stringListFlag[%d] = %q, want %q", i, f[i], want[i])
		}
	}
	if got := f.String(); got != "a,b,c" {
		t.Fatalf("String() = %q, want %q", got, "a,b,c")
	}
}

// ── parseDriveMVArgs ─────────────────────────────────────────────────

func TestParseDriveMVArgs(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		from    string
		fnm     string
		to      string
		wantErr bool
	}{
		{name: "files only", files: []string{"a", " b "}, to: "dest"},
		{name: "name and from", from: "src", fnm: "clip.mp4", to: "dest"},
		{name: "files with from", files: []string{"a"}, from: "src", to: "dest"},
		{name: "missing to", files: []string{"a"}, wantErr: true},
		{name: "file and name conflict", files: []string{"a"}, from: "src", fnm: "x", to: "dest", wantErr: true},
		{name: "name without from", fnm: "x", to: "dest", wantErr: true},
		{name: "neither file nor name", to: "dest", wantErr: true},
		{name: "from equals to", files: []string{"a"}, from: "same", to: "same", wantErr: true},
		{name: "blank file ids ignored", files: []string{" ", ""}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDriveMVArgs(tt.files, tt.from, tt.fnm, tt.to)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseDriveMVArgs(%v,%q,%q,%q): expected error, got %+v", tt.files, tt.from, tt.fnm, tt.to, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDriveMVArgs: unexpected error: %v", err)
			}
			if got.To != "dest" {
				t.Fatalf("To = %q, want dest", got.To)
			}
		})
	}
}

func TestParseDriveMVArgs_TrimsAndNormalises(t *testing.T) {
	// Note: comma-splitting is owned by stringListFlag; parseDriveMVArgs
	// receives already-split values and only trims/drops blanks.
	got, err := parseDriveMVArgs([]string{" a ", "b", "", "   "}, "  src  ", "", "  dest  ")
	if err != nil {
		t.Fatalf("parseDriveMVArgs: %v", err)
	}
	if got.From != "src" || got.To != "dest" {
		t.Fatalf("From/To = %q/%q, want src/dest", got.From, got.To)
	}
	if len(got.Files) != 2 || got.Files[0] != "a" || got.Files[1] != "b" {
		t.Fatalf("Files = %v, want [a b]", got.Files)
	}
}
