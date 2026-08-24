package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

type fakeImagesDriveReader struct {
	files []drive.DriveFileInfo
	err   error
}

func (f fakeImagesDriveReader) ListFiles(context.Context, string) ([]drive.DriveFileInfo, error) {
	return f.files, f.err
}

func TestImagesDriveReaderAdapter_MapsFiles(t *testing.T) {
	adapter := newImagesDriveReaderAdapter(fakeImagesDriveReader{files: []drive.DriveFileInfo{{ID: "id-1", Name: "hero.png", MimeType: "image/png", WebViewLink: "https://drive/file"}}})
	files, err := adapter.ListFiles(context.Background(), "folder")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].ID != "id-1" || files[0].Name != "hero.png" || files[0].WebViewLink != "https://drive/file" {
		t.Fatalf("unexpected mapped files: %+v", files)
	}
}

func TestImagesDriveReaderAdapter_PropagatesError(t *testing.T) {
	want := errors.New("drive unavailable")
	adapter := newImagesDriveReaderAdapter(fakeImagesDriveReader{err: want})
	_, err := adapter.ListFiles(context.Background(), "folder")
	if !errors.Is(err, want) {
		t.Fatalf("ListFiles error = %v, want %v", err, want)
	}
}

func TestImagesDriveReaderAdapter_NilFailsClosed(t *testing.T) {
	adapter := newImagesDriveReaderAdapter(nil)
	if adapter != nil {
		t.Fatal("nil concrete reader should not produce an adapter")
	}
}
