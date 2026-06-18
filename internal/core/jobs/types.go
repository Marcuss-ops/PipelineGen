package jobs

// JobType is a type alias for string, the canonical job type identifier.
// The legacy models.JobType has been eliminated; all job types are now
// plain strings matched against the Dispatcher registry.
type JobType = string

const (
	JobTypeBatchScriptGenerate   JobType = "script.generate_batch"
	JobTypeClipScriptGenerate    JobType = "script.generate_from_clips"
	JobTypeCatalogScriptGenerate JobType = "script.generate_from_catalog"
	JobTypeMediaCurate           JobType = "media.curate"
)
