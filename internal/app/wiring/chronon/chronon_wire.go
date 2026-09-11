// chronon_wire.go owns the minimal chronon.render-plan.v2 wire shapes PipelineGen
// still needs. RenderingGen owns the semantic compiler and the full layer
// vocabulary; the former PipelineGen plan projector was deleted — the only
// remaining producer here is the native GPU certification probe, which
// synthesizes the minimal video-only plan (NVDEC → CUDA/Vulkan surface →
// composite → NVENC) that reproduces the CUDA_ERROR_ILLEGAL_ADDRESS handoff.
package chronon

const (
	chrononSchema  = "chronon.render-plan.v2"
	chrononVersion = 2
)

type chrononRenderPlan struct {
	Schema  string         `json:"schema"`
	Version int            `json:"version"`
	JobID   string         `json:"job_id"`
	Canvas  chrononCanvas  `json:"canvas"`
	Layers  []chrononLayer `json:"layers"`
	Output  chrononOutput  `json:"output"`
}

type chrononCanvas struct {
	Width          int `json:"width"`
	Height         int `json:"height"`
	FPSNum         int `json:"fps_num"`
	FPSDen         int `json:"fps_den"`
	DurationFrames int `json:"duration_frames"`
}

type chrononLayer struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Source         string `json:"source,omitempty"`
	Fit            string `json:"fit,omitempty"`
	StartFrame     int    `json:"start_frame"`
	DurationFrames int    `json:"duration_frames"`
}

type chrononOutput struct {
	Path   string `json:"path"`
	Format string `json:"format,omitempty"`
	Codec  string `json:"codec,omitempty"`
}
