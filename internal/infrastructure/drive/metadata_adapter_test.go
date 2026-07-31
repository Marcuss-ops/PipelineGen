package drive

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type metadataProbeTransport struct {
	mockHost   string
	mockScheme string
}

func (t *metadataProbeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = t.mockScheme
	req.URL.Host = t.mockHost
	return http.DefaultTransport.RoundTrip(req)
}

type metadataProbeResponse struct {
	status int
	body   string
}

type metadataProbeServer struct {
	*httptest.Server
	mu          sync.Mutex
	response    metadataProbeResponse
	fieldsQuery string
}

func newMetadataProbeServer(response metadataProbeResponse) *metadataProbeServer {
	probe := &metadataProbeServer{response: response}
	probe.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/drive/v3/files/") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		probe.mu.Lock()
		probe.fieldsQuery = r.URL.Query().Get("fields")
		response := probe.response
		probe.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.status)
		_, _ = w.Write([]byte(response.body))
	}))
	return probe
}

func (s *metadataProbeServer) driveService(t *testing.T) *driveapi.Service {
	t.Helper()
	parsed, err := url.Parse(s.Server.URL)
	if err != nil {
		t.Fatalf("parse metadata probe URL: %v", err)
	}
	client := &http.Client{Transport: &metadataProbeTransport{
		mockHost:   parsed.Host,
		mockScheme: parsed.Scheme,
	}}
	service, err := driveapi.NewService(context.Background(),
		option.WithHTTPClient(client),
		option.WithoutAuthentication(),
		option.WithScopes(driveapi.DriveScope),
	)
	if err != nil {
		t.Fatalf("create Drive service: %v", err)
	}
	return service
}

func metadataFieldsSet(fields string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, field := range strings.FieldsFunc(fields, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	}) {
		if field != "" {
			set[field] = struct{}{}
		}
	}
	return set
}

func TestUploaderGetFileMeta_RequestsAndMapsAllMetadata(t *testing.T) {
	responseBody := `{"id":"metadata-file","name":"fight.mp4","mimeType":"video/mp4","size":"8192","webViewLink":"https://drive.google.com/file/d/metadata-file/view","parents":["folder-a","folder-b"],"trashed":false}`
	server := newMetadataProbeServer(metadataProbeResponse{status: http.StatusOK, body: responseBody})
	defer server.Close()

	uploader := &Uploader{Service: server.driveService(t)}
	meta, err := uploader.GetFileMeta(context.Background(), "metadata-file")
	if err != nil {
		t.Fatalf("GetFileMeta: %v", err)
	}
	if meta == nil {
		t.Fatal("GetFileMeta returned nil metadata")
	}

	if meta.ID != "metadata-file" {
		t.Errorf("ID = %q, want metadata-file", meta.ID)
	}
	if meta.Name != "fight.mp4" {
		t.Errorf("Name = %q, want fight.mp4", meta.Name)
	}
	if meta.MimeType != "video/mp4" {
		t.Errorf("MimeType = %q, want video/mp4", meta.MimeType)
	}
	if meta.Size != 8192 {
		t.Errorf("Size = %d, want 8192", meta.Size)
	}
	if meta.WebViewLink != "https://drive.google.com/file/d/metadata-file/view" {
		t.Errorf("WebViewLink = %q, want canonical Drive link", meta.WebViewLink)
	}
	if !reflect.DeepEqual(meta.Parents, []string{"folder-a", "folder-b"}) {
		t.Errorf("Parents = %v, want [folder-a folder-b]", meta.Parents)
	}
	if meta.Trashed {
		t.Error("Trashed = true, want false")
	}

	server.mu.Lock()
	fields := metadataFieldsSet(server.fieldsQuery)
	server.mu.Unlock()
	wantFields := []string{"id", "name", "mimeType", "size", "webViewLink", "parents", "trashed"}
	if len(fields) != len(wantFields) {
		t.Fatalf("requested fields = %q, parsed as %v; want exactly %v", server.fieldsQuery, fields, wantFields)
	}
	for _, field := range wantFields {
		if _, ok := fields[field]; !ok {
			t.Errorf("requested fields %q missing from query %q", field, server.fieldsQuery)
		}
	}
}

