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
		return call.Media(src, resumableSmallFileOptions(size, mediaType)...).Context(ctx)
	}
	return call.Media(src, resumableSmallFileOptions(size, mediaType)...).Context(ctx)
}

func withUpdateMedia(call *driveapi.FilesUpdateCall, ctx context.Context, src uploadSource, size int64, mediaType string) *driveapi.FilesUpdateCall {
	if size >= resumableUploadThreshold {
		if reader, opts, ok := bigChunkResumableOptions(src, size, mediaType); ok {
			return call.Media(reader, opts...).Context(ctx)
		}
		return call.Media(src, resumableSmallFileOptions(size, mediaType)...).Context(ctx)
	}
	return call.Media(src, resumableSmallFileOptions(size, mediaType)...).Context(ctx)
}

// resumableSmallFileOptions preserves the Drive SDK's historical resumable
// route for artifacts below one configured chunk. The chunk equals the file
// size rounded down to the 256 KiB granularity the API requires, so the first
// chunk fills without hitting EOF and the SDK opens a resumable session
// instead of collapsing into a single multipart request. A 5 MiB artifact is
// therefore one acknowledged chunk PUT, exactly like the retired
// ResumableMedia route.
func resumableSmallFileOptions(size int64, mediaType string) []googleapi.MediaOption {
	const granularity = 256 << 10
	chunk := int(size) / granularity * granularity
	if chunk <= 0 {
		chunk = granularity
	}
	return []googleapi.MediaOption{
		googleapi.ChunkSize(chunk),
		googleapi.ContentType(mediaType),
	}
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
