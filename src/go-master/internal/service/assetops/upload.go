package assetops

import (
	"context"

	"velox/go-master/internal/upload/drive"
)

// UploadToDrive uploads a file to Google Drive using the provided uploader
func UploadToDrive(ctx context.Context, uploader *drive.Uploader, path, folderID, filename string) (*AssetUploadResult, error) {
	result, err := uploader.UploadFile(ctx, path, folderID, filename)
	if err != nil {
		return nil, err
	}
	return &AssetUploadResult{
		FileID:       result.FileID,
		WebViewLink:  result.WebViewLink,
		DownloadLink: result.DownloadLink,
		MD5Checksum:  result.MD5Checksum,
	}, nil
}
