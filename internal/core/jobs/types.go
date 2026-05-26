package jobs

type JobType string

const (
	JobTypeArtlistRun         JobType = "artlist.run"
	JobTypeYouTubeClipExtract JobType = "youtube_clip.extract"
	JobTypeScriptGenerate     JobType = "script.generate"
	JobTypeScriptPublish      JobType = "script.publish"
	JobTypeVoiceoverGenerate  JobType = "voiceover.generate"
	JobTypeMediaMatch         JobType = "media.match"
	JobTypeMediaImport        JobType = "media.import"
	JobTypeMediaStock         JobType = "media.stock"
	JobTypeWorkflowRun        JobType = "workflow.run"
	JobTypeMediaGenerate      JobType = "media.generate_missing_asset"
	JobTypeMediaReindex       JobType = "media.reindex"
)



