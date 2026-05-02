package assetops

// DedupeStrategy defines the strategy for handling existing assets
type DedupeStrategy string

const (
	DedupeSkip    DedupeStrategy = "skip"
	DedupeVerify  DedupeStrategy = "verify"
	DedupeReplace DedupeStrategy = "replace"
)

// AssetState holds the state of an existing asset for deduplication
type AssetState struct {
	LocalPathExists bool
	DriveLink       string
	FileHash        string
	RemoteChecksum  string
	ManifestStatus  string
}

// SkipDecision is the result of a dedupe check
type SkipDecision struct {
	Skip    bool
	Reason  string
	Replace bool
}

// DestinationSpec specifies the destination for an asset
type DestinationSpec struct {
	FolderID   string
	FolderName string
	FileName   string
	BaseDir    string
}

// ResolvedDestination is the result of resolving a destination
type ResolvedDestination struct {
	Path     string
	FolderID string
	FileName string
}

// AssetUploadResult wraps the result of an asset upload
type AssetUploadResult struct {
	FileID         string
	WebViewLink    string
	DownloadLink   string
	MD5Checksum    string
}

// NormalizeClipOptions specifies options for normalizing a video clip
type NormalizeClipOptions struct {
	Duration       *float64
	Width          *int
	Height         *int
	FPS            *int
	Codec          string
	Preset         string
	CRF            *int
	KeepAudio      bool
	DisableDuration bool
}
