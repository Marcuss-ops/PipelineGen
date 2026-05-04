package assetindex

import "time"

type AssetRecord struct {
    AssetID      string
    AssetType    string
    Source       string
    SourceID     string
    OperationKey string

    GroupName string
    Subfolder string

    LocalPath    string
    DriveLink    string
    DownloadLink string

    FileHash    string
    ContentHash string

    Status   string
    Metadata string

    CreatedAt time.Time
    UpdatedAt time.Time
}
