package main

// ass.go builds the ASS track the delivery burn needs from the SAME certified
// timings the pipeline used for the voiceover. It deliberately lives in the
// velox CLI: the remote Master assembles the final video and has no subtitle
// contract, so the only honest place to burn captions is the delivery of the
// certified artifact, after the download and before the Drive publication.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

const (
	cueMaxChars       = 42
	cueMaxDurationUS  = 3_600_000
	cueTailUS         = 250_000
	cueLineChars      = 38
	cueMinPunctuation = 18
)

type assWord struct {
	Index   int    `json:"index"`
	Text    string `json:"text"`
	StartUS int64  `json:"start_us"`
	EndUS   int64  `json:"end_us"`
}

type assSceneSpeech struct {
	SceneID string    `json:"scene_id"`
	Words   []assWord `json:"words"`
}

type assPhraseTiming struct {
	SceneIndex      int    `json:"scene_index"`
	Text            string `json:"text"`
	TimelineStartUS int64  `json:"timeline_start_us"`
}

type subtitleCue struct {
	SceneID string
	Start   float64
	End     float64
	Text    string
}

type assStyle struct {
	Font      string
	FontSize  int
	Primary   string
	Outline   string
	Back      string
	OutlineW  float64
	Shadow    float64
	Alignment int
	MarginL   int
	MarginR   int
	MarginV   int
}

type subtitleStyleSpec struct {
	Font       string `json:"font"`
	Position   string `json:"position"`
	Color      string `json:"color"`
	FontSizePX int    `json:"font_size_px"`
	Stroke     struct {
		Color string  `json:"color"`
		Width float64 `json:"width"`
	} `json:"stroke"`
	Shadow struct {
		Color   string  `json:"color"`
		Opacity float64 `json:"opacity"`
		BlurPX  float64 `json:"blur_px"`
		OffsetX float64 `json:"offset_x"`
		OffsetY float64 `json:"offset_y"`
	} `json:"shadow"`
}

func defaultASSstyle() assStyle {
	return assStyle{
		Font:      "Poppins",
		FontSize:  64,
		Primary:   "#FFD400",
		Outline:   "#000000",
		Back:      "#000000",
		OutlineW:  6,
		Shadow:    6,
		Alignment: 2,
		MarginL:   80,
		MarginR:   80,
		MarginV:   60,
	}
}

