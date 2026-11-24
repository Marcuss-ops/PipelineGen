package runner

import (
	"time"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SceneTextSnapshot is the read-only scene identity the prepare branch needs
// (id, index, source-language text, and any pre-existing annotations). It
// deliberately excludes Voiceover and duration fields so the branch can run
// concurrently with TTS without racing the TTS writer.
type SceneTextSnapshot struct {
	ID              string
	Index           int
	Text            string
	Annotations     *scriptpkg.SceneAnnotations
	ClipIDs         []string
	ExtractEntities scriptpkg.Toggle
}

// PrepareResult carries the computed outputs of the prepare branch, applied
// to the durable result by the caller after the join.
type PrepareResult struct {
	Segments       []scriptpkg.VidRushSegmentResult
	Annotations    map[int]*scriptpkg.SceneAnnotations
	DisabledScenes map[int]bool
	Intents        []capmodel.OverlayIntent
}

// PrepareOutcome is the channel payload the prepare branch sends back to the
// main goroutine. Err and NLPErr stay separate because they are reported
// against different stages: the VidRush prepare/join and translated NLP.
type PrepareOutcome struct {
	Result               PrepareResult
	Prefetched           *contract.AudioPrefetchResult
	Err                  error
	LocalizedAnnotations map[int]map[contract.Language]*scriptpkg.SceneAnnotations
	NLPErr               error
	CompletedAt          time.Time
}
