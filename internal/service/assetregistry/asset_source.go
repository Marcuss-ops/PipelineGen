package assetregistry

// AssetSource identifies the source of an asset
type AssetSource string

const (
	AssetSourceArtlist   AssetSource = "artlist"
	AssetSourceYouTube   AssetSource = "youtube"
	AssetSourceStock     AssetSource = "stock"
	AssetSourceImage     AssetSource = "image"
	AssetSourceVoiceover AssetSource = "voiceover"
)

// String returns the string representation of the AssetSource
func (s AssetSource) String() string {
	return string(s)
}

// IsValid checks if the AssetSource is a known value
func (s AssetSource) IsValid() bool {
	switch s {
	case AssetSourceArtlist, AssetSourceYouTube, AssetSourceStock, AssetSourceImage, AssetSourceVoiceover:
		return true
	}
	return false
}
