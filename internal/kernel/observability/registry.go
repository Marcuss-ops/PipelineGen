package observability

// StageName identifies one canonical pipeline stage in the global
// observability taxonomy.
//
// NOTE: this is the OBSERVABILITY stage taxonomy (validate/acquire/
// process/...). It is intentionally distinct from
// internal/kernel/job.StageName (script/translation/voiceover/...),
// which identifies the workflow-stage dimension of job progress. The
// two vocabularies are kept separate so a job can report both its
// workflow progress and its execution timing without merging the two
// concepts.
type StageName string

const (
	StageValidate StageName = "validate"
	StageResolve  StageName = "resolve"
	StageGenerate StageName = "generate"
	StageDiscover StageName = "discover"
	StageAcquire  StageName = "acquire"
	StageProcess  StageName = "process"
	StageEnrich   StageName = "enrich"
	StagePersist  StageName = "persist"
	StageIndex    StageName = "index"
	StagePublish  StageName = "publish"
	StageVerify   StageName = "verify"
	StageCleanup  StageName = "cleanup"
)

// ComponentName identifies one external component (the adapter boundary).
type ComponentName string

const (
	ComponentOllama         ComponentName = "ollama"
	ComponentArtlist        ComponentName = "artlist"
	ComponentYouTube        ComponentName = "youtube"
	ComponentFFmpeg         ComponentName = "ffmpeg"
	ComponentDrive          ComponentName = "drive"
	ComponentSQLite         ComponentName = "sqlite"
	ComponentQdrant         ComponentName = "qdrant"
	ComponentNLP            ComponentName = "nlp"
	ComponentTTS            ComponentName = "tts"
	ComponentGoogleDocs     ComponentName = "google_docs"
	ComponentInternetImages ComponentName = "internet_images"
)

// OperationName identifies one operation at an external boundary.
type OperationName string

const (
	OperationSearch      OperationName = "search"
	OperationUpsert      OperationName = "upsert"
	OperationUpload      OperationName = "upload"
	OperationDownload    OperationName = "download"
	OperationTranscode   OperationName = "transcode"
	OperationCut         OperationName = "cut"
	OperationNormalize   OperationName = "normalize"
	OperationMerge       OperationName = "merge"
	OperationGenerate    OperationName = "generate"
	OperationSynthesize  OperationName = "synthesize"
	OperationTranscribe  OperationName = "transcribe"
	OperationTransaction OperationName = "transaction"
	OperationIndex       OperationName = "index"
	OperationEmbed       OperationName = "embed"
	OperationPublish     OperationName = "publish"
	OperationProbe       OperationName = "probe"
	OperationFetch       OperationName = "fetch"
	OperationExtract     OperationName = "extract"
	OperationResolve     OperationName = "resolve"
	OperationEnrich      OperationName = "enrich"
	OperationVerify      OperationName = "verify"
)

// AllStages returns the canonical stage names in registry order. It is
// useful to gate tooling (reporting, dashboards) that must not invent
// stage names outside the registry.
func AllStages() []StageName {
	return []StageName{
		StageValidate, StageResolve, StageGenerate, StageDiscover,
		StageAcquire, StageProcess, StageEnrich, StagePersist,
		StageIndex, StagePublish, StageVerify, StageCleanup,
	}
}

// AllComponents returns the canonical component names in registry order.
func AllComponents() []ComponentName {
	return []ComponentName{
		ComponentOllama, ComponentArtlist, ComponentYouTube,
		ComponentFFmpeg, ComponentDrive, ComponentSQLite,
		ComponentQdrant, ComponentNLP, ComponentTTS,
		ComponentGoogleDocs, ComponentInternetImages,
	}
}
