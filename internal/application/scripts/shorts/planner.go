// Package shorts builds the small, deterministic hand-off used by the
// Remotion Shorts editor. It deliberately does not call an LLM: the caller
// supplies the already-approved text, clips and indexed sound-effect cues.
package shorts

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/remotionjob"
)

var subtitleArtifacts asset.SubtitleArtifactRepository

func SetSubtitleArtifactRepository(repo asset.SubtitleArtifactRepository) {
	subtitleArtifacts = repo
}

const SchemaVersion = "remotion.shorts.v1"

var ErrInvalidRequest = errors.New("shorts: invalid request")

type Request struct {
	ID                  string        `json:"id"`
	Text                string        `json:"text"`
	Language            string        `json:"language,omitempty"`
	DurationMs          int64         `json:"duration_ms"`
	WordsPerCaption     int           `json:"words_per_caption,omitempty"`
	IncludeSoundEffects bool          `json:"include_sound_effects"`
	Clips               []Clip        `json:"clips"`
	SoundEffects        []SoundEffect `json:"sound_effects,omitempty"`
	FPS                 int           `json:"fps,omitempty"`
	Width               int           `json:"width,omitempty"`
	Height              int           `json:"height,omitempty"`
	UploadToDrive       bool          `json:"upload_to_drive,omitempty"`
	DriveFolderID       string        `json:"drive_folder_id,omitempty"`
	DriveFilename       string        `json:"drive_filename,omitempty"`
	Composition         string        `json:"composition,omitempty"`
	Captions            []Caption     `json:"captions,omitempty"`
}

// BuildRenderJob converts the deterministic Shorts plan into the one-way
// Remotion contract. Asset paths are resolved here, while Remotion only
// renders the paths and timings it receives.
func BuildRenderJob(req Request, plan Response) (remotionjob.RenderJob, error) {
	fps, width, height := req.FPS, req.Width, req.Height
	if fps == 0 {
		fps = 30
	}
	if width == 0 {
		width = 1080
	}
	if height == 0 {
		height = 1920
	}
	if fps < 1 || fps > 120 || width < 1 || height < 1 {
		return remotionjob.RenderJob{}, fmt.Errorf("%w: invalid fps/width/height", ErrInvalidRequest)
	}
	if req.UploadToDrive && normalizeDriveFolderID(req.DriveFolderID) == "" {
		return remotionjob.RenderJob{}, fmt.Errorf("%w: drive_folder_id is required when upload_to_drive=true", ErrInvalidRequest)
	}
	if req.UploadToDrive && strings.TrimSpace(req.Language) == "" {
		return remotionjob.RenderJob{}, fmt.Errorf("%w: language is required when upload_to_drive=true", ErrInvalidRequest)
	}
	frames := int((plan.DurationMs*int64(fps) + 999) / 1000)
	if frames < 1 {
		frames = 1
	}
	if len(plan.Clips) == 0 {
		return remotionjob.RenderJob{}, fmt.Errorf("%w: at least one clip is required", ErrInvalidRequest)
	}
	captionProps := make([]map[string]any, 0, len(plan.Captions))
	for _, caption := range plan.Captions {
		captionProps = append(captionProps, map[string]any{
			"start_ms": caption.StartMs,
			"end_ms":   caption.EndMs,
			"text":     caption.Text,
		})
	}
	sfxProps := make([]map[string]any, 0, len(plan.SoundEffects))
	for _, effect := range plan.SoundEffects {
		sfxProps = append(sfxProps, map[string]any{
			"file":   assetURL(effect.File),
			"atMs":   effect.AtMs,
			"volume": effect.Volume,
		})
	}
	comp := remotionjob.YouTubeShortComposition
	if req.Composition != "" {
		comp = req.Composition
	}
	if err := remotionjob.ValidateShortFormComposition(comp); err != nil {
		return remotionjob.RenderJob{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	clipProps := make([]map[string]any, 0, len(plan.Clips))
	for _, c := range plan.Clips {
		vol := 1.0
		if c.Volume != nil {
			vol = *c.Volume
		}
		var subDriveFileID string
		var subDriveURL string
		var subLocalPath string
		var subFileHash string
		if subtitleArtifacts != nil {
			artifacts, err := subtitleArtifacts.ListByAsset(context.Background(), c.ID)
			if err == nil {
				for _, artifact := range artifacts {
					if artifact.Format == asset.SubtitleFormatASS && artifact.Status == asset.SubtitleStatusReady && artifact.IsCurrent {
						subDriveFileID = artifact.DriveFileID
						subDriveURL = artifact.DriveURL
						subLocalPath = artifact.LocalPath
						subFileHash = artifact.FileHash
						break
					}
				}
			}
		}
		clipProps = append(clipProps, map[string]any{
			"id":                 c.ID,
			"path":               assetURL(c.Path),
			"startMs":            c.StartMs,
			"endMs":              c.EndMs,
			"clipStartMs":        c.ClipStartMs,
			"clipEndMs":          c.ClipEndMs,
			"volume":             vol,
			"subtitlesDriveID":   subDriveFileID,
			"subtitlesURL":       subDriveURL,
			"subtitlesLocalPath": subLocalPath,
			"subtitlesHash":      subFileHash,
		})
	}
	return remotionjob.RenderJob{
		SchemaVersion:    remotionjob.SchemaVersion,
		ID:               plan.ID,
		Composition:      comp,
		DurationInFrames: frames,
		FPS:              fps,
		Width:            width,
		Height:           height,
		Props: map[string]any{
			"brollPath":             assetURL(plan.Clips[0].Path),
			"quoteText":             plan.Text,
			"textColor":             "#FFFFFF",
			"captionHighlightColor": "#FFD400",
			"captionBaseColor":      "#FFFFFF",
			"eventBlendMs":          200,
			"captions":              captionProps,
			"soundEffects":          sfxProps,
			"clips":                 clipProps,
		},
		UploadToDrive: req.UploadToDrive,
		DriveFolderID: normalizeDriveFolderID(req.DriveFolderID),
		DriveFilename: driveFilename(req),
		Language:      plan.Language,
	}, nil
}

func normalizeDriveFolderID(value string) string {
	value = strings.TrimSpace(value)
	if marker := strings.Index(value, "/folders/"); marker >= 0 {
		value = value[marker+len("/folders/"):]
		if end := strings.IndexAny(value, "?/ "); end >= 0 {
			value = value[:end]
		}
	}
	return strings.TrimSpace(value)
}

func driveFilename(req Request) string {
	if name := strings.TrimSpace(req.DriveFilename); name != "" {
		return name
	}
	return strings.TrimSpace(req.ID) + ".mp4"
}

func assetURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/assets/") || strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "file://") {
		return value
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return value
	}
	return (&url.URL{Scheme: "file", Path: abs}).String()
}

