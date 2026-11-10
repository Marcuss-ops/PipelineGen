package images

import "net/http"

// RemoteFetchPort is the minimal HTTP transport surface required by image
// retrieval. The concrete client is owned and configured by composition.
type RemoteFetchPort interface {
	Do(*http.Request) (*http.Response, error)
}
