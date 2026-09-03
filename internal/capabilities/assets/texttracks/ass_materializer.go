package texttracks

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

type SubtitleMaterializerInput struct {
	AssetID string
	// DriveFilename is the source clip filename when known. Keeping the
	// basename in the sidecar name makes the association obvious in Drive;
	// AssetID remains the deterministic fallback for legacy callers.
	DriveFilename   string
	LanguageCode    string
	TextTrackID     int64
	ClipDurationMs  int64
	TimedCues       []detail.TimedCue
	SubtitleStyleID string
	ClipContentHash string
	DriveFolderID   string
}

type SubtitleMaterializerOutput struct {
	LocalPath       string
	LegacyFileMD5   string
	CuesHash        string
	TextHash        string
	CoveredDuration int64
	ValidationError string
	CueCount        int
	LastCueEndMs    int64
}

type SubtitleArtifactMaterializer struct {
	repo      detail.SubtitleArtifactRepository
	root      string
	publisher delivery.Publisher
}

func NewSubtitleArtifactMaterializer(repo detail.SubtitleArtifactRepository, rootPath string, publisher delivery.Publisher) *SubtitleArtifactMaterializer {
	if rootPath == "" {
		rootPath = "data/media/subtitles"
	}
	return &SubtitleArtifactMaterializer{
		repo:      repo,
		root:      rootPath,
		publisher: publisher,
	}
}

