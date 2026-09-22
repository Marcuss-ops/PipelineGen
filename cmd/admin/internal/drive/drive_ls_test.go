package drive

import (
	"context"
	"io"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

const folderMime = "application/vnd.google-apps.folder"

// ── splitFolderPath ───────────────────────────────────────────────────

func TestSplitFolderPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "clips", []string{"clips"}},
		{"nested", "job/it", []string{"job", "it"}},
		{"deep", "a/b/c", []string{"a", "b", "c"}},
		{"trims segments", " a / b ", []string{"a", "b"}},
		{"drops empty segments", "a//b/", []string{"a", "b"}},
		{"empty", "", nil},
		{"only separators", "///", nil},
		{"whitespace", "   ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitFolderPath(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("splitFolderPath(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("splitFolderPath(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ── resolveDoctorRoot ─────────────────────────────────────────────────

func TestResolveDoctorRoot(t *testing.T) {
	cfgWithRoot := &config.Config{Drive: config.DriveConfig{MediaRootFolder: "cfg-root"}}
	cfgNoRoot := &config.Config{}

	tests := []struct {
		name       string
		flagRoot   string
		cfg        *config.Config
		wantID     string
		wantSource string
	}{
		{"flag wins", "flag-root", cfgWithRoot, "flag-root", "flag"},
		{"config when no flag", "", cfgWithRoot, "cfg-root", "config"},
		{"default when nothing configured", "", cfgNoRoot, config.DefaultMediaRootFolderID, "default"},
		{"flag trimmed", "  flag-root  ", cfgWithRoot, "flag-root", "flag"},
		{"nil cfg falls back to default", "x", nil, "x", "flag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, source := resolveDoctorRoot(tt.flagRoot, tt.cfg)
			if id != tt.wantID || source != tt.wantSource {
				t.Fatalf("resolveDoctorRoot(%q)=%q,%q want %q,%q", tt.flagRoot, id, source, tt.wantID, tt.wantSource)
			}
		})
	}
}

func TestResolveDoctorRoot_NilCfgUsesDefault(t *testing.T) {
	id, source := resolveDoctorRoot("", nil)
	if id != config.DefaultMediaRootFolderID || source != "default" {
		t.Fatalf("resolveDoctorRoot(\"\", nil)=%q,%q want default,%q", id, source, config.DefaultMediaRootFolderID)
	}
}

// ── sort / summarize helpers ─────────────────────────────────────────

func TestSortDriveListEntries_FoldersFirstThenName(t *testing.T) {
	entries := []driveListEntry{
		{Name: "zeta.txt", Type: "file", Path: "zeta.txt"},
		{Name: "beta", Type: "folder", Path: "beta"},
		{Name: "alpha.txt", Type: "file", Path: "alpha.txt"},
		{Name: "Alpha", Type: "folder", Path: "Alpha"},
	}
	sortDriveListEntries(entries)
	want := []string{"Alpha", "beta", "alpha.txt", "zeta.txt"}
	for i, w := range want {
		if entries[i].Name != w {
			t.Fatalf("sorted[%d] = %q, want %q (full: %+v)", i, entries[i].Name, w, entries)
		}
	}
}

func TestSummarizeDriveList(t *testing.T) {
	entries := []driveListEntry{
		{Type: "folder"}, {Type: "file"}, {Type: "file"},
	}
	folders, files := summarizeDriveList(entries)
	if folders != 1 || files != 2 {
		t.Fatalf("summarizeDriveList = %d folders, %d files; want 1, 2", folders, files)
	}
}

// ── walkDriveFolder ──────────────────────────────────────────────────

type stubDriveReader struct {
	children map[string][]platformdrive.DriveFileInfo
	errs     map[string]error
}

func (s *stubDriveReader) DownloadFile(context.Context, string) (io.ReadCloser, string, error) {
	return nil, "", nil
}
func (s *stubDriveReader) GetFileMD5(context.Context, string) (string, error) { return "", nil }
func (s *stubDriveReader) GetFileMeta(context.Context, string) (*platformdrive.FileMeta, error) {
	return nil, nil
}
func (s *stubDriveReader) ListFiles(_ context.Context, parentID string) ([]platformdrive.DriveFileInfo, error) {
	if s.errs != nil {
		if err, ok := s.errs[parentID]; ok {
			return nil, err
		}
	}
	return s.children[parentID], nil
}
func (s *stubDriveReader) FindFileByName(context.Context, string, string) (platformdrive.ExistingFileLookup, error) {
	return platformdrive.ExistingFileLookup{}, nil
}
func (s *stubDriveReader) FileIsNotTrashed(context.Context, string) (bool, error) { return true, nil }
func (s *stubDriveReader) FileExists(context.Context, string) (bool, error)       { return true, nil }
func (s *stubDriveReader) SearchFiles(context.Context, string) ([]platformdrive.DriveFileInfo, error) {
	return nil, nil
}

var _ platformdrive.Reader = (*stubDriveReader)(nil)

func newStubReader() *stubDriveReader {
	return &stubDriveReader{children: map[string][]platformdrive.DriveFileInfo{
		"root": {
			{ID: "en-id", Name: "en", MimeType: folderMime},
			{ID: "a-id", Name: "a.txt", MimeType: "text/plain", Size: 10},
		},
		"en-id": {
			{ID: "b-id", Name: "b.mp4", MimeType: "video/mp4", Size: 20},
		},
	}}
}

func TestWalkDriveFolder_NonRecursive(t *testing.T) {
	entries, err := walkDriveFolder(context.Background(), newStubReader(), "root", "", 0, false, 5, false, zap.NewNop())
	if err != nil {
		t.Fatalf("walkDriveFolder: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("non-recursive entries = %d, want 2 (%+v)", len(entries), entries)
	}
	for _, e := range entries {
		if e.Depth != 0 {
			t.Errorf("non-recursive entry %q has depth %d, want 0", e.Name, e.Depth)
		}
	}
}

func TestWalkDriveFolder_Recursive(t *testing.T) {
	entries, err := walkDriveFolder(context.Background(), newStubReader(), "root", "", 0, true, 5, false, zap.NewNop())
	if err != nil {
		t.Fatalf("walkDriveFolder: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("recursive entries = %d, want 3 (%+v)", len(entries), entries)
	}
	var b *driveListEntry
	for i := range entries {
		if entries[i].Name == "b.mp4" {
			b = &entries[i]
		}
	}
	if b == nil {
		t.Fatal("recursive walk did not descend into subfolder (b.mp4 missing)")
	}
	if b.Path != "en/b.mp4" || b.Depth != 1 {
		t.Errorf("b.mp4 path/depth = %q/%d, want en/b.mp4/1", b.Path, b.Depth)
	}
}

func TestWalkDriveFolder_MaxDepthZeroStopsRecursion(t *testing.T) {
	entries, err := walkDriveFolder(context.Background(), newStubReader(), "root", "", 0, true, 0, false, zap.NewNop())
	if err != nil {
		t.Fatalf("walkDriveFolder: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("max-depth=0 entries = %d, want 2 (no descent)", len(entries))
	}
}

func TestWalkDriveFolder_FilesOnlyStillWalks(t *testing.T) {
	entries, err := walkDriveFolder(context.Background(), newStubReader(), "root", "", 0, true, 5, true, zap.NewNop())
	if err != nil {
		t.Fatalf("walkDriveFolder: %v", err)
	}
	for _, e := range entries {
		if e.Type == "folder" {
			t.Errorf("files-only listing leaked folder entry %q", e.Name)
		}
	}
	// a.txt (root) + b.mp4 (nested) → folders hidden but traversal kept.
	if len(entries) != 2 {
		t.Fatalf("files-only entries = %d, want 2 (%+v)", len(entries), entries)
	}
}

func TestWalkDriveFolder_RootListErrorPropagates(t *testing.T) {
	boom := context.Canceled
	reader := &stubDriveReader{errs: map[string]error{"root": boom}}
	_, err := walkDriveFolder(context.Background(), reader, "root", "", 0, false, 5, false, zap.NewNop())
	if err == nil {
		t.Fatal("walkDriveFolder on failing root: expected error, got nil")
	}
}

func TestWalkDriveFolder_SubfolderErrorIsSkipped(t *testing.T) {
	reader := &stubDriveReader{
		children: map[string][]platformdrive.DriveFileInfo{
			"root": {{ID: "en-id", Name: "en", MimeType: folderMime}},
		},
		errs: map[string]error{"en-id": context.Canceled},
	}
	entries, err := walkDriveFolder(context.Background(), reader, "root", "", 0, true, 5, false, zap.NewNop())
	if err != nil {
		t.Fatalf("subfolder error must be non-fatal, got %v", err)
	}
	// Only the folder entry survives; descent into it failed silently.
	if len(entries) != 1 || entries[0].Name != "en" {
		t.Fatalf("entries = %+v, want just the unreadable folder entry", entries)
	}
}
