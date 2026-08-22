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

// TaxonomyInput is the producer-side input for resolving the canonical
// asset taxonomy. Producers supply what they know (asset identity, source
// provider, media type); the resolver owns every derivation (asset_kind,
// source_type, namespace, semantic_role) so those decisions are not
// scattered as string literals across producers.
type TaxonomyInput struct {
	AssetID      string
	Provider     string    // youtube | artlist | stock | drive | image | voiceover | ...
	MediaType    MediaType // optional; defaults to MediaVideo
	AssetKind    AssetKind // optional override; derived when empty
	SourceType   string    // optional override; defaults to Provider
	Namespace    string    // optional override; defaults to Provider ("image" → "images")
	SemanticRole string    // optional override; defaults per provider ("image" → "visual", else "discovery")
}

// ResolveTaxonomy builds and validates the canonical taxonomy from the
// producer input. It is the SINGLE owner of the provider → (namespace,
// asset_kind, semantic_role) derivation; producers must not hand-build
// AssetTaxonomy struct literals with scattered string constants.
func ResolveTaxonomy(in TaxonomyInput) (AssetTaxonomy, error) {
	mediaType := in.MediaType
	if mediaType == "" {
		mediaType = MediaVideo
	}
	kind := in.AssetKind
	if kind == "" {
		kind = defaultAssetKind(in.Provider, mediaType)
	}
	sourceType := in.SourceType
	if sourceType == "" {
		sourceType = in.Provider
	}
	namespace := in.Namespace
	if namespace == "" {
		namespace = defaultNamespace(in.Provider)
	}
	role := in.SemanticRole
	if role == "" {
		role = defaultSemanticRole(in.Provider)
	}
	t := AssetTaxonomy{
		AssetID:      in.AssetID,
		Namespace:    namespace,
		MediaType:    mediaType,
		AssetKind:    kind,
		SourceType:   sourceType,
		SemanticRole: role,
	}
	if err := t.Validate(); err != nil {
		return AssetTaxonomy{}, fmt.Errorf("resolve media taxonomy: %w", err)
	}
	return t, nil
}

// defaultNamespace is the canonical namespace derivation. Until an SSOT
// namespace policy is defined, the provider name is the namespace, with the
// image provider converging on the historical "images" namespace.
func defaultNamespace(provider string) string {
	if provider == "image" {
		return "images"
	}
	return provider
}

// defaultSemanticRole is the canonical semantic-role derivation. Image assets
// are visual surfaces; every other provider defaults to discovery.
func defaultSemanticRole(provider string) string {
	if provider == "image" {
		return "visual"
	}
	return "discovery"
}

// defaultAssetKind derives the canonical asset_kind from the provider and
// media type when the producer does not supply an explicit kind. Every
// media_type has a derivable kind so ResolveTaxonomy can be the single
// decision point for producers that only know provider + media type.
func defaultAssetKind(provider string, mediaType MediaType) AssetKind {
	switch mediaType {
	case MediaImage:
		return AssetWebImage
	case MediaAudio:
		switch provider {
		case "voiceover":
			return AssetVoiceover
		case "bgm":
			return AssetBGM
		case "sfx", "sound_effect", "soundeffects":
			return AssetSFX
		default:
			return AssetClipAudio
		}
	case MediaText:
		return AssetMetadata
	case MediaDocument:
		return AssetDocument
	default:
		switch provider {
		case "artlist", "stock":
			return AssetStockVideo
		default:
			return AssetClip
		}
	}
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
