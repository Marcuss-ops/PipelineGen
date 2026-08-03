// Package remotionjob defines the one-way hand-off contract from PipelineGen
// to the sibling Remotion editor worker.
package remotionjob

import "fmt"

const SchemaVersion = "remotion.render-job.v1"

// YouTubeShortComposition is the only composition PipelineGen may submit to
// the render boundary. Longform and compilation renders belong to a separate
// explicitly managed workflow and must never be triggered by script jobs.
const YouTubeShortComposition = "YouTubeShortComposition"

func ValidateShortFormComposition(composition string) error {
	if composition != YouTubeShortComposition {
		return fmt.Errorf("unsupported video render composition %q: only %s is enabled", composition, YouTubeShortComposition)
	}
	return nil
}

// RenderJob is produced by PipelineGen after script, asset and timing
// resolution. RemotionUpload only validates and renders this job; it must not
// resolve assets or access PipelineGen's SQLite, Qdrant, or provider APIs.
type RenderJob struct {
	SchemaVersion    string         `json:"schemaVersion"`
	ID               string         `json:"id"`
	Composition      string         `json:"composition"`
	DurationInFrames int            `json:"durationInFrames"`
	FPS              int            `json:"fps"`
	Width            int            `json:"width"`
	Height           int            `json:"height"`
	Props            map[string]any `json:"props"`
	UploadToDrive    bool           `json:"uploadToDrive,omitempty"`
	DriveFolderID    string         `json:"driveFolderId,omitempty"`
	DriveFilename    string         `json:"driveFilename,omitempty"`
	Language         string         `json:"language,omitempty"`
}
