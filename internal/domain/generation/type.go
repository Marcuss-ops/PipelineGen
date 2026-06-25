package generation

// Type is the canonical public generation type.
type Type string

const (
	TypeScriptFromClips   Type = "script.from_clips"
	TypeScriptWithImages  Type = "script.with_images"
	TypeScriptBatch       Type = "script.batch"
	TypeLessonGenerate    Type = "lesson.generate"
	TypeBookGenerate      Type = "book.generate"
	TypeVoiceoverGenerate Type = "voiceover.generate"
	TypeImagesGenerate    Type = "images.generate"
	TypeMetadataExport    Type = "metadata.export"
)

func (t Type) String() string { return string(t) }