func (m *SubtitleArtifactMaterializer) Materialize(ctx context.Context, in SubtitleMaterializerInput) (*SubtitleMaterializerOutput, error) {
	if in.AssetID == "" {
		return nil, fmt.Errorf("ass_materializer: asset_id is required")
	}
	if in.LanguageCode == "" {
		return nil, fmt.Errorf("ass_materializer: language_code is required")
	}
	if len(in.TimedCues) == 0 {
		return nil, fmt.Errorf("ass_materializer: timed_cues is empty")
	}

	// 1. Compute text and cues hashes
	var fullTextBuilder strings.Builder
	for i, c := range in.TimedCues {
		if i > 0 {
			fullTextBuilder.WriteString(" ")
		}
		fullTextBuilder.WriteString(c.Text)
	}
	fullText := fullTextBuilder.String()

	textHashBytes := digest.SHA256Bytes([]byte(fullText))
	textHash := textHashBytes

	cuesJSON, err := json.Marshal(in.TimedCues)
	if err != nil {
		return nil, fmt.Errorf("ass_materializer: marshal cues: %w", err)
	}
	cuesHashBytes := digest.SHA256Bytes(cuesJSON)
	cuesHash := cuesHashBytes[:8] // Keep 8 chars for the file path as requested

	// Idempotency is resolved before touching the filesystem or Drive.  The
	// subtitle is a durable asset, not a per-render temporary.  Reprocessing a
	// clip with the same current text/cues/style must reuse the existing ASS and
	// its Drive reference; otherwise every render creates a duplicate subtitle
	// file even though the content has not changed.
	if current, lookupErr := m.repo.FindCurrent(ctx, in.AssetID, in.LanguageCode, detail.SubtitleFormatASS); lookupErr != nil {
		return nil, fmt.Errorf("ass_materializer: lookup current artifact: %w", lookupErr)
	} else if current != nil && current.Status == detail.SubtitleStatusReady && current.IsCurrent &&
		current.TextHash == textHash && current.CuesHash == cuesHashBytes &&
		current.StyleVersion == in.SubtitleStyleID && current.ClipDurationMs == in.ClipDurationMs &&
		(in.ClipContentHash == "" || current.ClipContentHash == in.ClipContentHash) &&
		current.DriveFileID != "" && current.DriveURL != "" {
		if _, statErr := os.Stat(current.LocalPath); statErr == nil {
			return &SubtitleMaterializerOutput{
				LocalPath:       current.LocalPath,
				LegacyFileMD5:   current.LegacyFileMD5,
				CuesHash:        current.CuesHash,
				TextHash:        current.TextHash,
				CoveredDuration: current.LastCueEndMs,
				CueCount:        current.CueCount,
				LastCueEndMs:    current.LastCueEndMs,
			}, nil
		}
		return nil, fmt.Errorf("ass_materializer: canonical subtitle %q exists on Drive but is not materialized locally; refusing duplicate generation/upload", current.DriveFileID)
	}

	// 2. Build deterministic ASS file path
	localDir := filepath.Join(m.root, in.AssetID, in.LanguageCode)
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return nil, fmt.Errorf("ass_materializer: mkdir: %w", err)
	}
	localPath := filepath.Join(localDir, fmt.Sprintf("%s.ass", cuesHash))

	// 3. Generate ASS Content
	assContent := m.generateASSContent(in.TimedCues, in.SubtitleStyleID)

	// Write to file
	if err := os.WriteFile(localPath, []byte(assContent), 0644); err != nil {
		return nil, fmt.Errorf("ass_materializer: write file: %w", err)
	}

	// Calculate File Hash
	fileHashBytes := digest.SHA256Bytes([]byte(assContent))
	fileHash := fileHashBytes

	// 4. Validate ASS file
	vErr := validateASSFile(localPath, in.ClipDurationMs)
	validationErrorStr := ""
	status := detail.SubtitleStatusReady
	if vErr != nil {
		validationErrorStr = vErr.Error()
		status = detail.SubtitleStatusFailed
	}

	var lastCueEndMs int64
	if len(in.TimedCues) > 0 {
		lastCueEndMs = in.TimedCues[len(in.TimedCues)-1].EndMs
	}

	output := &SubtitleMaterializerOutput{
		LocalPath:       localPath,
		LegacyFileMD5:   fileHash,
		CuesHash:        cuesHashBytes, // Full hash for database record
		TextHash:        textHash,
		CoveredDuration: lastCueEndMs,
		ValidationError: validationErrorStr,
		CueCount:        len(in.TimedCues),
		LastCueEndMs:    lastCueEndMs,
	}

	// 5. Register in DB
	art := &detail.SubtitleArtifact{
		AssetID:          in.AssetID,
		TextTrackID:      in.TextTrackID,
		LanguageCode:     in.LanguageCode,
		Format:           detail.SubtitleFormatASS,
		LocalPath:        localPath,
		LegacyFileMD5:    fileHash,
		TextHash:         textHash,
		CuesHash:         output.CuesHash,
		ClipContentHash:  in.ClipContentHash,
		CueCount:         len(in.TimedCues),
		ClipDurationMs:   in.ClipDurationMs,
		LastCueEndMs:     lastCueEndMs,
		StyleVersion:     in.SubtitleStyleID,
		GeneratorVersion: "vidrush-ass-v2",
		Status:           status,
		IsCurrent:        true,
		ValidationError:  validationErrorStr,
	}

	if err := m.repo.Upsert(ctx, art); err != nil {
		return nil, fmt.Errorf("ass_materializer: db upsert: %w", err)
	}
	if status != detail.SubtitleStatusReady {
		return output, nil
	}
	markFailed := func(cause error) (*SubtitleMaterializerOutput, error) {
		art.Status = detail.SubtitleStatusFailed
		art.ValidationError = cause.Error()
		if persistErr := m.repo.Upsert(ctx, art); persistErr != nil {
			return nil, fmt.Errorf("%w; persist failed status: %v", cause, persistErr)
		}
		return nil, cause
	}
	if m.publisher == nil {
		return markFailed(fmt.Errorf("ass_materializer: Drive publisher is not configured"))
	}
	if strings.TrimSpace(in.DriveFolderID) == "" {
		return markFailed(fmt.Errorf("ass_materializer: Drive folder is not configured for asset %s", in.AssetID))
	}
	filename := fmt.Sprintf("%s.%s.ass", in.AssetID, in.LanguageCode)
	if strings.TrimSpace(in.DriveFilename) != "" {
		base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(in.DriveFilename)), filepath.Ext(strings.TrimSpace(in.DriveFilename)))
		if base != "" {
			filename = fmt.Sprintf("%s.%s.ass", base, in.LanguageCode)
		}
	}
	result, err := m.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationClipMetadata,
		DestinationFolderID: in.DriveFolderID,
		LocalPath:           localPath,
		Filename:            filename,
		AssetID:             in.AssetID,
		DestinationSubpath:  []string{"Ass Sub"},
		ContentHash:         fileHash,
		IdempotencyKey:      delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, in.AssetID+":"+in.LanguageCode, fileHash, in.TextTrackID),
		ConflictPolicy:      delivery.ConflictOverwrite,
	})
	if err != nil {
		return markFailed(fmt.Errorf("ass_materializer: publish Drive artifact: %w", err))
	}
	if result == nil || result.FileID == "" || result.WebViewLink == "" {
		return markFailed(fmt.Errorf("ass_materializer: publish Drive artifact returned incomplete result"))
	}
	art.DriveFileID = result.FileID
	art.DriveURL = result.WebViewLink
	if err := m.repo.Upsert(ctx, art); err != nil {
		return nil, fmt.Errorf("ass_materializer: persist Drive artifact: %w", err)
	}

	return output, nil
}

