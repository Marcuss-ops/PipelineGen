package models

// SourceType rappresenta la fonte di un media
type SourceType string

const (
	// SourceStock indica media di tipo stock footage
	SourceStock SourceType = "stock"
	// SourceArtlist indica media da Artlist
	SourceArtlist SourceType = "artlist"
	// SourceYoutubeClip indica clip da YouTube
	SourceYoutubeClip SourceType = "youtube_clip"
	// SourceClipDrive indica clip da Google Drive
	SourceClipDrive SourceType = "clip_drive"
	// SourceImage indica immagini
	SourceImage SourceType = "image"
	// SourceGenerated indica contenuto generato (script, voiceover)
	SourceGenerated SourceType = "generated"
)

// IsValid verifica se il SourceType è valido
func (s SourceType) IsValid() bool {
	switch s {
	case SourceStock, SourceArtlist, SourceYoutubeClip, SourceClipDrive, SourceImage, SourceGenerated:
		return true
	}
	return false
}

// MediaType rappresenta il tipo di media
type MediaType string

const (
	// MediaTypeStock indica footage stock
	MediaTypeStock MediaType = "stock"
	// MediaTypeClip indica un clip video
	MediaTypeClip MediaType = "clip"
	// MediaTypeImage indica un'immagine
	MediaTypeImage MediaType = "image"
	// MediaTypeAudio indica un file audio (voiceover)
	MediaTypeAudio MediaType = "audio"
	// MediaTypeDocument indica un documento (Google Doc)
	MediaTypeDocument MediaType = "document"
)

// IsValid verifica se il MediaType è valido
func (m MediaType) IsValid() bool {
	switch m {
	case MediaTypeStock, MediaTypeClip, MediaTypeImage, MediaTypeAudio, MediaTypeDocument:
		return true
	}
	return false
}

// AssetStatus rappresenta lo stato di un asset media
type AssetStatus string

const (
	// AssetStatusActive indica asset attivo e disponibile
	AssetStatusActive AssetStatus = "active"
	// AssetStatusArchived indica asset archiviato
	AssetStatusArchived AssetStatus = "archived"
	// AssetStatusDeleted indica asset cancellato (soft delete)
	AssetStatusDeleted AssetStatus = "deleted"
	// AssetStatusProcessing indica asset in elaborazione
	AssetStatusProcessing AssetStatus = "processing"
	// AssetStatusFailed indica asset con errore
	AssetStatusFailed AssetStatus = "failed"
)

// IsValid verifica se l'AssetStatus è valido
func (s AssetStatus) IsValid() bool {
	switch s {
	case AssetStatusActive, AssetStatusArchived, AssetStatusDeleted, AssetStatusProcessing, AssetStatusFailed:
		return true
	}
	return false
}
