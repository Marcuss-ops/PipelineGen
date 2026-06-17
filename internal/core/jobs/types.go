package jobs

type JobType string

const (
	JobTypeArtlistRun            JobType = "artlist.run"
	JobTypeYouTubeClipExtract    JobType = "youtube_clip.extract"
	JobTypeScriptGenerate        JobType = "script.generate"
	JobTypeBatchScriptGenerate   JobType = "script.generate_batch"
	JobTypeScriptPublish         JobType = "script.publish"
	JobTypeVoiceoverGenerate     JobType = "voiceover.generate"
	JobTypeMediaMatch            JobType = "media.match"
	JobTypeMediaImport           JobType = "media.import"
	JobTypeMediaStock            JobType = "media.stock"
	JobTypeWorkflowRun           JobType = "workflow.run"
	JobTypeMediaGenerate         JobType = "media.generate_missing_asset"
	JobTypeMediaReindex          JobType = "media.reindex"
	JobTypeBooksProcess          JobType = "books.process"
	JobTypeLessonsProcess        JobType = "lessons.process"
	JobTypeClipScriptGenerate    JobType = "script.generate_from_clips"
	JobTypeCatalogScriptGenerate JobType = "script.generate_from_catalog"
	JobTypeMediaCurate           JobType = "script.curate"
)