type FontPreset struct {
	FontName string
	FontSize int
	Bold     int
	Outline  float64
	Shadow   int
	MarginV  int
}

var defaultFontPresets = map[string]FontPreset{
	"montserrat": {FontName: "Montserrat", FontSize: 54, Bold: 1, Outline: 3.5, Shadow: 2, MarginV: 32},
	"impact":     {FontName: "Impact", FontSize: 58, Bold: 0, Outline: 3.5, Shadow: 2, MarginV: 32},
	"anton":      {FontName: "Anton", FontSize: 56, Bold: 0, Outline: 3.5, Shadow: 2, MarginV: 32},
	"bebas":      {FontName: "Bebas Neue", FontSize: 60, Bold: 0, Outline: 3.5, Shadow: 2, MarginV: 32},
	"roboto":     {FontName: "Roboto", FontSize: 52, Bold: 1, Outline: 3.0, Shadow: 2, MarginV: 32},
}

// ResolveFontPreset maps a style ID or preset name to standard typography parameters.
func ResolveFontPreset(styleID string) FontPreset {
	norm := strings.ToLower(strings.TrimSpace(styleID))
	if strings.Contains(norm, "impact") {
		return defaultFontPresets["impact"]
	}
	if strings.Contains(norm, "anton") {
		return defaultFontPresets["anton"]
	}
	if strings.Contains(norm, "bebas") {
		return defaultFontPresets["bebas"]
	}
	if strings.Contains(norm, "roboto") {
		return defaultFontPresets["roboto"]
	}
	return defaultFontPresets["montserrat"]
}

// CompileASSContent is the canonical, deterministic ASS content generator
// shared by the durable materializer and clip.render's subtitle compiler.
// Identical cues + style ALWAYS produce identical bytes; it generates no
// absolute paths. Fail-closed: empty cues are a typed error, never an
// empty/placeholder ASS (speech recognition is never regenerated just to
// build subtitles).
func CompileASSContent(cues []detail.TimedCue, styleID string) (string, error) {
	if len(cues) == 0 {
		return "", fmt.Errorf("ass_materializer: timed_cues is empty")
	}
	if styleID == "" {
		styleID = "vidrush-default"
	}
	preset := ResolveFontPreset(styleID)
	var sb strings.Builder
	sb.WriteString("[Script Info]\n")
	sb.WriteString("Title: PipelineGen Auto Subtitles\n")
	sb.WriteString("ScriptType: v4.00+\n")
	sb.WriteString("WrapStyle: 0\n")
	sb.WriteString("PlayResX: 1920\n")
	sb.WriteString("PlayResY: 1080\n\n")

	sb.WriteString("[V4+ Styles]\n")
	sb.WriteString("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	alignment := 2 // bottom-center: the existing default for short-form clips.
	if strings.Contains(strings.ToLower(strings.TrimSpace(styleID)), "center") {
		alignment = 5 // true screen center, requested by the centered-subtitles preset.
	}
	sb.WriteString(fmt.Sprintf("Style: %s,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,%d,0,0,0,100,100,0,0,1,%.1f,%d,%d,10,10,%d,1\n\n",
		styleID, preset.FontName, preset.FontSize, preset.Bold, preset.Outline, preset.Shadow, alignment, preset.MarginV))

	sb.WriteString("[Events]\n")
	sb.WriteString("Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")

	var lastEndMs int64
	for _, c := range cues {
		startMs, endMs := normalizeASSWindow(c.StartMs, c.EndMs)
		// Source tracks can contain overlapping cue windows. ASS validation is
		// intentionally strict, so serialize the windows at centisecond
		// precision while preserving their deterministic order.
		if startMs < lastEndMs {
			startMs = lastEndMs
		}
		if endMs <= startMs {
			endMs = startMs + 10
		}
		startStr := formatASSTime(startMs)
		endStr := formatASSTime(endMs)
		// Clean dialogue text from line breaks/returns to keep ASS format valid
		text := strings.ReplaceAll(c.Text, "\n", " ")
		text = strings.ReplaceAll(text, "\r", "")
		sb.WriteString(fmt.Sprintf("Dialogue: 0,%s,%s,%s,,0,0,0,,%s\n", startStr, endStr, styleID, text))
		lastEndMs = endMs
	}

	return sb.String(), nil
}

// generateASSContent is the materializer's private convenience wrapper over
// the canonical generator (kept for call-site symmetry with the old API).
func (m *SubtitleArtifactMaterializer) generateASSContent(cues []detail.TimedCue, styleID string) string {
	content, err := CompileASSContent(cues, styleID)
	if err != nil {
		return ""
	}
	return content
}

// normalizeASSWindow accounts for ASS's centisecond timestamp precision.
// Very short source cues can otherwise round to the same timestamp and
// produce an invalid Dialogue line. The one-centisecond minimum keeps the
// artifact valid while preserving the cue's deterministic ordering.
func normalizeASSWindow(startMs, endMs int64) (int64, int64) {
	if startMs < 0 {
		startMs = 0
	}
	if endMs < startMs {
		endMs = startMs
	}
	startMs = (startMs / 10) * 10
	endMs = (endMs / 10) * 10
	if endMs <= startMs {
		endMs = startMs + 10
	}
	return startMs, endMs
}

func formatASSTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	m := (ms % 3600000) / 60000
	s := (ms % 60000) / 1000
	c := (ms % 1000) / 10
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, c)
}

