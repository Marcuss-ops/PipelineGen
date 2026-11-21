// Package finaljob owns the neutral contracts for the Producer-side
// final-job segment handoff. The capability builds the request and consumes
// the receipt; platform/media/finaljob implements the mechanics. Neither side
// owns the type, so the adapter does not import the scripts business package.
package finaljob

import (
	"context"

	scriptfinaljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/finaljob"
)

// VideoSegmentRequest identifies a complete non-stock source window that must
// be certified for the remote copy assembler. Stock-pool slots never enter
// this interface; the Master owns those sources.
type VideoSegmentRequest struct {
	SegmentID          string
	SourceAssetID      string
	SourceSHA256       string
	SourceInUS         int64
	SourceDurationUS   int64
	TimelineStartFrame int64
	FrameCount         int64
	TimelineRevision   int64
	TimelineSHA256     string
	PlacementTag       string
	ReuseFromSHA256    string
	ReuseFromURI       string
}

// VideoSegmentPreparer is the capability-facing port. The implementation lives
// in platform/media/finaljob; the interface and its DTOs live in the kernel.
type VideoSegmentPreparer interface {
	PrepareFinalJobVideoSegment(context.Context, scriptfinaljob.RuntimeAssetV1, VideoSegmentRequest) (scriptfinaljob.RuntimeAssetV1, error)
}

// SegmentPrepTiming records Producer-side preparation for the non-stock clip
// and overlay windows included in one final-job handoff. JSON tags are the
// receipt wire contract: moving the type must not rename a field.
type SegmentPrepTiming struct {
	Segments     int                 `json:"segments,omitempty"`
	Reused       int                 `json:"reused,omitempty"`
	Cached       int                 `json:"cached,omitempty"`
	Derived      int                 `json:"derived,omitempty"`
	Copied       int                 `json:"copied,omitempty"`
	DownloadMS   int64               `json:"download_ms,omitempty"`
	EncodeMS     int64               `json:"encode_ms,omitempty"`
	VerifyMS     int64               `json:"verify_ms,omitempty"`
	PublishMS    int64               `json:"publish_ms,omitempty"`
	Bytes        int64               `json:"bytes,omitempty"`
	PaddedFrames int64               `json:"padded_frames,omitempty"`
	Windows      []SegmentPrepWindow `json:"windows,omitempty"`
}

// SegmentPrepWindow identifies the work for one non-stock timeline placement.
// Paths are local diagnostics and never source stock.
type SegmentPrepWindow struct {
	SegmentID    string `json:"segment_id,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	Normalized   bool   `json:"normalized,omitempty"`
	Path         string `json:"path,omitempty"`
	Frames       int64  `json:"frames,omitempty"`
	DownloadMS   int64  `json:"download_ms,omitempty"`
	CopyMS       int64  `json:"copy_ms,omitempty"`
	EncodeMS     int64  `json:"encode_ms,omitempty"`
	VerifyMS     int64  `json:"verify_ms,omitempty"`
	PublishMS    int64  `json:"publish_ms,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	PaddedFrames int64  `json:"padded_frames,omitempty"`
}
