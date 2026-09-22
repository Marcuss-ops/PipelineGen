package drive

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

type fakeClipFolderWriter struct {
	rows      []*detail.ClipFolder
	upsertErr error
}

func (f *fakeClipFolderWriter) UpsertFolder(_ context.Context, folder *detail.ClipFolder) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.rows = append(f.rows, folder)
	return nil
}

func scanReader() *stubDriveReader {
	return &stubDriveReader{children: map[string][]platformdrive.DriveFileInfo{
		"root": {
			{ID: "stock-id", Name: "stock", MimeType: folderMime},
			{ID: "clips-id", Name: "clips", MimeType: folderMime},
			{ID: "readme", Name: "readme.txt", MimeType: "text/plain"},
		},
		"stock-id": {
			{ID: "cars-id", Name: "cars", MimeType: folderMime},
		},
		"clips-id": {},
	}}
}

func rowFor(t *testing.T, w *fakeClipFolderWriter, path string) *detail.ClipFolder {
	t.Helper()
	for _, r := range w.rows {
		if r.FolderPath == path {
			return r
		}
	}
	t.Fatalf("no row with folder_path %q (rows=%+v)", path, w.rows)
	return nil
}

func TestScanFolders_SyncsFoldersAndCounts(t *testing.T) {
	writer := &fakeClipFolderWriter{}
	scanned, synced, err := scanFolders(context.Background(), scanReader(), writer, "root", "", "", true, zap.NewNop())
	if err != nil {
		t.Fatalf("scanFolders: %v", err)
	}
	if scanned != 3 || synced != 3 {
		t.Fatalf("scanned/synced = %d/%d, want 3/3", scanned, synced)
	}
	if len(writer.rows) != 3 {
		t.Fatalf("writer rows = %d, want 3", len(writer.rows))
	}
	// Root-level name → source mapping (stock→stock, clips→youtube).
	if got := rowFor(t, writer, "stock"); got.Source != "stock" || got.Group != "stock" {
		t.Errorf("stock row = %+v, want source/group stock", got)
	}
	// Child inherits the parent's source.
	child := rowFor(t, writer, "stock/cars")
	if child.Source != "stock" {
		t.Errorf("stock/cars source = %q, want stock (inherited)", child.Source)
	}
	if clips := rowFor(t, writer, "clips"); clips.Source != "youtube" {
		t.Errorf("clips source = %q, want youtube", clips.Source)
	}
	// Files must never be cataloged as folders.
	if len(writer.rows) != 3 {
		t.Errorf("readme.txt was cataloged: rows=%+v", writer.rows)
	}
}

func TestScanFolders_ReadOnlyCountsScannedButSyncsNothing(t *testing.T) {
	scanned, synced, err := scanFolders(context.Background(), scanReader(), nil, "root", "", "", false, zap.NewNop())
	if err != nil {
		t.Fatalf("scanFolders: %v", err)
	}
	if scanned != 3 {
		t.Errorf("scanned = %d, want 3 (read-only still counts)", scanned)
	}
	if synced != 0 {
		t.Errorf("synced = %d, want 0 when --sync-db=false", synced)
	}
}

func TestScanFolders_WriterErrorDoesNotCountAsSynced(t *testing.T) {
	writer := &fakeClipFolderWriter{upsertErr: errors.New("db down")}
	scanned, synced, err := scanFolders(context.Background(), scanReader(), writer, "root", "", "", true, zap.NewNop())
	if err != nil {
		t.Fatalf("scanFolders: %v", err)
	}
	if scanned != 3 {
		t.Errorf("scanned = %d, want 3", scanned)
	}
	if synced != 0 {
		t.Errorf("synced = %d, want 0 when every upsert fails", synced)
	}
	if len(writer.rows) != 0 {
		t.Errorf("writer rows = %d, want 0", len(writer.rows))
	}
}

func TestScanFolders_NilReaderFailsClosed(t *testing.T) {
	if _, _, err := scanFolders(context.Background(), nil, nil, "root", "", "", false, zap.NewNop()); err == nil {
		t.Fatal("scanFolders with nil reader: expected error, got nil")
	}
}

func TestScanFolders_ListErrorPropagates(t *testing.T) {
	reader := &stubDriveReader{errs: map[string]error{"root": context.Canceled}}
	if _, _, err := scanFolders(context.Background(), reader, nil, "root", "", "", false, zap.NewNop()); err == nil {
		t.Fatal("scanFolders with failing ListFiles: expected error, got nil")
	}
}