// ValidateASSFile is the canonical ASS structural validator (single owner).
// clip.render's subtitle compiler validates its scratch artifact through
// this before sealing the plan — the plan never references an invalid ASS.
func ValidateASSFile(path string, clipDurationMs int64) error {
	return validateASSFile(path, clipDurationMs)
}

func validateASSFile(path string, clipDurationMs int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	hasScriptInfo := false
	hasStyles := false
	hasEvents := false
	hasDialogue := false

	scanner := bufio.NewScanner(f)
	var lastEndMs int64 = -1
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Check UTF-8 validity
		if !utf8.ValidString(line) {
			return fmt.Errorf("invalid UTF-8 at line %d", lineNum)
		}

		if line == "[Script Info]" {
			hasScriptInfo = true
		} else if line == "[V4+ Styles]" {
			hasStyles = true
		} else if line == "[Events]" {
			hasEvents = true
		} else if strings.HasPrefix(line, "Dialogue:") {
			hasDialogue = true
			// Validate Dialogue structure
			parts := strings.SplitN(line, ",", 10)
			if len(parts) < 10 {
				return fmt.Errorf("malformed Dialogue line at %d", lineNum)
			}
			startStr := parts[1]
			endStr := parts[2]
			text := parts[9]

			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("empty Dialogue text at line %d", lineNum)
			}

			startMs, err := parseASSTime(startStr)
			if err != nil {
				return fmt.Errorf("invalid start time %q at line %d: %w", startStr, lineNum, err)
			}
			endMs, err := parseASSTime(endStr)
			if err != nil {
				return fmt.Errorf("invalid end time %q at line %d: %w", endStr, lineNum, err)
			}

			if startMs < 0 || endMs < 0 {
				return fmt.Errorf("negative timestamp at line %d", lineNum)
			}
			if startMs >= endMs {
				return fmt.Errorf("start time >= end time at line %d", lineNum)
			}
			if startMs < lastEndMs {
				return fmt.Errorf("cues not sorted or overlap at line %d", lineNum)
			}
			lastEndMs = endMs
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if !hasScriptInfo {
		return fmt.Errorf("missing [Script Info] section")
	}
	if !hasStyles {
		return fmt.Errorf("missing [V4+ Styles] section")
	}
	if !hasEvents {
		return fmt.Errorf("missing [Events] section")
	}
	if !hasDialogue {
		return fmt.Errorf("missing Dialogue lines")
	}

	// Validate last End <= duration + 250 ms
	if clipDurationMs > 0 && lastEndMs > clipDurationMs+250 {
		return fmt.Errorf("last cue end (%d ms) exceeds clip duration (%d ms + 250ms tolerance)", lastEndMs, clipDurationMs)
	}

	return nil
}

func parseASSTime(t string) (int64, error) {
	var h, m, s, c int64
	_, err := fmt.Sscanf(t, "%d:%02d:%02d.%02d", &h, &m, &s, &c)
	if err != nil {
		return 0, err
	}
	return h*3600000 + m*60000 + s*1000 + c*10, nil
}
