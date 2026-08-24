package delivery

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestIngestImage_RepositoryNilFailsClosed(t *testing.T) {
	svc := &ImageStorageService{}

	asset, err := svc.IngestImage(
		context.Background(),
		"subject",
		"",
		"",
		bytes.NewReader([]byte("image bytes")),
		"image.jpg",
		"https://example.test/image.jpg",
		"test image",
		nil,
		true,
		true,
	)
	if asset != nil {
		t.Fatalf("asset = %#v, want nil when repository is unavailable", asset)
	}
	if !errors.Is(err, ErrImageRepositoryUnavailable) {
		t.Fatalf("err = %v, want errors.Is(ErrImageRepositoryUnavailable)", err)
	}
}