// buildSubtitles decodes a job document (the /api/jobs/{id}/full body) and
// returns the cues plus the requested ASS style.
func buildSubtitles(raw []byte) ([]subtitleCue, assStyle, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, assStyle{}, fmt.Errorf("decode job document: %w", err)
	}
	node, ok := findMapWithKey(doc, "scene_speech_timings")
	if !ok {
		return nil, assStyle{}, fmt.Errorf("job document has no scene_speech_timings")
	}
	encoded, err := json.Marshal(node)
	if err != nil {
		return nil, assStyle{}, fmt.Errorf("re-encode timing node: %w", err)
	}
	var payload struct {
		PhraseTimings []assPhraseTiming `json:"phrase_timings"`
		SceneSpeech   []assSceneSpeech  `json:"scene_speech_timings"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, assStyle{}, fmt.Errorf("decode timing node: %w", err)
	}
	if len(payload.SceneSpeech) == 0 {
		return nil, assStyle{}, fmt.Errorf("job document has no scene speech timings")
	}
	if len(payload.PhraseTimings) < len(payload.SceneSpeech) {
		return nil, assStyle{}, fmt.Errorf("job document has %d phrase timings for %d scenes; cannot place the captions on the timeline",
			len(payload.PhraseTimings), len(payload.SceneSpeech))
	}
	style := defaultASSstyle()
	if spec, ok := findSubtitleStyle(doc); ok {
		style = assStyleFromSpec(spec)
	}
	cues := make([]subtitleCue, 0, len(payload.SceneSpeech)*4)
	lastStart := int64(-1)
	for i, scene := range payload.SceneSpeech {
		offsetUS := payload.PhraseTimings[i].TimelineStartUS
		if offsetUS < 0 {
			return nil, assStyle{}, fmt.Errorf("scene %q has a negative timeline start", scene.SceneID)
		}
		if offsetUS < lastStart {
			return nil, assStyle{}, fmt.Errorf("scene %q timeline start %dus is not monotonic", scene.SceneID, offsetUS)
		}
		lastStart = offsetUS
		cues = append(cues, buildSceneCues(scene, offsetUS)...)
	}
	if len(cues) == 0 {
		return nil, assStyle{}, fmt.Errorf("the certified timings produced no caption cues")
	}
	normalizeCues(cues)
	return cues, style, nil
}

// buildSceneCues turns one scene's word timings into readable cues. The
// algorithm mirrors the reviewed delivery fallback: up to 42 characters or
// 3.6 s per cue, a punctuation break once the cue is long enough, and a 0.25 s
// tail so the caption does not vanish on the exact final syllable.
func buildSceneCues(scene assSceneSpeech, offsetUS int64) []subtitleCue {
	var cues []subtitleCue
	var current []assWord
	var startUS, endUS int64

	flush := func() {
		if len(current) == 0 {
			return
		}
		text := strings.TrimSpace(joinWords(current))
		if text != "" {
			start := float64(startUS+offsetUS) / 1e6
			end := float64(endUS+offsetUS+cueTailUS) / 1e6
			cues = append(cues, subtitleCue{SceneID: scene.SceneID, Start: start, End: end, Text: text})
		}
		current = nil
		startUS = 0
		endUS = 0
	}

	for _, word := range scene.Words {
		text := strings.TrimSpace(word.Text)
		if text == "" {
			continue
		}
		if len(current) > 0 {
			candidateLen := joinedLen(current, text)
			candidateDur := word.EndUS - startUS
			if candidateLen > cueMaxChars || candidateDur > cueMaxDurationUS {
				flush()
			}
		}
		if len(current) == 0 {
			startUS = word.StartUS
		}
		current = append(current, word)
		endUS = word.EndUS
		if endsWithPunctuation(text) && len(joinWords(current)) >= cueMinPunctuation {
			flush()
		}
	}
	flush()
	return cues
}

// normalizeCues keeps the track monotonic. Two cues may share a timeline
// instant when a scene ends exactly where the next one starts; the earlier cue
// closes just before the later one opens.
func normalizeCues(cues []subtitleCue) {
	for i := range cues {
		if cues[i].End <= cues[i].Start {
			cues[i].End = cues[i].Start + 0.6
		}
		if i == len(cues)-1 {
			continue
		}
		if cues[i].End > cues[i+1].Start-0.02 {
			cues[i].End = math.Max(cues[i].Start+0.6, cues[i+1].Start-0.02)
		}
	}
}

func joinWords(words []assWord) string {
	parts := make([]string, 0, len(words))
	for _, word := range words {
		if text := strings.TrimSpace(word.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func joinedLen(words []assWord, extra string) int {
	length := 0
	for _, word := range words {
		text := strings.TrimSpace(word.Text)
		if text == "" {
			continue
		}
		if length > 0 {
			length++
		}
		length += len(text)
	}
	if extra != "" {
		if length > 0 {
			length++
		}
		length += len(extra)
	}
	return length
}

func endsWithPunctuation(text string) bool {
	switch text[len(text)-1] {
	case '.', ',', ';', ':':
		return true
	default:
		return false
	}
}

func assStyleFromSpec(spec subtitleStyleSpec) assStyle {
	style := defaultASSstyle()
	if font := strings.TrimSpace(spec.Font); font != "" {
		style.Font = font
	}
	if spec.FontSizePX > 0 {
		style.FontSize = spec.FontSizePX
	}
	if colour := strings.TrimSpace(spec.Color); colour != "" {
		style.Primary = colour
	}
	if colour := strings.TrimSpace(spec.Stroke.Color); colour != "" {
		style.Outline = colour
	}
	if spec.Stroke.Width > 0 {
		style.OutlineW = spec.Stroke.Width
	}
	if colour := strings.TrimSpace(spec.Shadow.Color); colour != "" {
		style.Back = colour
	}
	if spec.Shadow.OffsetY > 0 {
		style.Shadow = spec.Shadow.OffsetY
	}
	style.Alignment = assAlignment(spec.Position)
	if style.Alignment == 8 {
		style.MarginV = 60
	}
	return style
}

func assAlignment(position string) int {
	switch strings.ToLower(strings.TrimSpace(position)) {
	case "top_left":
		return 7
	case "top_center", "top":
		return 8
	case "top_right":
		return 9
	case "center", "middle":
		return 5
	case "bottom_left":
		return 1
	case "bottom_right":
		return 3
	default:
		return 2
	}
}

// assColour converts #RRGGBB to the ASS &HAABBGGRR form. alpha is 0 for
// opaque, 26 for a 90% opaque shadow.
func assColour(colour string, alpha int) (string, error) {
	value := strings.TrimPrefix(strings.TrimSpace(colour), "#")
	if len(value) == 3 {
		value = strings.Join([]string{value[0:1], value[0:1], value[1:2], value[1:2], value[2:3], value[2:3]}, "")
	}
	if len(value) != 6 {
		return "", fmt.Errorf("invalid colour %q (want #RRGGBB)", colour)
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("invalid colour %q: %w", colour, err)
	}
	return fmt.Sprintf("&H%02X%02X%02X%02X", alpha, raw[2], raw[1], raw[0]), nil
}

func writeASS(w io.Writer, cues []subtitleCue, style assStyle) error {
	primary, err := assColour(style.Primary, 0)
	if err != nil {
		return err
	}
	outline, err := assColour(style.Outline, 0)
	if err != nil {
		return err
	}
	back, err := assColour(style.Back, 26)
	if err != nil {
		return err
	}
	if style.FontSize <= 0 {
		style.FontSize = 64
	}
	fmt.Fprint(w, "[Script Info]\n")
	fmt.Fprint(w, "ScriptType: v4.00+\n")
	fmt.Fprint(w, "PlayResX: 1920\n")
	fmt.Fprint(w, "PlayResY: 1080\n")
	fmt.Fprint(w, "WrapStyle: 2\n")
	fmt.Fprint(w, "ScaledBorderAndShadow: yes\n")
	fmt.Fprint(w, "\n[V4+ Styles]\n")
	fmt.Fprint(w, "Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	fmt.Fprintf(w, "Style: Default,%s,%d,%s,&H000000FF,%s,%s,0,0,0,0,100,100,0,0,1,%g,%g,%d,%d,%d,%d,1\n",
		style.Font, style.FontSize, primary, outline, back,
		style.OutlineW, style.Shadow, style.Alignment, style.MarginL, style.MarginR, style.MarginV)
	fmt.Fprint(w, "\n[Events]\n")
	fmt.Fprint(w, "Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	for _, cue := range cues {
		fmt.Fprintf(w, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n",
			assTimestamp(cue.Start), assTimestamp(cue.End), assEscape(splitCaption(cue.Text)))
	}
	return nil
}

func assTimestamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	hours := int(seconds / 3600)
	minutes := int(math.Mod(seconds, 3600) / 60)
	secs := math.Mod(seconds, 60)
	return fmt.Sprintf("%d:%02d:%05.2f", hours, minutes, secs)
}

// splitCaption wraps a caption on a word boundary. ASS uses the literal
// two-character sequence \N for a hard line break.
func splitCaption(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= cueLineChars {
		return text
	}
	cut := strings.LastIndex(text[:cueLineChars+1], " ")
	if cut <= 0 {
		return text
	}
	return text[:cut] + `\N` + text[cut+1:]
}

func assEscape(text string) string {
	text = strings.ReplaceAll(text, "\r", "")
	text = strings.ReplaceAll(text, "\n", `\N`)
	text = strings.ReplaceAll(text, "{", "(")
	text = strings.ReplaceAll(text, "}", ")")
	return text
}

// findMapWithKey walks an arbitrary JSON tree and returns the first map that
// carries the requested key. The job document nests the run result differently
// across API versions; the search keeps the decoder independent of that shape.
func findMapWithKey(v any, key string) (map[string]any, bool) {
	switch node := v.(type) {
	case map[string]any:
		if _, ok := node[key]; ok {
			return node, true
		}
		for _, child := range node {
			if found, ok := findMapWithKey(child, key); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range node {
			if found, ok := findMapWithKey(child, key); ok {
				return found, true
			}
		}
	}
	return nil, false
}

// findSubtitleStyle locates the payload's subtitles.style map without assuming
// where the request is nested in the job document.
func findSubtitleStyle(v any) (subtitleStyleSpec, bool) {
	switch node := v.(type) {
	case map[string]any:
		if raw, ok := node["subtitles"]; ok {
			if subs, ok := raw.(map[string]any); ok {
				if styleRaw, ok := subs["style"]; ok {
					encoded, err := json.Marshal(styleRaw)
					if err == nil {
						var spec subtitleStyleSpec
						if json.Unmarshal(encoded, &spec) == nil {
							return spec, true
						}
					}
				}
			}
		}
		for _, child := range node {
			if spec, ok := findSubtitleStyle(child); ok {
				return spec, true
			}
		}
	case []any:
		for _, child := range node {
			if spec, ok := findSubtitleStyle(child); ok {
				return spec, true
			}
		}
	}
	return subtitleStyleSpec{}, false
}
