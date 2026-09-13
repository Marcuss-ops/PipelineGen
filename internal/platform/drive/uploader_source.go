// Package drive — uploader_source.go.
//
// uploadSource is the byte source for ONE Drive upload. A local file backs both
// interfaces directly. A remote object-store URL is the locator-first case:
// clip.render commits the certified object-store locator and the Drive outbox
// streams it here, so the rendered video is never written to local disk.
//
// Google's upload API needs io.ReaderAt for the resumable path (the only path
// that reliably handles multi-hundred-MB artifacts) and io.Reader for the
// simple media path. An HTTP body is only an io.Reader, so httpObjectSource
// implements ReadAt with HTTP Range requests — the object store already serves
// them — which is what makes a disk-free resumable upload possible.
package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// uploadSource is the byte source for one upload: sequential read for the
// simple path, random access for the resumable path, and close for both.
type uploadSource interface {
	io.Reader
	io.ReaderAt
	io.Closer
}

// httpObjectSource streams a remote object store artifact. Reads use one lazy
// sequential GET; ReadAt issues a bounded Range GET per call. Close releases
// the sequential body, if one was opened.
type httpObjectSource struct {
	ctx  context.Context
	url  string
	size int64

	mu   sync.Mutex
	body io.ReadCloser
}

func (s *httpObjectSource) Read(p []byte) (int, error) {
	s.mu.Lock()
	if s.body == nil {
		resp, err := s.get("")
		if err != nil {
			s.mu.Unlock()
			return 0, err
		}
		s.body = resp.Body
	}
	body := s.body
	s.mu.Unlock()
	return body.Read(p)
}

func (s *httpObjectSource) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("object store read: negative offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= s.size {
		return 0, io.EOF
	}
	end := off + int64(len(p)) - 1
	if end >= s.size {
		end = s.size - 1
	}
	resp, err := s.get(fmt.Sprintf("bytes=%d-%d", off, end))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && off > 0 {
		if _, err := io.CopyN(io.Discard, resp.Body, off); err != nil {
			return 0, fmt.Errorf("discard unhandled range prefix: %w", err)
		}
	}
	n, err := io.ReadFull(resp.Body, p[:end-off+1])
	if err == io.ErrUnexpectedEOF {
		err = io.EOF
	}
	return n, err
}

func (s *httpObjectSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.body == nil {
		return nil
	}
	err := s.body.Close()
	s.body = nil
	return err
}

func (s *httpObjectSource) get(rangeHeader string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("object store request: %w", err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("object store GET: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("object store GET %s: HTTP %d", s.url, resp.StatusCode)
	}
	return resp, nil
}
