package entitycatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPImageCandidateValidatorRejectsPrivateURL(t *testing.T) {
	httpImageCandidateAllowLoopbackForTest = false
	defer func() { httpImageCandidateAllowLoopbackForTest = false }()
	validator := NewHTTPImageCandidateValidator(nil)
	if err := validator.Validate(context.Background(), "http://127.0.0.1:9/image.png"); err == nil {
		t.Fatal("loopback URL was accepted")
	}
}

func TestHTTPImageCandidateValidatorCapsRedirects(t *testing.T) {
	httpImageCandidateAllowLoopbackForTest = true
	defer func() { httpImageCandidateAllowLoopbackForTest = false }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.String(), http.StatusFound)
	}))
	defer server.Close()
	validator := NewHTTPImageCandidateValidator(server.Client())
	if err := validator.Validate(context.Background(), server.URL+"/loop.png"); err == nil {
		t.Fatal("redirect loop was accepted")
	}
}
