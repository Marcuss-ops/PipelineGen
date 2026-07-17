// Package shorts builds the small, deterministic hand-off used by the
// Remotion Shorts editor. It deliberately does not call an LLM: the caller
// supplies the already-approved text, clips and indexed sound-effect cues.
package shorts

import (
	"errors"
	"fmt"
	"strings"
)

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
}

type Clip struct {
	ID      string `json:"id"`
	Path    string `json:"path,omitempty"`
	StartMs int64  `json:"start_ms,omitempty"`
	EndMs   int64  `json:"end_ms,omitempty"`
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

	captions := buildCaptions(req.Text, req.DurationMs, req.WordsPerCaption)
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
