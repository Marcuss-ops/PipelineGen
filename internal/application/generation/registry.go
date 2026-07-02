package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	assetdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	domaingeneration "github.com/Marcuss-ops/PipelineGen/internal/domain/generation"
)

// Definition binds a public generation type to the payload builder
// used by the unified API.
type Definition struct {
	Type     domaingeneration.Type
	JobType  string
	Enabled  bool
	Validate func(json.RawMessage) error
	BuildJob func(ctx context.Context, input json.RawMessage, svc *Service) (any, error)
}

// Registry resolves generation types to definitions.
type Registry struct {
	defs map[domaingeneration.Type]Definition
}

// NewRegistry constructs a registry from a set of definitions.
func NewRegistry(defs ...Definition) *Registry {
	r := &Registry{defs: make(map[domaingeneration.Type]Definition, len(defs))}
	for _, def := range defs {
		_ = r.Register(def)
	}
	return r
}

// Register adds or replaces a definition.
func (r *Registry) Register(def Definition) error {
	if strings.TrimSpace(def.Type.String()) == "" {
		return fmt.Errorf("generation registry: type is required")
	}
	if strings.TrimSpace(def.JobType) == "" {
		return fmt.Errorf("generation registry: job type is required for %s", def.Type)
	}
	if def.Validate == nil {
		def.Validate = func(json.RawMessage) error { return nil }
	}
	if def.BuildJob == nil {
		return fmt.Errorf("generation registry: build function is required for %s", def.Type)
	}
	r.defs[def.Type] = def
	return nil
}

// Resolve returns the definition for a type.
func (r *Registry) Resolve(t domaingeneration.Type) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	def, ok := r.defs[t]
	return def, ok
}

// MustResolve returns the definition or an error.
func (r *Registry) MustResolve(t domaingeneration.Type) (Definition, error) {
	def, ok := r.Resolve(t)
	if !ok {
		return Definition{}, fmt.Errorf("unsupported generation type: %s", t)
	}
	return def, nil
}

// BookSource describes the accepted book input contract.
// Internal-only fields (OllamaURL, OutputPath) are hidden from the public
// API via json:"-" — the server resolves them from its own configuration.
// FilePath is resolved internally from the source asset and passed through
// to the worker; the client can also supply it directly for non-asset flows.
type BookSource struct {
	SourceAssetID string `json:"source_asset_id,omitempty"`
	FilePath      string `json:"file_path,omitempty"` // resolved from source asset or direct client path
	GoogleDocURL  string `json:"google_doc_url,omitempty"`
	Language      string `json:"language,omitempty"`
	Instruction   string `json:"instruction,omitempty"`
	Model         string `json:"model,omitempty"`
	PagesPerChunk int    `json:"pages_per_chunk,omitempty"`
	ChunkSize     int    `json:"chunk_size,omitempty"`
	OverlapSize   int    `json:"overlap_size,omitempty"`
	MaxChunks     int    `json:"max_chunks,omitempty"`
	OllamaURL     string `json:"-"` // resolved from server config, not accepted from client
	DriveFolderID string `json:"drive_folder_id,omitempty"`
	OutputPath    string `json:"-"` // resolved from server config, not accepted from client
	TranslateOnly bool   `json:"translate_only,omitempty"`
	GeneratePDF   bool   `json:"generate_pdf,omitempty"`
	PDFStyle      string `json:"pdf_style,omitempty"`
}

// LessonSource is the accepted lesson input contract.
// Internal fields (OllamaURL, Async) are hidden from the public API —
// the server resolves them from its own configuration. All generation is
// async by default.
type LessonSource struct {
	Title          string `json:"title,omitempty"`
	Topic          string `json:"topic,omitempty"`
	SourceText     string `json:"source_text,omitempty"`
	Language       string `json:"language,omitempty"`
	Tone           string `json:"tone,omitempty"`
	Model          string `json:"model,omitempty"`
	MaxChapters    int    `json:"max_chapters,omitempty"`
	GenerateImages bool   `json:"generate_images,omitempty"`
	ImageStyle     string `json:"image_style,omitempty"`
	ImageWidth     int    `json:"image_width,omitempty"`
	ImageHeight    int    `json:"image_height,omitempty"`
	GeneratePDF    bool   `json:"generate_pdf,omitempty"`
	OllamaURL      string `json:"-"` // resolved from server config, not accepted from client
	Async          bool   `json:"-"` // all generation is async; not a client choice
}

// ScriptSource is the accepted script input contract.
type ScriptSource struct {
	Topic               string   `json:"topic,omitempty"`
	SourceText          string   `json:"source_text,omitempty"`
	Guidelines          string   `json:"guidelines,omitempty"`
	ClipIDs             []string `json:"clip_ids,omitempty"`
	NumClips            int      `json:"num_clips,omitempty"`
	SegmentWords        int      `json:"segment_words,omitempty"`
	SegmentTopics       []string `json:"segment_topics,omitempty"`
	Title               string   `json:"title,omitempty"`
	OutputName          string   `json:"output_name,omitempty"`
	Language            string   `json:"language,omitempty"`
	Tone                string   `json:"tone,omitempty"`
	Style               string   `json:"style,omitempty"`
	Model               string   `json:"model,omitempty"`
	DriveFolderID       string   `json:"drive_folder_id,omitempty"`
	GenerateMetadata    bool     `json:"generate_metadata,omitempty"`
	GenerateSceneImages bool     `json:"generate_scene_images,omitempty"`
	GenerateVoiceover   bool     `json:"generate_voiceover,omitempty"`
	ExtractEntities     bool     `json:"extract_entities,omitempty"`
	ForceRefresh        bool     `json:"force_refresh,omitempty"`
}

// BatchSource is the accepted batch input contract.
type BatchSource struct {
	DocTitle            string `json:"doc_title,omitempty"`
	DriveFolderID       string `json:"drive_folder_id,omitempty"`
	Language            string `json:"language,omitempty"`
	Tone                string `json:"tone,omitempty"`
	Duration            int    `json:"duration,omitempty"`
	Model               string `json:"model,omitempty"`
	PromptVersion       string `json:"prompt_version,omitempty"`
	EditorPromptVersion string `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string `json:"qa_prompt_version,omitempty"`
	ChannelID           string `json:"channel_id,omitempty"`
	RequestTimeout      int    `json:"request_timeout,omitempty"`
	SaveToDB            bool   `json:"save_to_db,omitempty"`
	NoChapters          bool   `json:"no_chapters,omitempty"`
	Async               bool   `json:"-"` // all batch generation is async; not a client choice
	Items               []struct {
		Topic      string `json:"topic,omitempty"`
		SourceText string `json:"source_text,omitempty"`
	} `json:"items,omitempty"`
	BatchTopics []struct {
		Topic      string `json:"topic,omitempty"`
		SourceText string `json:"source_text,omitempty"`
	} `json:"batch_topics,omitempty"`
}

// resolveBookSource converts an asset into a usable book job payload.
func resolveBookSource(d *assetdomain.Details) (filePath, googleDocURL string) {
	if d == nil || d.Asset == nil {
		return "", ""
	}
	if loc := d.LocalLocation(); loc != nil {
		if p := strings.TrimSpace(loc.URI); p != "" {
			return p, ""
		}
	}
	if a := d.Asset; a != nil {
		if p := strings.TrimSpace(a.LocalPath()); p != "" {
			return p, ""
		}
		if link := strings.TrimSpace(a.SourceURL); link != "" {
			return "", link
		}
		if link := strings.TrimSpace(a.DriveLink()); link != "" {
			return "", link
		}
	}
	return "", ""
}
