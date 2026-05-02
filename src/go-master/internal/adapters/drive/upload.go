package drive

import (
	"bytes"
	"context"
	"fmt"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"io/ioutil"
)

type DriveUploader struct {
	service *drive.Service
}

func NewDriveUploader(credentialsFile string) (*DriveUploader, error) {
	ctx := context.Background()
	service, err := drive.NewService(ctx, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("failed to create drive service: %w", err)
	}
	return &DriveUploader{service: service}, nil
}

func (d *DriveUploader) Upload(ctx context.Context, input UploadInput) (*UploadResult, error) {
	fileContent, err := ioutil.ReadFile(input.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	file := &drive.File{
		Name:    input.Name,
		Parents: []string{input.FolderID},
	}

	_, err = d.service.Files.Create(file).Media(bytes.NewReader(fileContent)).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to upload to drive: %w", err)
	}

	return &UploadResult{
		FileID: "uploaded",
		URL:    "https://drive.google.com/file/d/uploaded/view",
	}, nil
}
