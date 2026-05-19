package jobs

import (
	"encoding/json"
	"fmt"
)

func DecodePayload(jobType JobType, raw json.RawMessage) (interface{}, error) {
	switch jobType {
	case JobTypeArtlistRun:
		var p ArtlistRunPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p, p.Validate()
	case JobTypeYouTubeClipExtract:
		var p YouTubeClipExtractPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p, p.Validate()
	case JobTypeScriptGenerate:
		var p ScriptGeneratePayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p, p.Validate()
	case JobTypeScriptPublish:
		var p ScriptPublishPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p, p.Validate()
	case JobTypeVoiceoverGenerate:
		var p VoiceoverGeneratePayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p, p.Validate()
	case JobTypeMediaMatch:
		var p MediaMatchPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p, p.Validate()
	case JobTypeMediaImport:
		var p MediaImportPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p, p.Validate()
	case JobTypeMediaStock:
		var p StockRunPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p, p.Validate()
	default:
		return nil, fmt.Errorf("unsupported job type %s", jobType)
	}
}
