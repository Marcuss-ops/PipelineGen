package drive

import (
	"context"
	"fmt"
)

// EnsureFolderPath walks a list of segments under rootID, creating each folder
// if it doesn't exist.  Returns the final folder ID.
//
// This replaces duplicated GetOrCreateFolder loops in Artlist, Stock, Ingest,
// and Google Accounting.
func EnsureFolderPath(ctx context.Context, uploader *Uploader, rootID string, segments ...string) (string, error) {
	if uploader == nil {
		return rootID, nil
	}
	currentID := rootID
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		id, err := uploader.GetOrCreateFolder(ctx, seg, currentID)
		if err != nil {
			return "", fmt.Errorf("ensure folder %q (segment %d of %d): %w", seg, i+1, len(segments), err)
		}
		currentID = id
	}
	return currentID, nil
}
