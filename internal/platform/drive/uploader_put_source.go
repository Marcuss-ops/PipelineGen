package drive

import (
	"context"
	"fmt"
	"strings"
)

// Files above 5 MiB use Drive's resumable protocol. The previous 16 MiB
// threshold sent typical 6–7 MiB final-audio artifacts through multipart;
// every retry then restarted the entire upload from byte zero. Resumable
// uploads preserve the session and let Drive continue from the acknowledged
// offset after a transient 502/timeout.
// openUploadSource resolves the upload bytes and their length. A local path is
// opened and stat'd; otherwise the remote SourceURL is streamed with the
// caller's ExpectedSize (the resumable path needs a length).
func (u *Uploader) openUploadSource(ctx context.Context, req PutFileRequest) (uploadSource, int64, error) {
	if path := strings.TrimSpace(req.LocalPath); path != "" {
		f, err := u.openReader(path)
		if err != nil {
			return nil, 0, fmt.Errorf("open local file: %w", err)
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, 0, fmt.Errorf("stat local file: %w", err)
		}
		return f, info.Size(), nil
	}
	if strings.TrimSpace(req.SourceURL) == "" {
		return nil, 0, fmt.Errorf("putFile: source url is required when no local path is set")
	}
	if req.ExpectedSize <= 0 {
		return nil, 0, fmt.Errorf("putFile: a remote source requires the expected size")
	}
	return &httpObjectSource{
		ctx:       ctx,
		url:       req.SourceURL,
		size:      req.ExpectedSize,
		authToken: objectStoreTokenForURL(req.SourceURL),
	}, req.ExpectedSize, nil
}