type Clip struct {
	ID          string   `json:"id"`
	Path        string   `json:"path,omitempty"`
	StartMs     int64    `json:"start_ms,omitempty"`
	EndMs       int64    `json:"end_ms,omitempty"`
	ClipStartMs int64    `json:"clip_start_ms,omitempty"`
	ClipEndMs   int64    `json:"clip_end_ms,omitempty"`
	Volume      *float64 `json:"volume,omitempty"`
}

type SoundEffect struct {
	ID     string  `json:"id,omitempty"`
	File   string  `json:"file"`
	AtMs   int64   `json:"at_ms"`
	Volume float64 `json:"volume"`
}

type Caption struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

type Response struct {
	SchemaVersion       string        `json:"schema_version"`
	Format              string        `json:"format"`
	ID                  string        `json:"id"`
	Language            string        `json:"language,omitempty"`
	DurationMs          int64         `json:"duration_ms"`
	Text                string        `json:"text"`
	Clips               []Clip        `json:"clips"`
	Captions            []Caption     `json:"captions"`
	IncludeSoundEffects bool          `json:"include_sound_effects"`
	SoundEffects        []SoundEffect `json:"sound_effects"`
}

func Build(req Request) (Response, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.Text = strings.TrimSpace(req.Text)
	if req.ID == "" || req.Text == "" || req.DurationMs <= 0 {
		return Response{}, fmt.Errorf("%w: id, text and duration_ms are required", ErrInvalidRequest)
	}
	if len(req.Clips) == 0 {
		return Response{}, fmt.Errorf("%w: at least one clip is required", ErrInvalidRequest)
	}
	for i, clip := range req.Clips {
		if strings.TrimSpace(clip.ID) == "" {
			return Response{}, fmt.Errorf("%w: clips[%d].id is required", ErrInvalidRequest, i)
		}
		if clip.StartMs < 0 || clip.EndMs < clip.StartMs {
			return Response{}, fmt.Errorf("%w: invalid clip window at clips[%d]", ErrInvalidRequest, i)
		}
	}
	if req.WordsPerCaption <= 0 {
		req.WordsPerCaption = 4
	}

	var captions []Caption
	if len(req.Captions) > 0 {
		captions = req.Captions
	} else {
		captions = buildCaptions(req.Text, req.DurationMs, req.WordsPerCaption)
	}
	sfx := req.SoundEffects
	if !req.IncludeSoundEffects {
		sfx = []SoundEffect{}
	}
	for i, effect := range sfx {
		if strings.TrimSpace(effect.File) == "" || effect.AtMs < 0 || effect.AtMs >= req.DurationMs {
			return Response{}, fmt.Errorf("%w: invalid sound_effects[%d]", ErrInvalidRequest, i)
		}
		if effect.Volume == 0 {
			sfx[i].Volume = 0.5
		}
		if sfx[i].Volume < 0 || sfx[i].Volume > 1 {
			return Response{}, fmt.Errorf("%w: sound_effects[%d].volume must be between 0 and 1", ErrInvalidRequest, i)
		}
	}
	if sfx == nil {
		sfx = []SoundEffect{}
	}

	return Response{
		SchemaVersion: SchemaVersion, Format: "remotion-shorts", ID: req.ID,
		Language: req.Language, DurationMs: req.DurationMs, Text: req.Text,
		Clips: req.Clips, Captions: captions,
		IncludeSoundEffects: req.IncludeSoundEffects, SoundEffects: sfx,
	}, nil
}

func buildCaptions(text string, durationMs int64, wordsPerCaption int) []Caption {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []Caption{}
	}
	groups := make([][]string, 0, (len(words)+wordsPerCaption-1)/wordsPerCaption)
	for i := 0; i < len(words); i += wordsPerCaption {
		end := i + wordsPerCaption
		if end > len(words) {
			end = len(words)
		}
		groups = append(groups, words[i:end])
	}
	result := make([]Caption, 0, len(groups))
	for i, group := range groups {
		start := durationMs * int64(i) / int64(len(groups))
		end := durationMs * int64(i+1) / int64(len(groups))
		result = append(result, Caption{StartMs: start, EndMs: end, Text: strings.Join(group, " ")})
	}
	return result
}
