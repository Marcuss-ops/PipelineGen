package assetop

type Operation string

const (
	OperationDiscover Operation = "discover"
	OperationGenerate Operation = "generate"
	OperationDownload Operation = "download"
	OperationProcess  Operation = "process"
	OperationUpload   Operation = "upload"
	OperationSync     Operation = "sync"
	OperationFinalize Operation = "finalize"
)

type AssetType string

const (
	AssetTypeVideo    AssetType = "video"
	AssetTypeAudio    AssetType = "audio"
	AssetTypeImage    AssetType = "image"
	AssetTypeDocument AssetType = "document"
)

type Source string

const (
	SourceArtlist   Source = "artlist"
	SourceYouTube   Source = "youtube"
	SourceVoiceover Source = "voiceover"
	SourceDrive     Source = "drive"
)

type AssetOperation struct {
	Operation   Operation              `json:"operation" yaml:"operation"`
	AssetType   AssetType              `json:"asset_type" yaml:"asset_type"`
	Source      Source                 `json:"source" yaml:"source"`
	SourceID    string                 `json:"source_id" yaml:"source_id"`
	Input       map[string]interface{} `json:"input" yaml:"input"`
	Policy      Policy                 `json:"policy" yaml:"policy"`
	Destination Destination            `json:"destination" yaml:"destination"`
}
