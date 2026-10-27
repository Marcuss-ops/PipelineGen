package job

import "encoding/json"

// CanonicalManifest wraps operator-specific deterministic inputs. Keys are
// sorted by encoding/json and values are validated as JSON before persistence.
type CanonicalManifest struct {
	Kind   CanonicalManifestKind
	Fields InputManifest
}

func (m CanonicalManifest) JSON() (json.RawMessage, error) {
	if m.Fields == nil {
		return json.RawMessage(`{}`), nil
	}
	return json.Marshal(m.Fields)
}

func ClipManifest(sourceSHA string, startUS, endUS int64, width, height, fpsNum, fpsDen int64, codec, profile, pixelFormat, audioPolicy, normalizationPolicy, processorVersion string) CanonicalManifest {
	return CanonicalManifest{ManifestClip, InputManifest{"source_sha256": sourceSHA, "start_us": startUS, "end_us": endUS, "width": width, "height": height, "fps_num": fpsNum, "fps_den": fpsDen, "codec": codec, "profile": profile, "pixel_format": pixelFormat, "audio_policy": audioPolicy, "normalization_policy": normalizationPolicy, "processor_version": processorVersion}}
}
