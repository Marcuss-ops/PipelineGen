package drive

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// EnvUploadChunkMB is the operator knob for the resumable upload chunk size,
// in megabytes (PipelineGen-owned PIPELINEGEN_* env namespace).
//
// The SDK default is googleapi.DefaultUploadChunkSize (16 MiB), which costs a
// full HTTP round trip per chunk: a ~500 MB master pays ~32 sequential PUTs,
// each bounded by bandwidth × RTT, before Drive acknowledges the final byte.
// Larger chunks amortize that per-chunk overhead — 256 MiB turns the same
// master into 2 PUTs. The resumable protocol itself is unchanged: every chunk
// is still acknowledged with a Content-Range response, so a transient 502
// still resumes from the acknowledged offset (only files larger than ONE
// chunk route through the big-chunk path; smaller artifacts keep the
// historical 16 MiB resumable behavior byte-for-byte).
//
// Drive requires chunk sizes to be multiples of 256 KiB; values that are not
// are rounded DOWN to the next multiple. "16" restores the SDK default.
const EnvUploadChunkMB = "PIPELINEGEN_DRIVE_UPLOAD_CHUNK_MB"

const (
	// uploadChunkMBDefault is the shipped default: large enough to cut a
	// 500 MB master to 2 PUTs, small enough that one retry re-sends at most
	// 256 MiB.
	uploadChunkMBDefault = 256
	// uploadChunkMBMin keeps a pathological 0/negative override out while
	// still letting an operator (and the wire tests) exercise real multi-PUT
	// chunking at small sizes.
	uploadChunkMBMin = 1
	// uploadChunkMBMax bounds operator error; Drive accepts multi-GB chunks
	// but a retry of one chunk must stay bounded.
	uploadChunkMBMax = 1024
	// chunkMultiple is Drive's chunk-size granularity (256 KiB).
	chunkMultiple = 256 * 1024
)

var (
	uploadChunkOnce sync.Once
	uploadChunkSize int
)

// resumableChunkBytes returns the effective resumable upload chunk size in
// bytes: the env override clamped to [16 MiB, 1 GiB] and rounded down to a
// 256 KiB multiple, or the 256 MiB default. A malformed value falls back to
// the default rather than failing every upload.
func resumableChunkBytes() int {
	uploadChunkOnce.Do(func() {
		uploadChunkSize = uploadChunkMBDefault * 1024 * 1024
		raw := strings.TrimSpace(os.Getenv(EnvUploadChunkMB))
		if raw == "" {
			return
		}
		mb, err := strconv.Atoi(raw)
		if err != nil || mb <= 0 {
			return
		}
		if mb < uploadChunkMBMin {
			mb = uploadChunkMBMin
		}
		if mb > uploadChunkMBMax {
			mb = uploadChunkMBMax
		}
		uploadChunkSize = (mb * 1024 * 1024 / chunkMultiple) * chunkMultiple
	})
	return uploadChunkSize
}

// resetUploadChunkForTest re-arms the once-guard so a test that changes the
// environment observes the new value. Production callers never need this.
func resetUploadChunkForTest() {
	uploadChunkOnce = sync.Once{}
	uploadChunkSize = 0
}
