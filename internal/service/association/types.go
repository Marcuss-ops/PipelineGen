package association

// ScoredMatch rappresenta un potenziale match di media con metadati.
type ScoredMatch struct {
	Title   string `json:"title"`
	Path    string `json:"path"`
	Score   int    `json:"score"`
	Source  string `json:"source"`
	Link    string `json:"link"`
	Details string `json:"details"`
	Reason  string `json:"reason,omitempty"`
}

// ScoredMatch legacy alias
type scoredMatch = ScoredMatch

// AssetSource definisce le origini degli asset.
type AssetSource string

const (
	AssetSourceStockDrive     AssetSource = "stock_drive"
	AssetSourceArtlistFolder  AssetSource = "artlist_folder"
	AssetSourceArtlistDynamic AssetSource = "artlist_dynamic"
	AssetSourceClipDrive      AssetSource = "clip_drive"
)

// FolderCandidate rappresenta una cartella candidata per l'associazione.
type FolderCandidate struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Link     string `json:"link"`
	FolderID string `json:"folder_id,omitempty"`
}
