package scripts

import (
	"sort"
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func mapScriptToClipScenes(script string, pack interface{}) []ClipScene {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil
	}
	clipIDs, clipNames := clipIdentityFromPack(pack)
	if scenes := scenesFromMarkers(script, clipIDs, clipNames); len(scenes) > 0 {
		return scenes
	}
	if len(clipIDs) == 0 {
		return []ClipScene{{SceneIndex: 0, Text: script, Kind: "narration"}}
	}
	blocks := splitScriptAcrossClips(script, len(clipIDs))
	scenes := make([]ClipScene, 0, len(clipIDs))
	for i, id := range clipIDs {
		text := ""
		if i < len(blocks) {
			text = strings.TrimSpace(blocks[i])
		}
		if text == "" && i < len(clipNames) {
			text = strings.TrimSpace(clipNames[i])
		}
		if text == "" {
			text = id
		}
		scenes = append(scenes, ClipScene{SceneIndex: i, Text: text, ClipID: id, Kind: "clip"})
	}
	return scenes
}

type scriptMarker struct {
	start int
	end   int
	kind  string
	value string
}

func scenesFromMarkers(script string, clipIDs, clipNames []string) []ClipScene {
	markers := make([]scriptMarker, 0)
	for _, match := range textutil.StripClipMarkerRe.FindAllStringIndex(script, -1) {
		marker := script[match[0]:match[1]]
		value := ""
		if colon := strings.Index(marker, ":"); colon >= 0 {
			value = strings.TrimSpace(marker[colon+1 : len(marker)-1])
		}
		markers = append(markers, scriptMarker{start: match[0], end: match[1], kind: "clip", value: value})
	}
	for _, match := range textutil.StripNarrationMarkerRe.FindAllStringIndex(script, -1) {
		markers = append(markers, scriptMarker{start: match[0], end: match[1], kind: "narration"})
	}
	if len(markers) == 0 {
		return nil
	}
	sort.SliceStable(markers, func(i, j int) bool { return markers[i].start < markers[j].start })
	scenes := make([]ClipScene, 0, len(markers))
	for i, marker := range markers {
		end := len(script)
		if i+1 < len(markers) {
			end = markers[i+1].start
		}
		text := strings.TrimSpace(script[marker.end:end])
		if text == "" {
			continue
		}
		clipID := ""
		if marker.kind == "clip" {
			clipID = resolveMarkerClipID(marker.value, clipIDs, clipNames)
		}
		scenes = append(scenes, ClipScene{SceneIndex: len(scenes), Text: text, ClipID: clipID, Kind: marker.kind})
	}
	return scenes
}