func TestAssetLocationResolverAdapter_RealDriveClassification(t *testing.T) {
	const (
		fileID = "probe-file"
		link   = "https://drive.google.com/file/d/probe-file/view"
	)

	tests := []struct {
		name          string
		response      metadataProbeResponse
		wantState     scriptpkg.LocationState
		wantLink      string
		wantErrorCode string
	}{
		{
			name: "valid file",
			response: metadataProbeResponse{
				status: http.StatusOK,
				body:   `{"id":"probe-file","name":"fight.mp4","mimeType":"video/mp4","size":"1024","webViewLink":"https://drive.google.com/file/d/probe-file/view","parents":["folder-a"],"trashed":false}`,
			},
			wantState: scriptpkg.LocationStateVerified,
			wantLink:  link,
		},
		{
			name: "trashed file",
			response: metadataProbeResponse{
				status: http.StatusOK,
				body:   `{"id":"probe-file","name":"fight.mp4","mimeType":"video/mp4","size":"1024","webViewLink":"https://drive.google.com/file/d/probe-file/view","parents":["folder-a"],"trashed":true}`,
			},
			wantState: scriptpkg.LocationStateTrashed,
		},
		{
			name: "metadata id mismatch",
			response: metadataProbeResponse{
				status: http.StatusOK,
				body:   `{"id":"another-file","name":"fight.mp4","mimeType":"video/mp4","size":"1024","webViewLink":"https://drive.google.com/file/d/another-file/view","parents":["folder-a"],"trashed":false}`,
			},
			wantState:     scriptpkg.LocationStateMissing,
			wantErrorCode: "FILE_ID_MISMATCH",
		},
		{
			name: "permission denied",
			response: metadataProbeResponse{
				status: http.StatusForbidden,
				body:   `{"error":{"code":403,"message":"Permission denied","status":"PERMISSION_DENIED"}}`,
			},
			wantState:     scriptpkg.LocationStateInaccessible,
			wantErrorCode: "PERMISSION_DENIED",
		},
		{
			name: "not found",
			response: metadataProbeResponse{
				status: http.StatusNotFound,
				body:   `{"error":{"code":404,"message":"File not found","status":"NOT_FOUND"}}`,
			},
			wantState:     scriptpkg.LocationStateMissing,
			wantErrorCode: "NOT_FOUND",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newMetadataProbeServer(test.response)
			defer server.Close()

			resolver := NewAssetLocationResolverAdapter(&Uploader{Service: server.driveService(t)})
			location, err := resolver.ResolveAndVerify(context.Background(), "asset-probe", fileID, link)
			if err != nil {
				t.Fatalf("ResolveAndVerify: %v", err)
			}
			if location == nil {
				t.Fatal("ResolveAndVerify returned nil location")
			}
			if location.State != test.wantState {
				t.Fatalf("state = %s, want %s", location.State, test.wantState)
			}
			if location.DriveLink != test.wantLink {
				t.Errorf("drive link = %q, want %q", location.DriveLink, test.wantLink)
			}
			if location.ErrorCode != test.wantErrorCode {
				t.Errorf("error code = %q, want %q", location.ErrorCode, test.wantErrorCode)
			}
		})
	}
}

func TestLocationVerifier_RealDriveRejectsInvalidMetadata(t *testing.T) {
	server := newMetadataProbeServer(metadataProbeResponse{
		status: http.StatusOK,
		body:   `{"id":"probe-file","name":"empty.mp4","mimeType":"video/mp4","size":"0","webViewLink":"https://drive.google.com/file/d/probe-file/view","parents":["folder-a"],"trashed":false}`,
	})
	defer server.Close()

	verifier := NewLocationVerifier(&Uploader{Service: server.driveService(t)}, nil)
	location, err := verifier.Verify(context.Background(), "asset-probe", "probe-file", "https://drive.google.com/file/d/probe-file/view")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if location.State != scriptpkg.LocationStateMissing {
		t.Fatalf("state = %s, want MISSING for zero-size non-Google file", location.State)
	}
	if location.DriveLink != "" {
		t.Fatalf("drive link = %q, want empty for unusable metadata", location.DriveLink)
	}
}

func TestUploaderGetFileMeta_PropagatesHTTPFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":{"code":403,"message":"Permission denied"}}`},
		{name: "not found", status: http.StatusNotFound, body: `{"error":{"code":404,"message":"File not found"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newMetadataProbeServer(metadataProbeResponse{status: test.status, body: test.body})
			defer server.Close()

			uploader := &Uploader{Service: server.driveService(t)}
			_, err := uploader.GetFileMeta(context.Background(), "probe-file")
			if err == nil {
				t.Fatal("GetFileMeta returned nil error for HTTP failure")
			}
			var apiErr *googleapi.Error
			if !errors.As(err, &apiErr) || apiErr.Code != test.status {
				t.Fatalf("GetFileMeta error does not preserve HTTP %d API error: %T %v", test.status, err, err)
			}
		})
	}
}
