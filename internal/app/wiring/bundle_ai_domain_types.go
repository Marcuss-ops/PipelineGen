package wiring

import (
	vowiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ingest"
	texttracks "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/books"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/capabilities/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	translation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/jobs"
	voicesync "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/sync"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/platform/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

// AIBundle owns script generation and AI composition surfaces.
type AIBundle struct {
	OllamaClient             *client.Client
	OllamaEmbedClient        *client.Client
	Reranker                 *reranker.Client
	ScriptGen                *ollama.Generator
	OllamaTranslator         *translation.OllamaTranslator
	MemoryRepo               scriptports.MemoryGate
	MemorySvc                *adapters.Service
	ScriptEngine             *scriptcore.Engine
	WhisperTranscriber       youtubeports.WhisperTranscriberPort
	SceneTextGenerator       *SceneTextGenerator
	ScriptVoiceoverGenerator *vowiring.ScriptVoiceoverGenerator
}

// DomainBundle is the remaining application-level domain dependency bag. New
// wiring should prefer capability-owned Deps structs instead of adding fields.
type DomainBundle struct {
	CueWriter          texttracks.TimedCueWriter
	FolderPathWriter   texttracks.FolderPathWriter
	YoutubeClipService *youtube.Service
	SubtitleFetcher    youtubeports.SubtitleFetcherPort

	VoiceoverSync                *voicesync.Service
	ImageService                 *images.Service
	IngestService                *ingest.Service
	BooksService                 *books.Service
	LessonsService               *lessonsSvc.Service
	MetaWriter                   semantic.MetadataWriterPort
	RealtimeMatcher              assetsapi.RealtimeMatcher
	RealtimeSearch               scriptcore.RealtimeSearchService
	AutotagService               *autotag.Service
	AssocService                 scriptcore.AssocSearchService
	VoiceoverGenerateHandler     *voiceoverjobs.GenerateJobHandler
	VoiceoverProcessItem         voiceover.VoiceoverItemExecutor
	VoiceoverPublishPool         interface{ Wait() }
	VoiceoverGenerateItemHandler *voiceoverjobs.GenerateItemJobHandler
	ArtifactService              *artifacts.Service
	ImageSearchResolver          images.ImageSearchResolver
	AudioProcessor               *audioasset.Processor
}
