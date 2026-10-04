package drive

import (
	"context"
	"io"

	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

const resumableUploadThreshold = 5 * 1024 * 1024

func withCreateMedia(call *driveapi.FilesCreateCall, ctx context.Context, src uploadSource, size int64, mediaType string) *driveapi.FilesCreateCall {
	if size >= resumableUploadThreshold {
		if reader, opts, ok := bigChunkResumableOptions(src, size, mediaType); ok {
			return call.Media(reader, opts...).Context(ctx)
		}
		return call.ResumableMedia(ctx, src, size, mediaType)
	}
	return call.Media(src).Context(ctx)
}

func withUpdateMedia(call *driveapi.FilesUpdateCall, ctx context.Context, src uploadSource, size int64, mediaType string) *driveapi.FilesUpdateCall {
	if size >= resumableUploadThreshold {
		if reader, opts, ok := bigChunkResumableOptions(src, size, mediaType); ok {
			return call.Media(reader, opts...).Context(ctx)
		}
		return call.ResumableMedia(ctx, src, size, mediaType)
	}
	return call.Media(src).Context(ctx)
}

// bigChunkResumableOptions selects the caller-sized resumable upload route for
// artifacts larger than one configured chunk. Smaller files retain the Drive
// SDK's historical ResumableMedia path and its 16 MiB acknowledged chunks.
func bigChunkResumableOptions(src uploadSource, size int64, mediaType string) (io.Reader, []googleapi.MediaOption, bool) {
	chunk := resumableChunkBytes()
	if size <= int64(chunk) {
		return nil, nil, false
	}
	return io.NewSectionReader(src, 0, size), []googleapi.MediaOption{
		googleapi.ChunkSize(chunk),
		googleapi.ContentType(mediaType),
	}, true
}
