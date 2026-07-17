// Package remotionjob defines the one-way hand-off contract from PipelineGen
// to the sibling Remotion editor worker.
package remotionjob

const SchemaVersion = "remotion.render-job.v1"

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
}
