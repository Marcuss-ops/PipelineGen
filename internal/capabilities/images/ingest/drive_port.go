package ingest

import "context"

// DriveFile is the provider-neutral file summary needed by image sync.
type DriveFile struct {
	ID          string
	Name        string
	MimeType    string
	WebViewLink string
}

// DriveReader is the read-only Drive boundary used by image ingest sync.
type DriveReader interface {
	ListFiles(context.Context, string) ([]DriveFile, error)
}
