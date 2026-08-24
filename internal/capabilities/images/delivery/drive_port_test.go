package delivery

import (
	"context"
	"testing"
)

type fakeDriveReader struct {
	files []DriveFile
	err   error
}

func (f fakeDriveReader) ListFiles(context.Context, string) ([]DriveFile, error) {
	return f.files, f.err
}

func TestDriveReaderPortShape(t *testing.T) {
	var reader DriveReader = fakeDriveReader{files: []DriveFile{{ID: "id-1", Name: "hero.png", MimeType: "image/png"}}}
	files, err := reader.ListFiles(context.Background(), "folder")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].ID != "id-1" || files[0].MimeType != "image/png" {
		t.Fatalf("unexpected files: %+v", files)
	}
}
