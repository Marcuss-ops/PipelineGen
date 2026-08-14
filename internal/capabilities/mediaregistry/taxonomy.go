package mediaregistry

import "fmt"

type MediaType string

const (
	MediaVideo    MediaType = "video"
	MediaImage    MediaType = "image"
	MediaAudio    MediaType = "audio"
	MediaText     MediaType = "text"
	MediaDocument MediaType = "document"
)

type AssetKind string

const (
	AssetClip           AssetKind = "clip"
	AssetStockVideo     AssetKind = "stock_video"
	AssetGeneratedVideo AssetKind = "generated_video"
	AssetRenderedVideo  AssetKind = "rendered_video"
	AssetStockImage     AssetKind = "stock_image"
	AssetWebImage       AssetKind = "web_image"
	AssetAIImage        AssetKind = "ai_image"
	AssetGraphic        AssetKind = "graphic"
	AssetVoiceover      AssetKind = "voiceover"
	AssetBGM            AssetKind = "bgm"
	AssetSFX            AssetKind = "sfx"
	AssetClipAudio      AssetKind = "clip_audio"
	AssetFinalAudio     AssetKind = "final_audio"
	AssetMetadata       AssetKind = "metadata"
	AssetDocument       AssetKind = "document"
)

type AssetTaxonomy struct {
	AssetID      string
	Namespace    string
	MediaType    MediaType
	AssetKind    AssetKind
	SourceType   string
	SemanticRole string
}

// IsZero reports whether the taxonomy carries none of its canonical
// dimensions. The MediaCommitter skips the taxonomy upsert for a zero
// taxonomy so legacy producers can converge incrementally.
func (t AssetTaxonomy) IsZero() bool {
	return t.AssetID == "" && t.Namespace == "" && t.MediaType == "" &&
		t.AssetKind == "" && t.SourceType == "" && t.SemanticRole == ""
}

func (t AssetTaxonomy) Validate() error {
	if t.AssetID == "" || t.Namespace == "" || t.MediaType == "" || t.AssetKind == "" || t.SourceType == "" {
		return fmt.Errorf("media taxonomy: asset identity and five dimensions are required")
	}
	valid := map[MediaType]map[AssetKind]bool{
		MediaVideo:    {AssetClip: true, AssetStockVideo: true, AssetGeneratedVideo: true, AssetRenderedVideo: true},
		MediaImage:    {AssetStockImage: true, AssetWebImage: true, AssetAIImage: true, AssetGraphic: true},
		MediaAudio:    {AssetVoiceover: true, AssetBGM: true, AssetSFX: true, AssetClipAudio: true, AssetFinalAudio: true},
		MediaText:     {AssetMetadata: true},
		MediaDocument: {AssetDocument: true},
	}
	if !valid[t.MediaType][t.AssetKind] {
		return fmt.Errorf("media taxonomy: asset_kind %q is invalid for media_type %q", t.AssetKind, t.MediaType)
	}
	return nil
}
