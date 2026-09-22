package job

import "sort"

// StageName identifies one explicit generation stage. The stage contract is
// shared by parent and child jobs so progress is derived from child outcomes,
// not from a synthetic percentage.
//
// This is the WORKFLOW-PROGRESS dimension (what a child job completed), owned
// here; the EXECUTION/measurement dimension (where wall time was spent:
// script.prepare, overlay_render, clip.*, stock.*, ...) is owned by
// internal/kernel/observability.StageName. The two are deliberately separate
// vocabularies — see registry.go there.
type StageName string

const (
	StageScript      StageName = "script"
	StageClips       StageName = "clips"
	StageStock       StageName = "stock"
	StageTranslation StageName = "translation"
	StageVoiceover   StageName = "voiceover"
	StageOverlay     StageName = "overlay"
	StageRender      StageName = "render"
	StageUpload      StageName = "upload"
	StagePersistence StageName = "persistence"
)

// canonicalStageOrder is the ONE ordered declaration of the workflow stages.
// Every stage constant above MUST appear here exactly once, in the order a
// parent job observes its children; FlattenStageProgress is derived from it so
// the persisted order can no longer drift from the declarations.
//
// DO NOT append a stage that has no producer. A stage in this list is a claim
// about a child job that reports it, and godlike/07 no-fake-availability
// forbids advertising progress nobody observes. The producers are the
// postprocessors that perform the work: script (the item itself), clips
// (clip_bindings), stock (stock_bindings), translation,
// voiceover, overlay (visual_slots, the overlay layer plan), upload and
// persistence (adapters/postprocessor_progress.go::stageForProcessor is the
// canonical mapping, pinned by its test). `render` is produced by the durable
// Runner's localized render fan-out — it is the ONE stage whose producer is not
// a postprocessor (the item pipeline does not render video), projected through
// scriptgeneration.recordRenderStageProgress.
var canonicalStageOrder = []StageName{
	StageScript,
	StageClips,
	StageStock,
	StageTranslation,
	StageVoiceover,
	StageOverlay,
	StageRender,
	StageUpload,
	StagePersistence,
}

// CanonicalStageOrder returns the workflow stages in canonical order. The
// returned slice is a copy: callers cannot reorder the declaration.
func CanonicalStageOrder() []StageName {
	out := make([]StageName, len(canonicalStageOrder))
	copy(out, canonicalStageOrder)
	return out
}

type StageStatus string

const (
	StageQueued    StageStatus = "queued"
	StageRunning   StageStatus = "running"
	StageCompleted StageStatus = "completed"
	StageFailed    StageStatus = "failed"
	StageSkipped   StageStatus = "skipped"
)

// StageLanguageStatus is the durable observation emitted by one child job.
type StageLanguageStatus struct {
	Stage    StageName `json:"stage"`
	Language string    `json:"language"`
	// Unit is the language-independent work unit this observation belongs to
	// (a scene id, a clip id) for stages whose unit is NOT a language — the
	// clip/stock/overlay stages. It is empty for the language-scoped stages
	// (translation, voiceover, upload, persistence, script) and for rows
	// persisted before the field existed.
	//
	// Without it, two per-scene observations of the same stage collapse into
	// one entry per language and the parent under-reports its own fan-out.
	Unit   string      `json:"unit,omitempty"`
	Status StageStatus `json:"status"`
	JobID  string      `json:"job_id,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// StageProgress is the parent-facing aggregate for one stage.
type StageProgress struct {
	Stage     StageName             `json:"stage"`
	Completed int                   `json:"completed"`
	Total     int                   `json:"total"`
	Languages []StageLanguageStatus `json:"languages,omitempty"`
}

// AggregateStageProgressByStage groups child observations by stage while
// preserving input order. Total counts every observed child; Completed counts
// only terminal successful children.
func AggregateStageProgressByStage(statuses []StageLanguageStatus) map[string]StageProgress {
	out := make(map[string]StageProgress)
	for _, status := range statuses {
		if status.Stage == "" {
			continue
		}
		key := string(status.Stage)
		progress := out[key]
		if progress.Stage == "" {
			progress.Stage = status.Stage
		}
		progress.Total++
		if status.Status == StageCompleted {
			progress.Completed++
		}
		progress.Languages = append(progress.Languages, status)
		out[key] = progress
	}
	return out
}

// MergeStageProgress upserts observations from src into dst using the
// canonical (stage, language, unit, job_id) identity. It recomputes totals so
// repeated child updates do not inflate parent counters.
func MergeStageProgress(dst map[string]StageProgress, src map[string]StageProgress) map[string]StageProgress {
	if dst == nil {
		dst = make(map[string]StageProgress, len(src))
	}
	for stage, incoming := range src {
		current := dst[stage]
		if current.Stage == "" {
			current.Stage = incoming.Stage
		}
		for _, observation := range incoming.Languages {
			found := false
			for i := range current.Languages {
				if current.Languages[i].Language == observation.Language &&
					current.Languages[i].Unit == observation.Unit &&
					current.Languages[i].JobID == observation.JobID {
					current.Languages[i] = observation
					found = true
					break
				}
			}
			if !found {
				current.Languages = append(current.Languages, observation)
			}
		}
		current.Total = len(current.Languages)
		current.Completed = 0
		for _, observation := range current.Languages {
			if observation.Status == StageCompleted {
				current.Completed++
			}
		}
		dst[stage] = current
	}
	return dst
}

// FlattenStageProgress gives stable order to persisted progress maps.
func FlattenStageProgress(progress map[string]StageProgress) []StageLanguageStatus {
	ordered := canonicalStageOrder
	out := make([]StageLanguageStatus, 0)
	seen := make(map[string]struct{}, len(progress))
	for _, stage := range ordered {
		if item, ok := progress[string(stage)]; ok {
			out = append(out, item.Languages...)
			seen[string(stage)] = struct{}{}
		}
	}
	unknown := make([]string, 0, len(progress))
	for key := range progress {
		if _, ok := seen[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		out = append(out, progress[key].Languages...)
	}
	return out
}
