package delivery

import "context"

// DriveFile is the provider-neutral file summary needed by image sync.
type DriveFile struct {
	ID          string
	Name        string
	MimeType    string
	WebViewLink string
}

// DriveReader is the application-owned read port for image Drive sync.
type DriveReader interface {
	ListFiles(context.Context, string) ([]DriveFile, error)
}
